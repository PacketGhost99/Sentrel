package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
	"sentrel/internal/jobpolicy"
	"sentrel/internal/sigs"
)

// Agent is the running agent.
type Agent struct {
	cfg    *Config
	log    *slog.Logger
	cert   tls.Certificate
	key    *ecdsa.PrivateKey
	caPEM  []byte
	jobKey ed25519.PublicKey

	// Write policy, fixed from the local config at start-up.
	writePolicy string
	opKeys      []ed25519.PublicKey

	collector *Collector
	failures  *failureCounter
	ps        portscanState
	secSource string
	priv      privRunner
	privOut   privStreamer

	mu        sync.Mutex
	conf      *pb.AgentConfig
	out       chan *pb.AgentMessage // current connection's send queue (nil when offline)
	connected bool
	backlog   []*pb.AgentMessage // metric batches buffered while offline

	collectNow chan struct{}
	invNow     chan struct{}
	blocksNow  chan struct{}
	blocks     []*pb.BlockedIP
	jobsMu     sync.Mutex
	seenJobs   map[string]time.Time
	jobSem     chan struct{}
	updating   bool
}

const maxBacklog = 300 // ~2.5h at 30s

// New loads credentials and prepares an agent.
func New(cfg *Config, log *slog.Logger) (*Agent, error) {
	cert, err := tls.LoadX509KeyPair(cfg.certFile(), cfg.keyFile())
	if err != nil {
		return nil, fmt.Errorf("load agent certificate: %w (re-register the agent)", err)
	}
	k, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("agent key must be ECDSA")
	}
	ca, _ := os.ReadFile(cfg.caFile())
	jk, err := readJobKey(cfg.JobKeyFile())
	if err != nil {
		return nil, fmt.Errorf("job key %s: %w", cfg.JobKeyFile(), err)
	}
	// Self-heal the folder ACLs of existing Windows installs.
	if err := hardenStateDirs(cfg.StateDir, cfg.Path()); err != nil {
		log.Warn("could not protect the state directory", "err", err)
	}
	policy, err := jobpolicy.NormalizePolicy(cfg.WriteJobs)
	if err != nil {
		return nil, err
	}
	opKeys, err := jobpolicy.ParseKeys(cfg.OperatorKeys)
	if err != nil {
		return nil, err
	}
	if policy == jobpolicy.PolicyOperator && len(opKeys) == 0 {
		log.Warn("write_jobs is operator but no operator_keys are configured: all write jobs will be refused")
	}
	run, stream := newPrivRunner()
	a := &Agent{cfg: cfg, log: log, cert: cert, key: k, caPEM: ca, jobKey: jk, writePolicy: policy, opKeys: opKeys, collector: NewCollector(cfg.StateDir),
		failures: newFailureCounter(), priv: run, privOut: stream, conf: defaultConfig(),
		collectNow: make(chan struct{}, 1), invNow: make(chan struct{}, 1), blocksNow: make(chan struct{}, 1),
		seenJobs: map[string]time.Time{}, jobSem: make(chan struct{}, 2)}
	a.loadSeenJobs()
	return a, nil
}

func defaultConfig() *pb.AgentConfig {
	return &pb.AgentConfig{IntervalSeconds: 30, InventoryIntervalSeconds: 3600, TopProcesses: 10, CollectServices: true, CollectPorts: true,
		CollectDocker: true, BruteforceEnabled: true, SecurityIntervalSeconds: 60, CollectTemperatures: true, PortscanEnabled: true, PortscanThresholdPerMin: scanDefaultThreshold}
}

func (a *Agent) config() *pb.AgentConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conf
}

func kick(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// send queues a message; metric batches are buffered while offline.
func (a *Agent) send(m *pb.AgentMessage) {
	a.mu.Lock()
	out := a.out
	if out == nil {
		if _, ok := m.Msg.(*pb.AgentMessage_Metrics); ok {
			a.backlog = append(a.backlog, m)
			if len(a.backlog) > maxBacklog {
				a.backlog = a.backlog[len(a.backlog)-maxBacklog:]
			}
		}
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	select {
	case out <- m:
	case <-time.After(30 * time.Second):
		a.log.Warn("send queue blocked; dropping message")
	}
}

// Run connects and collects until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("sentrel agent starting", "version", AgentVersion(), "server", a.cfg.ServerURL, "agent_id", a.cfg.AgentID)
	marker := a.checkUpdateMarker()
	if a.config().BruteforceEnabled {
		a.startSecurityWatch(ctx)
	}
	var wg sync.WaitGroup
	for _, f := range []func(context.Context){a.metricsLoop, a.inventoryLoop, a.securityLoop, a.portscanLoop, a.blocksLoop, a.diskHealthLoop, a.housekeeping} {
		wg.Add(1)
		go func(f func(context.Context)) { defer wg.Done(); f(ctx) }(f)
	}
	if marker != nil {
		go a.updateWatchdog(ctx)
	}
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.session(ctx, marker)
		marker = nil
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, errRevoked) {
			a.log.Error("this agent has been revoked or deleted on the server; re-register to monitor this host again")
			_ = os.WriteFile(filepath.Join(a.cfg.StateDir, "REVOKED"), []byte(time.Now().Format(time.RFC3339)+"\n"), 0o600)
			<-ctx.Done()
			break
		}
		if time.Since(start) > time.Minute {
			backoff = 2 * time.Second
		}
		jitter := time.Duration(mrand.Int64N(int64(backoff / 2)))
		a.log.Warn("disconnected from server", "err", err, "retry_in", (backoff + jitter).Round(time.Second))
		select {
		case <-ctx.Done():
		case <-time.After(backoff + jitter):
		}
		backoff = time.Duration(math.Min(float64(backoff*2), float64(60*time.Second)))
	}
	a.collector.SaveHistory()
	a.saveSeenJobs()
	wg.Wait()
	return nil
}

