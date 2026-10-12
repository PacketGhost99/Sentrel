package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "sentrel/gen/sentrelv1"
)

func (e *testEnv) pushCPU(hostID int64, v float64) {
	e.a.handleAgentMessage(context.Background(), hostID, &pb.AgentMessage{Msg: &pb.AgentMessage_Metrics{Metrics: &pb.MetricBatch{Samples: []*pb.Sample{
		{Name: "cpu_usage_percent", Value: v, TsMs: time.Now().UnixMilli()},
	}}}})
}

func (e *testEnv) cpuAlert() *Alert {
	for _, al := range e.a.alerts.Active() {
		if al.Metric == "cpu" {
			cp := al
			return &cp
		}
	}
	return nil
}

func (e *testEnv) queued() int {
	e.a.notifier.mu.Lock()
	defer e.a.notifier.mu.Unlock()
	n := 0
	for _, q := range e.a.notifier.queue {
		n += len(q)
	}
	return n
}

func TestMutedRuleFiresButDoesNotNotify(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer hook.Close()
	if code, _, raw := admin.do("POST", "/notification-channels", map[string]any{"name": "hook", "type": "webhook", "config": map[string]any{"url": hook.URL}}); code != 201 {
		t.Fatalf("channel: %d %s", code, raw)
	}
	// Keep queued messages in the queue so the test can inspect them.
	admin.do("PUT", "/settings/alerting", map[string]any{"group_wait_seconds": 3600, "flap_threshold": 50, "flap_window_minutes": 30, "resolve_count": 1, "default_repeat_minutes": 240})
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	e.a.alerts.refreshHosts(ctx)

	if code, m, raw := admin.do("PUT", "/hosts/"+itoa(hostID)+"/notify", map[string]any{"metric": "cpu", "notify": false}); code != 200 || m["metric"] != "cpu" || m["notify"] != false {
		t.Fatalf("notify: %d %s", code, raw)
	}
	if code, _, _ := admin.do("PUT", "/hosts/"+itoa(hostID)+"/notify", map[string]any{"metric": "nope", "notify": false}); code != 400 {
		t.Fatalf("unknown metric accepted: %d", code)
	}
	// The override keeps the inherited thresholds.
	var r ThresholdRule
	if err := e.a.db.GetContext(ctx, &r, `SELECT * FROM threshold_rules WHERE scope='host' AND scope_id=? AND metric='cpu'`, hostID); err != nil {
		t.Fatal(err)
	}
	if r.Notify || !r.Enabled || r.Warn == nil || *r.Warn != 85 || r.Crit == nil || *r.Crit != 95 || r.ForCount != 4 {
		t.Fatalf("override did not copy the effective rule: %+v", r)
	}

	for i := 0; i < 4; i++ {
		e.pushCPU(hostID, 99)
	}
	al := e.cpuAlert()
	if al == nil || al.Status != "firing" || !al.Muted {
		t.Fatalf("expected a firing, muted alert: %+v", al)
	}
	if n := e.queued(); n != 0 {
		t.Fatalf("muted alert enqueued %d notifications", n)
	}
	var silenced int
	_ = e.a.db.GetContext(ctx, &silenced, `SELECT COUNT(*) FROM alert_events WHERE alert_id=? AND event='silenced' AND message='notifications disabled for this rule'`, al.ID)
	if silenced == 0 {
		t.Fatal("no silenced event recorded")
	}
	// The API exposes the flag.
	if code, m, _ := admin.do("GET", "/alerts/"+itoa(al.ID), nil); code != 200 {
		t.Fatalf("get alert: %d", code)
	} else if a, _ := m["alert"].(map[string]any); a != nil && a["muted"] != true {
		t.Fatalf("alert json muted = %v", a["muted"])
	}
	// Resolving a muted alert does not notify either.
	e.pushCPU(hostID, 10)
	if e.cpuAlert() != nil {
		t.Fatal("alert did not resolve")
	}
	if n := e.queued(); n != 0 {
		t.Fatalf("resolution of a muted alert enqueued %d notifications", n)
	}

	// Unmuting restores notifications.
	if code, _, raw := admin.do("PUT", "/hosts/"+itoa(hostID)+"/notify", map[string]any{"metric": "cpu", "notify": true}); code != 200 {
		t.Fatalf("unmute: %d %s", code, raw)
	}
	for i := 0; i < 4; i++ {
		e.pushCPU(hostID, 99)
	}
	if al = e.cpuAlert(); al == nil || al.Muted {
		t.Fatalf("expected unmuted firing alert: %+v", al)
	}
	if n := e.queued(); n != 1 {
		t.Fatalf("expected one queued notification, got %d", n)
	}
}

