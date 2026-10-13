package app

import (
	"context"
	"testing"
	"time"

	pb "sentrel/gen/sentrelv1"
)

func (e *testEnv) pushSecurity(hostID int64, r *pb.SecurityReport) {
	e.a.handleAgentMessage(context.Background(), hostID, &pb.AgentMessage{Msg: &pb.AgentMessage_Security{Security: r}})
}

func (e *testEnv) portscanAlert() *Alert {
	for _, al := range e.a.alerts.Active() {
		if al.Metric == "portscan" {
			cp := al
			return &cp
		}
	}
	return nil
}

func scanReport(ips ...string) *pb.SecurityReport {
	now := time.Now()
	r := &pb.SecurityReport{WindowStartMs: now.Add(-time.Minute).UnixMilli(), WindowEndMs: now.UnixMilli(),
		PortscanStatus: &pb.PortScanStatus{Supported: true, Active: true}}
	for _, ip := range ips {
		r.Portscans = append(r.Portscans, &pb.PortScan{Ip: ip, FirstSeenMs: now.Add(-30 * time.Second).UnixMilli(), LastSeenMs: now.UnixMilli()})
	}
	return r
}

func TestPortscanDefaultRuleAndConfig(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	var enabled bool
	var warn float64
	if err := e.a.db.QueryRowContext(ctx, `SELECT enabled, warn FROM threshold_rules WHERE scope='global' AND metric='portscan'`).Scan(&enabled, &warn); err != nil || !enabled || warn != 1 {
		t.Fatalf("default portscan rule: %v enabled=%v warn=%v", err, enabled, warn)
	}
	// Per-host overrides reach the agent config; the allowlist is merged.
	d := e.a.settings.Agent()
	d.PortscanAllowlist = []string{"198.51.100.0/24"}
	if err := e.a.settings.Save(ctx, 0, keyAgent, d); err != nil {
		t.Fatal(err)
	}
	code, _, raw := admin.do("PUT", "/hosts/"+itoa(hostID), map[string]any{"agent_options": map[string]any{
		"portscan_threshold_per_min": 50, "portscan_allowlist": []string{"203.0.113.7", "10.0.0.0/8"}}})
	if code != 200 {
		t.Fatalf("update host: %d %s", code, raw)
	}
	h, _ := e.a.getHost(ctx, hostID)
	cfg := e.a.agentConfig(h)
	if !cfg.PortscanEnabled || cfg.PortscanThresholdPerMin != 50 || !hasAll(cfg.PortscanAllowlist, "198.51.100.0/24", "203.0.113.7", "10.0.0.0/8") {
		t.Fatalf("config: %+v", cfg)
	}
	if code, _, _ := admin.do("PUT", "/hosts/"+itoa(hostID), map[string]any{"agent_options": map[string]any{"portscan_allowlist": []string{"not-an-ip"}}}); code != 400 {
		t.Fatalf("bad allowlist accepted: %d", code)
	}
	if code, _, _ := admin.do("PUT", "/hosts/"+itoa(hostID), map[string]any{"agent_options": map[string]any{"portscan_threshold_per_min": 0}}); code != 400 {
		t.Fatalf("bad threshold accepted: %d", code)
	}
}

