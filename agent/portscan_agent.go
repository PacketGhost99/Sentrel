package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	pb "sentrel/gen/sentrelv1"
)

const (
	portscanPoll        = 30 * time.Second // scanner-set read interval
	portscanSyncMin     = time.Minute      // at most one scan-sync per minute
	portscanRetryAfter  = 5 * time.Minute  // after a failed scan-sync
	portscanStatusEvery = 10 * time.Minute // status is re-sent at least this often
)

// portscanLoop keeps the kernel rules current and reads flagged sources
// back. All real work happens in the kernel; this wakes every 30 s, reads
// /proc/net/tcp and one small JSON blob.
func (a *Agent) portscanLoop(ctx context.Context) {
	if runtime.GOOS != "linux" {
		reason := "not supported on this operating system"
		if runtime.GOOS == "windows" {
			reason = "not yet supported on Windows"
		}
		a.setPortscanStatus(&pb.PortScanStatus{Reason: reason})
		return
	}
	tracker := newScanTracker()
	srv := &serverAddrs{host: serverHost(a.cfg.ServerURL)}
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}
	t := time.NewTicker(portscanPoll)
	defer t.Stop()
	for {
		a.portscanTick(ctx, tracker, srv)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) portscanTick(ctx context.Context, tracker *scanTracker, srv *serverAddrs) {
	cfg := a.config()
	ps := &a.ps
	ps.mu.Lock()
	wasActive := (ps.status != nil && ps.status.Active) || ps.syncedKey != ""
	ps.mu.Unlock()

	if !cfg.PortscanEnabled {
		if wasActive {
			_, _ = a.priv(ctx, 30*time.Second, "scan-off")
			ps.mu.Lock()
			ps.syncedKey = ""
			ps.mu.Unlock()
		}
		st := &pb.PortScanStatus{Supported: nftPresent(), Reason: "disabled in settings"}
		if !st.Supported {
			st.Reason = "nftables is not installed on this host"
		}
		a.setPortscanStatus(st)
		tracker.Observe(time.Now(), nil)
		return
	}

	threshold := int(cfg.PortscanThresholdPerMin)
	if threshold <= 0 {
		threshold = scanDefaultThreshold
	}
	ports, err := listeningTCPPorts()
	if err != nil {
		a.setPortscanStatus(&pb.PortScanStatus{Reason: err.Error()})
		return
	}
	allow := validAllowEntries(cfg.PortscanAllowlist)
	allow = append(allow, srv.addrs(ctx)...)
	key := scanSyncKey(ports, threshold, allow, nil)

	ps.mu.Lock()
	need := key != ps.syncedKey
	recent := !ps.syncedAt.IsZero() && time.Since(ps.syncedAt) < portscanSyncMin
	failed := !ps.failAt.IsZero() && time.Since(ps.failAt) < portscanRetryAfter && key == ps.failKey
	ps.mu.Unlock()
	if need && !recent && !failed {
		args := []string{formatPortList(ports), "--threshold", itoa(int64(threshold))}
		if len(allow) > 0 {
			args = append(args, "--allow", strings.Join(allow, ","))
		}
		out, err := a.priv(ctx, time.Minute, "scan-sync", args...)
		ps.mu.Lock()
		ps.syncedAt = time.Now()
		if err != nil {
			ps.failAt, ps.failKey = time.Now(), key
			ps.mu.Unlock()
			a.setPortscanStatus(&pb.PortScanStatus{Reason: scanFailReason(string(out), err)})
			a.log.Warn("port-scan detection unavailable", "reason", scanFailReason(string(out), err))
			return
		}
		ps.syncedKey, ps.failAt = key, time.Time{}
		ps.mu.Unlock()
	}
	ps.mu.Lock()
	synced := ps.syncedKey != ""
	ps.mu.Unlock()
	if !synced {
		return // waiting out the retry delay; the last failure status stands
	}

	out, err := a.priv(ctx, time.Minute, "scan-list")
	if err != nil {
		// Rules vanished (nft flush, firewall reload): reinstall next tick.
		ps.mu.Lock()
		ps.syncedKey = ""
		ps.mu.Unlock()
		a.setPortscanStatus(&pb.PortScanStatus{Supported: true, Reason: "detection rules missing; reinstalling"})
		return
	}
	var entries []ScanEntry
	if json.Unmarshal(out, &entries) != nil {
		return
	}
	found := tracker.Observe(time.Now(), entries)
	a.setPortscanStatus(&pb.PortScanStatus{Supported: true, Active: true})
	if len(found) > 0 {
		ps.mu.Lock()
		ps.pending = append(ps.pending, found...)
		ps.mu.Unlock()
		a.log.Info("port scan detected", "sources", len(found))
	}
}