var errRevoked = errors.New("revoked")

func (a *Agent) hello(selfTest bool, updatedFrom string) (*pb.AgentMessage, error) {
	ms := time.Now().UnixMilli()
	h := sha256.Sum256(sigs.HelloMessage(a.cfg.AgentID, ms))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, h[:])
	if err != nil {
		return nil, err
	}
	certPEM, _ := os.ReadFile(a.cfg.certFile())
	return &pb.AgentMessage{Msg: &pb.AgentMessage_Hello{Hello: &pb.Hello{
		AgentId: a.cfg.AgentID, SchemaVersion: uint32(pb.SchemaVersion_SCHEMA_VERSION_CURRENT), AgentVersion: AgentVersion(),
		Host: CollectHostInfo(context.Background()), CertPem: certPEM, UnixMs: ms, Signature: sig, UpdatedFrom: updatedFrom, SelfTest: selfTest,
		WritePolicy: a.writePolicy, OperatorKeys: append([]string(nil), a.cfg.OperatorKeys...),
	}}}, nil
}

// session runs one connection until it fails.
func (a *Agent) session(ctx context.Context, marker *updateMarker) error {
	conn, err := dial(ctx, a.cfg.ServerURL, a.caPEM, &a.cert, "")
	if err != nil {
		return err
	}
	defer conn.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := pb.NewAgentServiceClient(conn).Connect(sctx)
	if err != nil {
		return friendlyErr(err)
	}
	from := ""
	if marker != nil {
		from = marker.From
	}
	h, err := a.hello(false, from)
	if err != nil {
		return err
	}
	if err := stream.Send(h); err != nil {
		return friendlyErr(err)
	}
	first, err := stream.Recv()
	if err != nil {
		return friendlyErr(err)
	}
	if gb := first.GetGoodbye(); gb != nil {
		if gb.Revoked {
			return errRevoked
		}
		return fmt.Errorf("server closed the connection: %s", gb.Reason)
	}
	w := first.GetWelcome()
	if w == nil {
		return errors.New("expected welcome")
	}
	a.applyConfig(w.Config)
	if skew := time.Since(time.UnixMilli(w.ServerUnixMs)); skew > time.Minute || skew < -time.Minute {
		a.log.Warn("clock differs from the server", "skew", skew.Round(time.Second))
	}
	if marker != nil {
		// A successful connection confirms the update: close the privileged
		// rollback window before dropping the agent-side marker.
		if out, err := a.priv(ctx, time.Minute, "confirm-update"); err != nil {
			a.log.Warn("could not clear the protected update marker", "err", err, "output", string(out))
		}
	}
	_ = os.Remove(markerPath(a.cfg.StateDir))
	_ = os.Remove(filepath.Join(a.cfg.StateDir, "REVOKED"))
	a.log.Info("connected to server", "host_id", w.HostId, "server_version", w.ServerVersion)

	out := make(chan *pb.AgentMessage, 256)
	a.mu.Lock()
	backlog := a.backlog
	a.backlog = nil
	a.out = out
	a.connected = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.out = nil
		a.connected = false
		a.mu.Unlock()
	}()

	sendErr := make(chan error, 1)
	go func() {
		for _, m := range backlog {
			if err := stream.Send(m); err != nil {
				sendErr <- err
				return
			}
		}
		for {
			select {
			case <-sctx.Done():
				sendErr <- nil
				return
			case m := <-out:
				if err := stream.Send(m); err != nil {
					sendErr <- err
					return
				}
			}
		}
	}()
	kick(a.collectNow)
	kick(a.invNow)
	kick(a.blocksNow)

	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			a.handle(sctx, m)
		}
	}()
	select {
	case err := <-sendErr:
		return friendlyErr(err)
	case err := <-recvErr:
		return friendlyErr(err)
	case <-ctx.Done():
		return nil
	}
}