func TestPortscanIngestAlertAndMute(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	e.a.alerts.refreshHosts(ctx)
	path := "/hosts/" + itoa(hostID)

	e.pushSecurity(hostID, scanReport("203.0.113.5"))
	al := e.portscanAlert()
	if al == nil {
		t.Fatal("no portscan alert")
	}
	if al.Title != "Port scan from 203.0.113.5 on "+hostName(t, e, hostID) {
		t.Fatalf("title %q", al.Title)
	}

	_, sec, _ := admin.do("GET", path+"/security", nil)
	scans, _ := sec["portscans"].([]any)
	if len(scans) != 1 || scans[0].(map[string]any)["ip"] != "203.0.113.5" {
		t.Fatalf("portscans: %v", sec["portscans"])
	}
	st, _ := sec["portscan_status"].(map[string]any)
	if st["known"] != true || st["supported"] != true || st["active"] != true {
		t.Fatalf("status: %v", st)
	}
	if sec["notify_portscan"] != true {
		t.Fatalf("notify_portscan = %v", sec["notify_portscan"])
	}
	// Scans are not failed logins and do not feed the brute-force view.
	if fl, _ := sec["failed_logins"].([]any); len(fl) != 0 || sec["total"] != float64(0) {
		t.Fatalf("scan leaked into failed logins: %v %v", sec["failed_logins"], sec["total"])
	}

	// Muting is per host and keeps the alert.
	if code, m, _ := admin.do("PUT", path+"/notify", map[string]any{"metric": "portscan", "notify": false}); code != 200 || m["notify"] != false {
		t.Fatalf("mute: %d %v", code, m)
	}
	_, sec, _ = admin.do("GET", path+"/security", nil)
	if sec["notify_portscan"] != false {
		t.Fatalf("notify_portscan after mute = %v", sec["notify_portscan"])
	}
	e.pushSecurity(hostID, scanReport("203.0.113.5"))
	if a := e.portscanAlert(); a == nil || !a.Muted {
		t.Fatalf("expected muted alert, got %+v", a)
	}

	// Old scanners fall out of the window and the alert resolves.
	if _, err := e.a.db.ExecContext(ctx, `UPDATE security_events SET window_end=window_end-3600 WHERE kind='portscan'`); err != nil {
		t.Fatal(err)
	}
	e.pushSecurity(hostID, &pb.SecurityReport{PortscanStatus: &pb.PortScanStatus{Supported: true, Active: true}})
	if e.portscanAlert() != nil {
		t.Fatal("alert did not resolve")
	}
}

func hostName(t *testing.T, e *testEnv, id int64) string {
	t.Helper()
	h, err := e.a.getHost(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return h.Name
}

func TestPortscanUnsupportedStatus(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	e.pushSecurity(hostID, &pb.SecurityReport{PortscanStatus: &pb.PortScanStatus{Reason: "not yet supported on Windows"}})
	_, sec, _ := admin.do("GET", "/hosts/"+itoa(hostID)+"/security", nil)
	st, _ := sec["portscan_status"].(map[string]any)
	if st["known"] != true || st["supported"] != false || st["reason"] != "not yet supported on Windows" {
		t.Fatalf("status: %v", st)
	}
	if e.portscanAlert() != nil {
		t.Fatal("alert without scanners")
	}
}

func TestPortscanAutoBlock(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	e.a.alerts.refreshHosts(ctx)
	d := e.a.settings.Agent()
	d.AutoBlockWhitelist = []string{"192.0.2.0/24"}
	d.AutoBlockMinutes = 30
	if err := e.a.settings.Save(ctx, 0, keyAgent, d); err != nil {
		t.Fatal(err)
	}
	blocks := func() map[string]IPBlock {
		var rows []IPBlock
		_ = e.a.db.SelectContext(ctx, &rows, `SELECT * FROM ip_blocks WHERE host_id=?`, hostID)
		m := map[string]IPBlock{}
		for _, r := range rows {
			m[r.IP] = r
		}
		return m
	}

	// Detection only: auto-block is off by default.
	e.pushSecurity(hostID, scanReport("203.0.113.5"))
	if len(blocks()) != 0 {
		t.Fatalf("blocked without opt-in: %v", blocks())
	}
	// Opt in; whitelisted sources are never blocked, the rest once.
	if code, _, raw := admin.do("PUT", "/hosts/"+itoa(hostID), map[string]any{"allow_ip_block": true, "auto_block": true}); code != 200 {
		t.Fatalf("host update: %d %s", code, raw)
	}
	e.pushSecurity(hostID, scanReport("203.0.113.5", "192.0.2.9"))
	e.pushSecurity(hostID, scanReport("203.0.113.5"))
	got := blocks()
	b, ok := got["203.0.113.5"]
	if len(got) != 1 || !ok || b.Reason != "auto-block: port scan" || b.Status != "pending" {
		t.Fatalf("blocks: %+v", got)
	}
	if min := time.Until(time.Unix(b.ExpiresAt, 0)); min < 29*time.Minute || min > 31*time.Minute {
		t.Fatalf("block duration %v", min)
	}
}

func hasAll(list []string, want ...string) bool {
	have := map[string]bool{}
	for _, e := range list {
		have[e] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}