// setPortscanStatus records the status; changes mark it for sending.
func (a *Agent) setPortscanStatus(st *pb.PortScanStatus) {
	a.ps.mu.Lock()
	defer a.ps.mu.Unlock()
	if a.ps.status == nil || a.ps.status.Supported != st.Supported || a.ps.status.Active != st.Active || a.ps.status.Reason != st.Reason {
		a.ps.sentAt = time.Time{}
	}
	a.ps.status = st
}

// portscanDrain returns what the next security report should carry and
// whether the report is worth sending even with nothing else in it.
func (a *Agent) portscanDrain() (status *pb.PortScanStatus, scans []*pb.PortScan, mustSend bool) {
	a.ps.mu.Lock()
	defer a.ps.mu.Unlock()
	if a.ps.status == nil {
		return nil, nil, false
	}
	if a.offline() {
		return a.ps.status, nil, false // keep pending scans until a report can be delivered
	}
	scans, a.ps.pending = a.ps.pending, nil
	stale := time.Since(a.ps.sentAt) >= portscanStatusEvery
	mustSend = len(scans) > 0 || stale || a.ps.status.Active
	if mustSend {
		a.ps.sentAt = time.Now()
	}
	return a.ps.status, scans, mustSend
}

func nftPresent() bool {
	if _, err := exec.LookPath("nft"); err == nil {
		return true
	}
	for _, p := range []string{"/usr/sbin/nft", "/sbin/nft", "/usr/bin/nft"} {
		if _, err := exec.LookPath(p); err == nil {
			return true
		}
	}
	return false
}

// scanFailReason turns a helper failure into a short user-facing reason.
func scanFailReason(out string, err error) string {
	s := strings.TrimSpace(out)
	if s == "" {
		s = err.Error()
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	switch {
	case strings.Contains(s, "nftables is not installed"):
		return "nftables is not installed on this host"
	case strings.Contains(s, "Operation not supported"), strings.Contains(s, "Could not process rule"), strings.Contains(s, "Protocol not supported"):
		return "the kernel lacks the nftables features needed (dynamic sets)"
	case strings.Contains(s, "sudo:"):
		return "the privileged helper is not available (sudo): " + s
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// validAllowEntries keeps the entries that parse as an IP or CIDR.
func validAllowEntries(in []string) []string {
	var out []string
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if v4, v6, err := validAddrList(e); err == nil && len(v4)+len(v6) == 1 {
			out = append(out, append(v4, v6...)[0])
		}
		if len(out) >= scanMaxAllow-16 {
			break
		}
	}
	return out
}

// serverAddrs resolves the Sentrel server's host so its own probes are
// never flagged. Lookups are cached for ten minutes.
type serverAddrs struct {
	host string
	mu   sync.Mutex
	ips  []string
	at   time.Time
}

func serverHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		if u2, err2 := url.Parse("//" + raw); err2 == nil {
			return u2.Hostname()
		}
		return ""
	}
	return u.Hostname()
}

func (s *serverAddrs) addrs(ctx context.Context) []string {
	if s.host == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && time.Since(s.at) < 10*time.Minute {
		return s.ips
	}
	s.at = time.Now()
	var out []string
	if ip := net.ParseIP(s.host); ip != nil {
		out = []string{ip.String()}
	} else {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if ips, err := net.DefaultResolver.LookupIPAddr(cctx, s.host); err == nil {
			for _, ip := range ips {
				out = append(out, ip.IP.String())
			}
		} else {
			out = s.ips // keep the previous answer on a failed lookup
		}
	}
	var keep []string
	for _, a := range out {
		if ip := net.ParseIP(a); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			keep = append(keep, a)
		}
	}
	s.ips = keep
	return keep
}

func (a *Agent) offline() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.out == nil
}