func TestHostNotifyEndpointAndSecurity(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	path := "/hosts/" + itoa(hostID)

	_, sec, _ := admin.do("GET", path+"/security", nil)
	if sec["notify_failed_logins"] != true {
		t.Fatalf("default notify_failed_logins = %v", sec["notify_failed_logins"])
	}
	if code, m, _ := admin.do("PUT", path+"/notify", map[string]any{"metric": "bruteforce", "notify": false}); code != 200 || m["notify"] != false {
		t.Fatalf("notify: %d %v", code, m)
	}
	_, sec, _ = admin.do("GET", path+"/security", nil)
	if sec["notify_failed_logins"] != false {
		t.Fatalf("notify_failed_logins after mute = %v", sec["notify_failed_logins"])
	}
	// Setting it again updates the same rule.
	admin.do("PUT", path+"/notify", map[string]any{"metric": "bruteforce", "notify": true})
	var n int
	_ = e.a.db.GetContext(context.Background(), &n, `SELECT COUNT(*) FROM threshold_rules WHERE scope='host' AND scope_id=? AND metric='bruteforce'`, hostID)
	if n != 1 {
		t.Fatalf("expected one host rule, got %d", n)
	}
	_, sec, _ = admin.do("GET", path+"/security", nil)
	if sec["notify_failed_logins"] != true {
		t.Fatalf("notify_failed_logins after unmute = %v", sec["notify_failed_logins"])
	}

	// Viewers may not change it.
	if code, _, _ := admin.do("POST", "/users", map[string]any{"username": "viewer1", "password": testPassword, "role": "viewer"}); code == 201 || code == 200 {
		v := e.login("viewer1", testPassword)
		if code, _, _ := v.do("PUT", path+"/notify", map[string]any{"metric": "cpu", "notify": false}); code != 403 {
			t.Fatalf("viewer changed notify: %d", code)
		}
	}
	// Unknown host.
	if code, _, _ := admin.do("PUT", "/hosts/99999/notify", map[string]any{"metric": "cpu", "notify": false}); code != 404 {
		t.Fatalf("unknown host: %d", code)
	}
}

func TestThresholdNotifyDefaultsToTrue(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	code, m, raw := admin.do("POST", "/thresholds", map[string]any{"scope": "global", "metric": "swap", "warn": 50, "crit": 70, "for_count": 2})
	if code != 200 && code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	if m["notify"] != true {
		t.Fatalf("notify default = %v", m["notify"])
	}
	_, m, _ = admin.do("POST", "/thresholds", map[string]any{"scope": "global", "metric": "swap", "warn": 50, "crit": 70, "for_count": 2, "notify": false})
	if m["notify"] != false {
		t.Fatalf("notify = %v", m["notify"])
	}
}