func friendlyErr(err error) error {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.PermissionDenied:
			if s.Message() == "agent certificate revoked or host deleted" {
				return errRevoked
			}
			return errors.New(s.Message())
		case codes.Unauthenticated, codes.InvalidArgument, codes.FailedPrecondition, codes.ResourceExhausted:
			return errors.New(s.Message())
		case codes.Unavailable:
			return fmt.Errorf("server unavailable: %s", s.Message())
		}
	}
	return err
}

func (a *Agent) applyConfig(c *pb.AgentConfig) {
	if c == nil {
		return
	}
	if c.IntervalSeconds < 5 {
		c.IntervalSeconds = 30
	}
	a.mu.Lock()
	prev := a.conf
	a.conf = c
	a.mu.Unlock()
	if prev.IntervalSeconds != c.IntervalSeconds {
		kick(a.collectNow)
	}
}

func (a *Agent) handle(ctx context.Context, m *pb.ServerMessage) {
	switch x := m.Msg.(type) {
	case *pb.ServerMessage_Config:
		a.applyConfig(x.Config)
		a.log.Info("configuration updated", "interval", x.Config.IntervalSeconds)
	case *pb.ServerMessage_Ping:
		a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_Pong{Pong: &pb.Pong{UnixMs: time.Now().UnixMilli()}}})
	case *pb.ServerMessage_Job:
		go a.runJob(ctx, x.Job)
	case *pb.ServerMessage_Goodbye:
		a.log.Warn("server said goodbye", "reason", x.Goodbye.Reason, "revoked", x.Goodbye.Revoked)
	}
}

// --- loops ---

func (a *Agent) metricsLoop(ctx context.Context) {
	for {
		iv := time.Duration(a.config().IntervalSeconds) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
		case <-a.collectNow:
		}
		cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		batch, state := a.collector.Collect(cctx, a.config())
		cancel()
		a.mu.Lock()
		state.BlockedIps = a.blocks
		a.mu.Unlock()
		a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_Metrics{Metrics: batch}})
		a.mu.Lock()
		online := a.out != nil
		a.mu.Unlock()
		if online {
			a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_State{State: state}})
		}
	}
}

func (a *Agent) inventoryLoop(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-a.invNow:
		}
		a.sendInventory(ctx, a.config().RefreshPackageIndex)
		iv := time.Duration(a.config().InventoryIntervalSeconds) * time.Second
		if iv < 5*time.Minute {
			iv = time.Hour
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(iv)
	}
}

func (a *Agent) sendInventory(ctx context.Context, refresh bool) *pb.Inventory {
	a.mu.Lock()
	online := a.out != nil
	a.mu.Unlock()
	if !online {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	start := time.Now()
	inv := CollectInventory(cctx, refresh, a.priv)
	a.log.Info("inventory collected", "installed", len(inv.Installed), "upgradable", len(inv.Upgradable), "took", time.Since(start).Round(time.Millisecond), "error", inv.Error)
	a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_Inventory{Inventory: inv}})
	return inv
}

func (a *Agent) startSecurityWatch(ctx context.Context) {
	src := watchAuthLog(ctx, a.failures, a.log)
	if src == "" {
		src = watchWindowsSecurity(ctx, a.failures, a.log)
	}
	a.secSource = src
	if src != "" {
		a.log.Info("brute-force detection active", "source", src)
	}
}

func (a *Agent) securityLoop(ctx context.Context) {
	f2bAt := time.Time{}
	var f2b *pb.Fail2Ban
	for {
		iv := time.Duration(a.config().SecurityIntervalSeconds) * time.Second
		if iv < 15*time.Second {
			iv = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
		}
		cfg := a.config()
		psStatus, psScans, psSend := a.portscanDrain()
		if !cfg.BruteforceEnabled && !cfg.Fail2BanEnabled && !psSend {
			a.failures.Flush()
			continue
		}
		start, end, fl := a.failures.Flush()
		rep := &pb.SecurityReport{WindowStartMs: start.UnixMilli(), WindowEndMs: end.UnixMilli(), FailedLogins: fl, Source: a.secSource,
			Portscans: psScans, PortscanStatus: psStatus}
		if cfg.Fail2BanEnabled && time.Since(f2bAt) > 5*time.Minute {
			f2bAt = time.Now()
			if out, err := a.priv(ctx, time.Minute, "fail2ban-status"); err == nil {
				var f pb.Fail2Ban
				if protojson.Unmarshal(out, &f) == nil {
					f2b = &f
				}
			}
		}
		if f2b != nil && f2b.Available {
			rep.Fail2Ban = f2b
		}
		a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_Security{Security: rep}})
	}
}

