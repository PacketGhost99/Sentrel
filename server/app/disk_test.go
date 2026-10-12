package app

import (
	"context"
	"testing"
	"time"

	pb "heartbeat/gen/heartbeatv1"
)

func TestValidateCleanupTasks(t *testing.T) {
	got, err := validateCleanupTasks("linux", "journal, pkg_cache,journal")
	if err != nil || len(got) != 2 || got[0] != "journal" || got[1] != "pkg_cache" {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "journal;rm -rf /", "../etc", "recycle", "docker,$(id)"} {
		if _, err := validateCleanupTasks("linux", bad); err == nil {
			t.Errorf("linux accepted %q", bad)
		}
	}
	if _, err := validateCleanupTasks("windows", "recycle,component_store"); err != nil {
		t.Errorf("windows tasks rejected: %v", err)
	}
	if _, err := validateCleanupTasks("windows", "journal"); err == nil {
		t.Error("windows accepted a linux task")
	}
}

func TestNormalizeDiskUsageParams(t *testing.T) {
	p := map[string]string{}
	if err := normalizeDiskUsageParams("linux", p); err != nil || p["path"] != "/" || p["depth"] != "3" || p["top"] != "30" {
		t.Fatalf("defaults: %v %v", p, err)
	}
	p = map[string]string{"mount": "/var/lib//docker/", "depth": "2", "top": "10"}
	if err := normalizeDiskUsageParams("linux", p); err != nil || p["path"] != "/var/lib/docker" {
		t.Fatalf("clean: %v %v", p, err)
	}
	for _, bad := range []map[string]string{{"path": "relative"}, {"path": "/", "depth": "0"}, {"path": "/", "depth": "99"}, {"path": "/", "top": "x"}} {
		if err := normalizeDiskUsageParams("linux", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	p = map[string]string{"path": "c:/"}
	if err := normalizeDiskUsageParams("windows", p); err != nil || p["path"] != "C:" {
		t.Fatalf("windows: %v %v", p, err)
	}
}

func TestDiskCleanupGatingAndUsageReport(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	hp := "/hosts/" + itoa(hostID)

	// Cleanup is opt-in per host, even for a dry run.
	cleanup := map[string]any{"host_id": hostID, "type": "disk_cleanup", "confirm": true, "params": map[string]string{"tasks": "journal"}}
	if code, _, _ := admin.do("POST", "/jobs", cleanup); code != 403 {
		t.Fatalf("cleanup without allow_cleanup: %d", code)
	}
	if code, m, _ := admin.do("PUT", hp, map[string]any{"allow_cleanup": true}); code != 200 {
		t.Fatalf("enable: %d %v", code, m)
	}
	if code, m, _ := admin.do("GET", hp, nil); code != 200 || m["allow_cleanup"] != true {
		t.Fatalf("detail allow_cleanup: %d %v", code, m["allow_cleanup"])
	}
	if code, m, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "disk_cleanup", "params": map[string]string{"tasks": "journal"}}); code != 409 || m["code"] != "confirmation_required" {
		t.Fatalf("unconfirmed cleanup: %d %v", code, m)
	}
	if code, _, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "disk_cleanup", "confirm": true, "params": map[string]string{"tasks": "journal;id"}}); code != 400 {
		t.Fatalf("bad task accepted: %d", code)
	}
	if code, _, raw := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "disk_cleanup", "dry_run": true, "params": map[string]string{"tasks": "journal,tmp"}}); code != 201 {
		t.Fatalf("dry run: %d %s", code, raw)
	}
	if code, _, raw := admin.do("POST", "/jobs", cleanup); code != 201 {
		t.Fatalf("confirmed cleanup: %d %s", code, raw)
	}

	// Task catalog.
	if code, _, raw := admin.do("GET", hp+"/disk-cleanup/tasks", nil); code != 200 || len(raw) < 20 {
		t.Fatalf("tasks: %d %s", code, raw)
	}

	// No report yet.
	if code, m, _ := admin.do("GET", hp+"/disk-usage?mount=/", nil); code != 200 || m["status"] != "none" {
		t.Fatalf("empty report: %d %v", code, m)
	}
	code, m, raw := admin.do("POST", hp+"/disk-usage", map[string]any{"mount": "/"})
	if code != 200 && code != 201 {
		t.Fatalf("queue: %d %s", code, raw)
	}
	jobID, _ := m["job_id"].(string)
	if jobID == "" {
		t.Fatalf("no job_id: %s", raw)
	}
	if _, m, _ := admin.do("GET", hp+"/disk-usage?mount=/", nil); m["status"] != "running" || m["job_id"] != jobID {
		t.Fatalf("running report: %v", m)
	}
	e.a.jobs.result(ctx, hostID, &pb.JobResult{JobId: jobID, Status: "succeeded", ResultJson: []byte(
		`{"path":"/","total_bytes":1000,"used_bytes":600,"entries":[{"path":"/var","bytes":400,"depth":1}],"largest_files":[{"path":"/var/big","bytes":300}]}`)})
	_, m, raw = admin.do("GET", hp+"/disk-usage?mount=/", nil)
	ents, _ := m["entries"].([]any)
	files, _ := m["largest_files"].([]any)
	if m["status"] != "done" || m["total_bytes"] != float64(1000) || m["used_bytes"] != float64(600) || len(ents) != 1 || len(files) != 1 {
		t.Fatalf("done report: %s", raw)
	}

	// A failed job is reported as failed.
	_, m, _ = admin.do("POST", hp+"/disk-usage", map[string]any{"mount": "/data"})
	e.a.jobs.result(ctx, hostID, &pb.JobResult{JobId: m["job_id"].(string), Status: "failed", Error: "boom"})
	if _, m, _ := admin.do("GET", hp+"/disk-usage?mount=/data", nil); m["status"] != "failed" || m["error"] != "boom" {
		t.Fatalf("failed report: %v", m)
	}
}

func TestAutoDiskAnalysisRateLimited(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	conn := &agentConn{hostID: hostID, send: make(chan *pb.ServerMessage, 8), cancel: func() {}}
	e.a.hub.mu.Lock()
	e.a.hub.conns[hostID] = conn
	e.a.hub.mu.Unlock()
	al := Alert{Metric: "disk", Source: "threshold", HostID: &hostID, Labels: "/data"}
	count := func() int {
		var n int
		_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM disk_reports WHERE host_id=? AND mount='/data'`, hostID)
		return n
	}
	e.a.maybeAutoDiskAnalysis(al)
	e.a.maybeAutoDiskAnalysis(al)
	deadline := time.Now().Add(3 * time.Second)
	for count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := count(); n != 1 {
		t.Fatalf("expected exactly one auto analysis, got %d", n)
	}
	// Other metrics do not trigger it.
	cpu := Alert{Metric: "cpu", Source: "threshold", HostID: &hostID, Labels: "/other"}
	e.a.maybeAutoDiskAnalysis(cpu)
	time.Sleep(200 * time.Millisecond)
	var n int
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM disk_reports WHERE mount='/other'`)
	if n != 0 {
		t.Fatal("cpu alert queued a disk analysis")
	}
}

func TestPruneDiskReports(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	for i := 0; i < 8; i++ {
		_, _ = e.a.db.Exec(`INSERT INTO disk_reports (host_id, mount, job_id, status, created_at) VALUES (?,?,?,?,?)`, hostID, "/", itoa(int64(i)), "done", 1000+i)
	}
	e.a.pruneDiskReports(context.Background())
	var n int
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM disk_reports WHERE host_id=?`, hostID)
	if n != 5 {
		t.Fatalf("kept %d reports, want 5", n)
	}
}