func TestBeatEndpoints(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	today := time.Now().UTC().Format("2006-01-02")
	_, err := e.a.db.Exec(`INSERT INTO uptime_daily (tenant_id, item_type, item_id, day, up_seconds, down_seconds) VALUES (0,'host',?,?,900,100)`, hostID, today)
	if err != nil {
		t.Fatal(err)
	}

	code, m, raw := admin.do("GET", "/hosts/beats?days=7", nil)
	if code != 200 {
		t.Fatalf("host beats: %d %s", code, raw)
	}
	beats, _ := m[itoa(hostID)].([]any)
	if len(beats) != 7 {
		t.Fatalf("expected 7 days, got %d (%s)", len(beats), raw)
	}
	last := beats[6].(map[string]any)
	if last["day"] != today || last["uptime"] != 90.0 {
		t.Fatalf("today's beat: %v", last)
	}
	if first := beats[0].(map[string]any); first["uptime"] != nil {
		t.Fatalf("day without data should be null: %v", first)
	}
	_, m, _ = admin.do("GET", "/hosts/beats?days=500", nil)
	if b, _ := m[itoa(hostID)].([]any); len(b) != 90 {
		t.Fatalf("days is capped at 90, got %d", len(b))
	}

	// Checks: results come from the time-series store.
	code, c, raw := admin.do("POST", "/checks", map[string]any{"name": "web", "type": "tcp", "target": "127.0.0.1:1", "interval_seconds": 30})
	if code != 201 && code != 200 {
		t.Fatalf("create check: %d %s", code, raw)
	}
	id := int64(c["id"].(float64))
	code, m, raw = admin.do("GET", "/checks/beats?limit=10", nil)
	if code != 200 {
		t.Fatalf("check beats: %d %s", code, raw)
	}
	if _, ok := m[itoa(id)].([]any); !ok {
		t.Fatalf("check %d missing from beats: %s", id, raw)
	}
	if code, _, _ := admin.do("GET", "/checks/beats?limit=1000", nil); code != 200 {
		t.Fatalf("limit cap: %d", code)
	}
	// Unauthenticated callers get nothing.
	if code, _, _ := e.client().do("GET", "/checks/beats", nil); code != 401 {
		t.Fatalf("unauthenticated beats: %d", code)
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct {
		typ, a, b string
		ge        bool
	}{
		{"deb", "1.2.3-1ubuntu2", "1.2.3-1ubuntu1", true},
		{"deb", "1.2.3-1ubuntu1", "1.2.3-1ubuntu1", true},
		{"deb", "1.2.3-1", "1.2.3-2", false},
		{"deb", "1:1.0-1", "2.0-1", true},
		{"deb", "1.0~rc1-1", "1.0-1", false},
		{"deb", "3.0.2-0ubuntu1.18", "3.0.2-0ubuntu1.9", true},
		{"rpm", "1.2.10-1.el9", "1.2.9-1.el9", true},
		{"rpm", "1.2.9-1.el9", "1.2.9-2.el9", false},
		{"rpm", "1:1.0-1", "2.0-1", true},
		{"rpm", "1.0~rc1", "1.0", false},
		{"apk", "3.1.4-r2", "3.1.4-r1", true},
	}
	for _, c := range cases {
		ge, known := versionAtLeast(c.typ, c.a, c.b)
		if !known || ge != c.ge {
			t.Errorf("%s %q >= %q: got %v (known %v), want %v", c.typ, c.a, c.b, ge, known, c.ge)
		}
	}
	if _, known := versionAtLeast("msi", "1", "2"); known {
		t.Error("unknown package types must report known=false")
	}
}

func TestFindingsUpgradeInfo(t *testing.T) {
	e := newTestEnv(t)
	stubVulnUpdates(e)
	admin := e.setupAdmin()
	if err := e.a.settings.Save(context.Background(), 0, keyVuln, VulnSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	now := time.Now().Unix()
	_, err := e.a.db.ExecContext(ctx, `INSERT INTO packages (tenant_id, host_id, name, version, arch, type, source, new_version, security) VALUES (0,?,?,?,?,?,?,?,1)`,
		hostID, "openssl", "3.0.2-0ubuntu1.9", "amd64", "deb", "openssl", "3.0.2-0ubuntu1.18")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ vuln, pkg, fixed string }{
		{"CVE-1", "openssl", "3.0.2-0ubuntu1.15"}, // fixed by the pending update
		{"CVE-2", "openssl", "3.0.2-0ubuntu1.20"}, // pending update is too old
		{"CVE-3", "bash", "5.1-2"},                // no update pending
	} {
		_, err := e.a.db.ExecContext(ctx, `INSERT INTO vuln_findings (tenant_id, host_id, vuln_id, package, version, fixed_version, severity, status, first_seen_at, last_seen_at)
			VALUES (0,?,?,?,?,?,'High','open',?,?)`, hostID, f.vuln, f.pkg, "1", f.fixed, now, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	code, _, raw := admin.do("GET", "/vulnerabilities", nil)
	if code != 200 {
		t.Fatalf("list: %d %s", code, raw)
	}
	var list []VulnFinding
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]VulnFinding{}
	for _, f := range list {
		got[f.VulnID] = f
	}
	if f := got["CVE-1"]; !f.Upgradable || f.AvailableVersion != "3.0.2-0ubuntu1.18" || f.OS != "linux" {
		t.Errorf("CVE-1: %+v", f)
	}
	if f := got["CVE-2"]; f.Upgradable || f.AvailableVersion != "3.0.2-0ubuntu1.18" {
		t.Errorf("CVE-2: %+v", f)
	}
	if f := got["CVE-3"]; f.Upgradable || f.AvailableVersion != "" || f.OS != "linux" {
		t.Errorf("CVE-3: %+v", f)
	}
}
