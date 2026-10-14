// Command sentrel-loadtest simulates many Sentrel agents against a running
// server, to measure how many one server can handle.
//
// Each simulated agent has its own identity (key pair, client certificate and
// hostname "loadtest-0001", ...). It enrols once, then speaks the real agent
// protocol: a Connect stream, a signed Hello, periodic MetricBatch (about 60
// samples) and HostState messages, and one Inventory per connection. Identities
// are cached in -data so later runs skip enrolment.
//
// Enrolment tokens are single-use, so the tool logs in to the server API
// (-user / -password) and creates one token per agent it has to enrol. The
// server rate-limits enrolment to 20 per minute per source IP; when the server
// is on a loopback address the tool therefore enrols from several loopback
// source addresses (127.x.y.1), which Windows and Linux both accept.
//
//	sentrel-loadtest -server http://127.0.0.1:18090 -user admin -password ... \
//	    -agents 200 -interval 30s -ramp 60s -duration 10m -data ./lt-identities
//
// This is a tool for testing your own server. Do not point it at a server you
// do not run.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type stats struct {
	connected    atomic.Int64 // agents currently connected
	reconnects   atomic.Int64
	connectFails atomic.Int64
	reports      atomic.Int64 // metric batches sent
	states       atomic.Int64
	inventories  atomic.Int64
	bytesSent    atomic.Int64
	sendErrors   atomic.Int64

	mu       sync.Mutex
	helloLat []time.Duration // hello to welcome
}

func (s *stats) addHello(d time.Duration) {
	s.mu.Lock()
	s.helloLat = append(s.helloLat, d)
	s.mu.Unlock()
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(float64(len(d)-1)*p)]
}

func main() {
	var (
		server    = flag.String("server", "http://127.0.0.1:8080", "server URL (http:// or https://)")
		user      = flag.String("user", "admin", "admin username, used to create enrolment tokens and to probe the API")
		password  = flag.String("password", os.Getenv("SENTREL_PASSWORD"), "admin password (or SENTREL_PASSWORD)")
		agents    = flag.Int("agents", 50, "number of simulated agents")
		interval  = flag.Duration("interval", 30*time.Second, "metric report interval per agent")
		ramp      = flag.Duration("ramp", 0, "spread agent connects evenly over this period")
		duration  = flag.Duration("duration", 0, "stop after this long (0 = until interrupted)")
		data      = flag.String("data", "", "directory caching agent identities so reruns skip enrolment (default: a temp dir, removed on exit)")
		prefix    = flag.String("prefix", "loadtest", "hostname prefix; agents are named PREFIX-0001, PREFIX-0002, ...")
		packages  = flag.Int("packages", 450, "installed packages in each agent's inventory")
		insecure  = flag.Bool("insecure", false, "https: skip server certificate verification")
		spread    = flag.Bool("spread-source", true, "enrol from several loopback source addresses (loopback servers only)")
		probe     = flag.Duration("probe", 15*time.Second, "how often to time GET /api/v1/hosts (0 = off)")
		enrolPar  = flag.Int("enrol-parallel", 8, "concurrent enrolments")
		enrolOnly = flag.Bool("enrol-only", false, "enrol and cache identities, then exit without connecting")
		verbose   = flag.Bool("v", false, "log every connection failure")
	)
	flag.Parse()
	if *agents < 1 || *interval < time.Second {
		fmt.Fprintln(os.Stderr, "need -agents >= 1 and -interval >= 1s")
		os.Exit(2)
	}
	lvl := slog.LevelWarn
	if *verbose {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	cacheDir := *data
	if cacheDir == "" {
		d, err := os.MkdirTemp("", "sentrel-loadtest-")
		if err != nil {
			fatal(err)
		}
		defer os.RemoveAll(d)
		cacheDir = d
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	api := newAPI(*server, *user, *password, *insecure)
	cfg := simConfig{
		server: *server, interval: *interval, packages: *packages, insecure: *insecure,
		spread: *spread && isLoopback(*server), log: log, api: api, cacheDir: cacheDir, prefix: *prefix,
	}

	// Phase 1: identities (enrol whatever is not cached).
	fmt.Printf("loading or enrolling %d identities in %s\n", *agents, cacheDir)
	ids := make([]*identity, *agents)
	var missing int
	for i := range ids {
		if id, err := loadIdentity(cacheDir, i); err == nil {
			ids[i] = id
		} else {
			missing++
		}
	}
	if missing > 0 {
		if *password == "" {
			fatal(fmt.Errorf("%d agents need enrolling: -password (or SENTREL_PASSWORD) is required", missing))
		}
		if err := api.login(ctx); err != nil {
			fatal(fmt.Errorf("login: %w", err))
		}
		fmt.Printf("enrolling %d new agents\n", missing)
		if err := enrolAll(ctx, cfg, ids, *enrolPar); err != nil {
			fatal(err)
		}
	}
	if ctx.Err() != nil || *enrolOnly {
		return
	}

	// Phase 2: run. The duration clock starts once identities are ready.
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	st := &stats{}
	start := time.Now()
	var wg sync.WaitGroup
	for i, id := range ids {
		delay := time.Duration(0)
		if *ramp > 0 {
			delay = *ramp * time.Duration(i) / time.Duration(*agents)
		}
		wg.Add(1)
		go func(i int, id *identity, delay time.Duration) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			newSimAgent(cfg, id, i, st).run(ctx)
		}(i, id, delay)
	}

	if *probe > 0 && *password != "" {
		go probeLoop(ctx, api, *probe)
	}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	var lastReports int64
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
loop:
	for {
		select {
		case <-done:
			break loop
		case <-tick.C:
			r := st.reports.Load()
			fmt.Printf("[%s] connected=%d/%d reports=%d (%.1f/s) reconnects=%d connect_fails=%d send_errors=%d sent=%.1fMB\n",
				time.Since(start).Round(time.Second), st.connected.Load(), *agents, r, float64(r-lastReports)/10,
				st.reconnects.Load(), st.connectFails.Load(), st.sendErrors.Load(), float64(st.bytesSent.Load())/1e6)
			lastReports = r
		}
	}
	st.mu.Lock()
	lat := append([]time.Duration(nil), st.helloLat...)
	st.mu.Unlock()
	fmt.Printf("\nfinished after %s: reports=%d states=%d inventories=%d reconnects=%d connect_fails=%d send_errors=%d\n",
		time.Since(start).Round(time.Second), st.reports.Load(), st.states.Load(), st.inventories.Load(),
		st.reconnects.Load(), st.connectFails.Load(), st.sendErrors.Load())
	fmt.Printf("hello->welcome latency: p50=%s p95=%s max=%s (n=%d)\n",
		pct(lat, 0.5).Round(time.Millisecond), pct(lat, 0.95).Round(time.Millisecond), pct(lat, 1).Round(time.Millisecond), len(lat))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// probeLoop times GET /api/v1/hosts and prints each sample.
func probeLoop(ctx context.Context, api *apiClient, every time.Duration) {
	if err := api.login(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "probe disabled:", err)
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d, n, err := api.timeHosts(ctx)
			if err != nil {
				fmt.Printf("  probe GET /api/v1/hosts: error: %v\n", err)
				continue
			}
			fmt.Printf("  probe GET /api/v1/hosts: %dms (%d bytes)\n", d.Milliseconds(), n)
		}
	}
}
