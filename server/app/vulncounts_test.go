package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pb "sentrel/gen/sentrelv1"
)

func intOf(v any) int { f, _ := v.(float64); return int(f) }

// All counts come from one model: distinct (host, CVE), highest severity per
// CVE, fixable = actionable remediation.
func TestVulnCountsConsistentAcrossViews(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	enableVuln(t, e)
	h1, _ := e.enrol(admin)
	e.seedKernelHost(t, h1, "7.0.0-38-generic", []string{"7.0.0-28", "7.0.0-38"}, "7.0.0.39")
	// One CVE on three packages counts once; mixed severities take the highest.
	e.addFinding(t, h1, "CVE-A", "linux-image-7.0.0-38-generic", "7.0.0-39.1", "Critical")
	e.addFinding(t, h1, "CVE-A", "linux-modules-7.0.0-38-generic", "7.0.0-39.1", "Critical")
	e.addFinding(t, h1, "CVE-A", "linux-headers-7.0.0-38", "7.0.0-39.1", "Critical")
	e.addFinding(t, h1, "CVE-B", "bash", "5.1-2", "High")
	e.addFinding(t, h1, "CVE-M", "bash", "5.1-2", "Low")
	e.addFinding(t, h1, "CVE-M", "libc6", "", "Medium")
	e.addFinding(t, h1, "CVE-W", "bash", "5.1-9", "Medium") // fix not available yet
	e.addFinding(t, h1, "CVE-L", "bash", "", "Low")
	e.addFinding(t, h1, "CVE-N", "bash", "", "Negligible")
	e.addFinding(t, h1, "CVE-U", "bash", "", "Unknown")
	h2, _ := e.enrol(admin)
	e.addFinding(t, h2, "CVE-A", "openssl", "", "High")

	_, sum, _ := admin.do("GET", "/vulnerabilities/summary", nil)
	hosts, _ := sum["hosts"].([]any)
	byID := map[int64]map[string]any{}
	for _, x := range hosts {
		m := x.(map[string]any)
		byID[int64(m["host_id"].(float64))] = m
	}
	want := map[string]int{"critical": 1, "high": 1, "medium": 2, "low": 1, "other": 2, "total": 7, "fixable": 3}
	for k, v := range want {
		if got := intOf(byID[h1][k]); got != v {
			t.Errorf("summary host1 %s = %d, want %d (%v)", k, got, v, byID[h1])
		}
	}
	if intOf(byID[h2]["high"]) != 1 || intOf(byID[h2]["total"]) != 1 || intOf(byID[h2]["fixable"]) != 0 {
		t.Errorf("summary host2: %v", byID[h2])
	}
	tot, _ := sum["totals"].(map[string]any)
	if intOf(tot["total"]) != 8 || intOf(tot["critical"]) != 1 || intOf(tot["high"]) != 2 || intOf(tot["fixable"]) != 3 {
		t.Errorf("summary totals: %v", tot)
	}

	// Host JSON.
	_, hd, _ := admin.do("GET", "/hosts/"+itoa(h1), nil)
	vc, _ := hd["vuln_counts"].(map[string]any)
	for k, v := range want {
		if intOf(vc[k]) != v {
			t.Errorf("host vuln_counts %s = %v, want %d", k, vc[k], v)
		}
	}
	of, _ := hd["open_findings"].(map[string]any)
	if intOf(of["critical"]) != 1 || intOf(of["medium"]) != 2 || intOf(of["other"]) != 2 {
		t.Errorf("open_findings: %v", of)
	}

	// Dashboard.
	_, dash, _ := admin.do("GET", "/dashboard", nil)
	dv, _ := dash["vulnerabilities"].(map[string]any)
	if intOf(dv["critical"]) != 1 || intOf(dv["high"]) != 2 || intOf(dv["medium"]) != 2 || intOf(dv["low"]) != 1 || intOf(dv["other"]) != 2 {
		t.Errorf("dashboard severities: %v", dv)
	}
	if intOf(dash["vulnerabilities_total"]) != 8 || intOf(dash["vulnerabilities_fixable"]) != 3 {
		t.Errorf("dashboard total/fixable: %v %v", dash["vulnerabilities_total"], dash["vulnerabilities_fixable"])
	}

	// Hosts list badge: critical + high of the same model.
	code, _, raw := admin.do("GET", "/hosts", nil)
	if code != 200 {
		t.Fatal(code)
	}
	var list []Host
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, h := range list {
		if h.ID == h1 && (h.VulnCritical != 1 || h.VulnHigh != 1) {
			t.Errorf("hosts list host1: %d/%d", h.VulnCritical, h.VulnHigh)
		}
	}

	// The findings list agrees: same rows per severity, and fixable filters on remediation.
	if got := e.findings(t, admin, "?host_id="+itoa(h1)); len(got) != 7 || got["CVE-M"].Severity != "Medium" {
		t.Errorf("list: %d rows, CVE-M %q", len(got), got["CVE-M"].Severity)
	}
	if got := e.findings(t, admin, "?host_id="+itoa(h1)+"&severity=Medium"); len(got) != 2 {
		t.Errorf("severity filter: %d rows", len(got))
	}
	if got := e.findings(t, admin, "?host_id="+itoa(h1)+"&fixable=1"); len(got) != 3 {
		t.Errorf("fixable filter: %d rows", len(got))
	}
}