// blocksLoop refreshes the list of firewall blocks (and expires old ones on
// iptables/Windows) every five minutes or on demand.
func (a *Agent) blocksLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		case <-a.blocksNow:
		}
		out, err := a.priv(ctx, time.Minute, "list-blocks")
		if err != nil {
			continue
		}
		var entries []BlockEntry
		if json.Unmarshal(out, &entries) != nil {
			continue
		}
		var bl []*pb.BlockedIP
		for _, e := range entries {
			bl = append(bl, &pb.BlockedIP{Ip: e.IP, ExpiresUnix: e.Expires, Backend: e.Backend})
		}
		a.mu.Lock()
		a.blocks = bl
		a.mu.Unlock()
	}
}

func (a *Agent) housekeeping(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.collector.SaveHistory()
			a.saveSeenJobs()
		}
	}
}

// --- job de-duplication ---

// seenSlack keeps an ID past its job's expiry to absorb clock skew.
const seenSlack = 10 * time.Minute

type seenFileDoc struct {
	Version int              `json:"version"`
	Jobs    map[string]int64 `json:"jobs"` // job ID -> remember-until (unix seconds)
}

func (a *Agent) seenFile() string { return filepath.Join(a.cfg.StateDir, "jobs-seen.json") }

// loadSeenJobs restores accepted job IDs so a restart cannot replay a job.
func (a *Agent) loadSeenJobs() {
	b, err := os.ReadFile(a.seenFile())
	if err != nil {
		return
	}
	var doc seenFileDoc
	if json.Unmarshal(b, &doc) != nil {
		return
	}
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	now := time.Now()
	for k, v := range doc.Jobs {
		if exp := time.Unix(v, 0); exp.After(now) {
			a.seenJobs[k] = exp
		}
	}
}

// saveSeenJobs prunes expired IDs and writes the rest atomically.
func (a *Agent) saveSeenJobs() {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	a.saveSeenLocked()
}

func (a *Agent) saveSeenLocked() {
	doc := seenFileDoc{Version: 1, Jobs: map[string]int64{}}
	now := time.Now()
	for k, exp := range a.seenJobs {
		if exp.After(now) {
			doc.Jobs[k] = exp.Unix()
		} else {
			delete(a.seenJobs, k)
		}
	}
	b, _ := json.Marshal(doc)
	if err := safeWriteFile(a.seenFile(), b, 0o600, -1, -1); err != nil {
		a.log.Warn("could not persist seen jobs", "err", err)
	}
}

// --- self-update watchdog ---

// checkUpdateMarker handles a pending self-update at startup: after three
// failed starts the previous binary is restored.
func (a *Agent) checkUpdateMarker() *updateMarker {
	m, err := readMarker(a.cfg.StateDir)
	if err != nil {
		return nil
	}
	m.Attempts++
	_ = writeMarker(a.cfg.StateDir, m)
	a.log.Info("starting after self-update", "from", m.From, "to", m.To, "attempt", m.Attempts)
	if m.Attempts > 3 {
		a.rollback("new version failed to start three times")
	}
	return m
}

func (a *Agent) updateWatchdog(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Minute):
	}
	if _, err := readMarker(a.cfg.StateDir); err == nil {
		a.rollback("new version could not connect to the server within 3 minutes")
	}
}

func (a *Agent) rollback(reason string) {
	a.log.Error("rolling back self-update", "reason", reason)
	if out, err := a.priv(context.Background(), time.Minute, "rollback-update"); err != nil {
		a.log.Error("rollback failed", "err", err, "output", string(out))
		_ = os.Remove(markerPath(a.cfg.StateDir))
		return
	}
	os.Exit(1) // service manager restarts the previous version
}

// SelfTest connects once with self_test set and returns whether the server
// accepted this binary's credentials (used before swapping binaries).
func SelfTest(ctx context.Context, cfg *Config) error {
	a, err := New(cfg, slogDiscard())
	if err != nil {
		return err
	}
	conn, err := dial(ctx, cfg.ServerURL, a.caPEM, &a.cert, "")
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := pb.NewAgentServiceClient(conn).Connect(ctx)
	if err != nil {
		return friendlyErr(err)
	}
	h, err := a.hello(true, "")
	if err != nil {
		return err
	}
	if err := stream.Send(h); err != nil {
		return friendlyErr(err)
	}
	m, err := stream.Recv()
	if err != nil {
		return friendlyErr(err)
	}
	if m.GetWelcome() == nil {
		return errors.New("server did not accept the self-test")
	}
	_ = stream.CloseSend()
	return nil
}

func clone[T proto.Message](m T) T { return proto.Clone(m).(T) }