func TestWindowsUpdateFindings(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	ctx := context.Background()
	hostID, _ := e.enrol(admin)
	hi, _ := json.Marshal(map[string]any{"os": "windows", "arch": "amd64"})
	if _, err := e.a.db.ExecContext(ctx, `UPDATE hosts SET host_info=? WHERE id=?`, string(hi), hostID); err != nil {
		t.Fatal(err)
	}
	inv := func(ups ...*pb.Package) *pb.AgentMessage {
		return &pb.AgentMessage{Msg: &pb.AgentMessage_Inventory{Inventory: &pb.Inventory{TsMs: time.Now().UnixMilli(), PackageManager: "windows", Upgradable: ups}}}
	}
	cu := &pb.Package{Name: "2026-10 Cumulative Update for Windows 11 (KB5044284)", Type: "windows-update", Version: "-", Kb: "KB5044284", NewVersion: "KB5044284",
		Source: "Critical", Security: true, Cves: []string{"CVE-2026-0001", "CVE-2026-0002"}}
	net := &pb.Package{Name: ".NET Security Update", Type: "windows-update", Version: "-", Kb: "KB5126052", NewVersion: "KB5126052",
		Source: "Important", Security: true, Cves: []string{"CVE-2026-0002", "CVE-2026-0003"}}
	drv := &pb.Package{Name: "Some driver", Type: "windows-update", Version: "-", NewVersion: "available"}

	// Disabled: no findings.
	e.a.handleAgentMessage(ctx, hostID, inv(cu))
	var n int
	_ = e.a.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM vuln_findings`)
	if n != 0 {
		t.Fatalf("findings created while disabled: %d", n)
	}

	enableVuln(t, e)
	e.a.handleAgentMessage(ctx, hostID, inv(cu, net, drv))
	got := e.findings(t, admin, "?host_id="+itoa(hostID))
	if len(got) != 3 {
		t.Fatalf("want 3 distinct CVEs, got %d: %v", len(got), got)
	}
	f := got["CVE-2026-0001"]
	if f.Severity != "Critical" || f.FixedVersion != "KB5044284" || f.Remediation != "upgrade" || !f.Upgradable || f.OS != "windows" {
		t.Errorf("CVE-2026-0001: %+v", f)
	}
	// Two updates carry CVE-2026-0002: one finding, highest severity.
	if f := got["CVE-2026-0002"]; f.Severity != "Critical" {
		t.Errorf("CVE-2026-0002 severity %q", f.Severity)
	}
	if f := got["CVE-2026-0003"]; f.Severity != "High" || f.FixedVersion != "KB5126052" {
		t.Errorf("CVE-2026-0003: %+v", f)
	}
	_, hd, _ := admin.do("GET", "/hosts/"+itoa(hostID), nil)
	vc, _ := hd["vuln_counts"].(map[string]any)
	if intOf(vc["total"]) != 3 || intOf(vc["critical"]) != 2 || intOf(vc["high"]) != 1 || intOf(vc["fixable"]) != 3 {
		t.Errorf("windows counts: %v", vc)
	}

	// The .NET update is installed: its exclusive CVE resolves; the shared one stays via the other update.
	e.a.handleAgentMessage(ctx, hostID, inv(cu))
	got = e.findings(t, admin, "?host_id="+itoa(hostID))
	if len(got) != 2 || got["CVE-2026-0003"].VulnID != "" {
		t.Fatalf("after installing .NET update: %v", got)
	}
	// A failed update search must not resolve anything.
	e.a.handleAgentMessage(ctx, hostID, &pb.AgentMessage{Msg: &pb.AgentMessage_Inventory{Inventory: &pb.Inventory{PackageManager: "windows", Error: "Windows Update search: boom"}}})
	if got = e.findings(t, admin, "?host_id="+itoa(hostID)); len(got) != 2 {
		t.Fatalf("findings resolved by a failed search: %v", got)
	}
	// All installed: everything resolves.
	e.a.handleAgentMessage(ctx, hostID, inv())
	if got = e.findings(t, admin, "?host_id="+itoa(hostID)); len(got) != 0 {
		t.Fatalf("findings left after all updates installed: %v", got)
	}
}

func TestVulnRefreshUpdatesThenScansAll(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	enableVuln(t, e)
	calls, _ := stubVulnUpdates(e)
	h1, _ := e.enrol(admin)
	h2, _ := e.enrol(admin)
	if code, _, raw := admin.do("POST", "/vulnerabilities/refresh", nil); code != 200 {
		t.Fatalf("refresh: %d %s", code, raw)
	}
	deadline := time.Now().Add(3 * time.Second)
	var st map[string]any
	for time.Now().Before(deadline) {
		_, st, _ = admin.do("GET", "/vulnerabilities/db", nil)
		if st["refresh_phase"] == "scanning" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st["refresh_phase"] != "scanning" || st["refresh_pending"] != true || intOf(st["refresh_scan_total"]) != 2 || calls.Load() != 1 {
		t.Fatalf("status after update: %v (update calls %d)", st, calls.Load())
	}
	if code, _, _ := admin.do("POST", "/vulnerabilities/refresh", nil); code != 409 {
		t.Fatalf("a second refresh while running: %d", code)
	}
	if e.a.vuln.QueueLength() != 2 {
		t.Fatalf("queue = %d, want both hosts", e.a.vuln.QueueLength())
	}
	e.a.vuln.drain(context.Background())
	_, st, _ = admin.do("GET", "/vulnerabilities/db", nil)
	if st["refresh_phase"] != "done" || st["refresh_pending"] != false {
		t.Fatalf("status after scans: %v", st)
	}
	var scans int
	_ = e.a.db.GetContext(context.Background(), &scans, `SELECT COUNT(*) FROM vuln_scans WHERE triggered_by='refresh' AND host_id IN (?,?)`, h1, h2)
	if scans != 2 {
		t.Fatalf("scans recorded: %d", scans)
	}

	// A failed update scans nothing and reports the error.
	e.a.vuln.updateFn = func(string) error { return context.DeadlineExceeded }
	admin.do("POST", "/vulnerabilities/refresh", nil)
	for time.Now().Before(deadline.Add(3 * time.Second)) {
		_, st, _ = admin.do("GET", "/vulnerabilities/db", nil)
		if st["refresh_phase"] == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st["refresh_phase"] != "failed" || st["refresh_pending"] != false || st["refresh_error"] == "" || e.a.vuln.QueueLength() != 0 {
		t.Fatalf("failed refresh status: %v", st)
	}
}
