package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func stubVulnUpdates(e *testEnv) (calls *atomic.Int32, called chan struct{}) {
	calls = &atomic.Int32{}
	called = make(chan struct{}, 8)
	e.a.vuln.updateFn = func(string) error { calls.Add(1); called <- struct{}{}; return nil }
	e.a.vuln.loadFn = func() error { calls.Add(1); called <- struct{}{}; return nil }
	return
}

func TestVulnDisabledByDefaultBlocksEverything(t *testing.T) {
	e := newTestEnv(t)
	calls, _ := stubVulnUpdates(e)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)

	if e.a.vuln.Enabled() {
		t.Fatal("new installs must default to disabled")
	}
	for _, p := range []string{"/vulnerabilities/scan-all", "/hosts/" + itoa(hostID) + "/scan", "/vulnerabilities/db/sync"} {
		code, _, raw := admin.do("POST", p, nil)
		if code != 409 || !strings.Contains(string(raw), "Vulnerability scanning is disabled; enable it in Settings") {
			t.Fatalf("%s: want 409 disabled, got %d %s", p, code, raw)
		}
	}
	// Inventory, schedule and restart triggers queue nothing.
	if e.a.vuln.enqueue(hostID, "inventory") || e.a.vuln.QueueLength() != 0 {
		t.Fatal("enqueue must be a no-op while disabled")
	}
	if err := e.a.vuln.SyncNow(); err == nil {
		t.Fatal("SyncNow must refuse while disabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.a.vuln.loop(ctx)
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("DB opened or downloaded while disabled: %d calls", calls.Load())
	}
	code, _, raw := admin.do("GET", "/vulnerabilities", nil)
	if code != 200 || string(raw) != "[]\n" && string(raw) != "[]" {
		t.Fatalf("findings: %d %s", code, raw)
	}
	_, st, _ := admin.do("GET", "/vulnerabilities/summary", nil)
	if st["enabled"] != false {
		t.Fatalf("summary enabled flag: %v", st["enabled"])
	}
}

func TestVulnEnableTriggersDBUpdate(t *testing.T) {
	e := newTestEnv(t)
	_, called := stubVulnUpdates(e)
	admin := e.setupAdmin()
	if code, _, raw := admin.do("PUT", "/settings/vuln", map[string]any{"enabled": true}); code != 200 {
		t.Fatalf("enable: %d %s", code, raw)
	}
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("enabling did not request a DB update")
	}
	if !e.a.vuln.Enabled() {
		t.Fatal("setting not saved")
	}
}

func TestVulnDisableDeletesDBOnlyWhenAsked(t *testing.T) {
	e := newTestEnv(t)
	stubVulnUpdates(e)
	admin := e.setupAdmin()
	dir := filepath.Join(e.a.cfg.DataDir, "vulndb")
	_ = os.MkdirAll(dir, 0o750)
	f := filepath.Join(dir, "vulnerability.db")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	admin.do("PUT", "/settings/vuln", map[string]any{"enabled": true})
	if code, _, raw := admin.do("PUT", "/settings/vuln", map[string]any{"enabled": false}); code != 200 {
		t.Fatalf("disable: %d %s", code, raw)
	}
	if _, err := os.Stat(f); err != nil {
		t.Fatal("DB deleted without delete_db")
	}
	admin.do("PUT", "/settings/vuln", map[string]any{"enabled": false, "delete_db": true})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("delete_db did not remove the DB")
	}
}

func newAppIn(t *testing.T, dir string) (*App, func()) {
	t.Helper()
	a, err := New(Config{DataDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	closeA := func() { once.Do(a.Close) }
	t.Cleanup(closeA)
	return a, closeA
}

func TestVulnMigrationKeepsExistingInstallsEnabled(t *testing.T) {
	// Fresh install: disabled.
	if a0, _ := newAppIn(t, t.TempDir()); a0.vuln.Enabled() {
		t.Fatal("fresh install should be disabled")
	}
	// An existing DB directory keeps scanning on.
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "vulndb"), 0o750)
	_ = os.WriteFile(filepath.Join(dir, "vulndb", "vulnerability.db"), []byte("x"), 0o600)
	if a1, _ := newAppIn(t, dir); !a1.vuln.Enabled() {
		t.Fatal("existing vulndb must migrate to enabled")
	}
	// Scan history alone does too, and an explicit setting is never overridden.
	dir2 := t.TempDir()
	a, closeA := newAppIn(t, dir2)
	if _, err := a.db.Exec(`INSERT INTO hosts (kind, name, created_at, updated_at) VALUES ('agent','h',1,1)`); err != nil {
		t.Fatal(err)
	}
	_, err := a.db.Exec(`INSERT INTO vuln_scans (tenant_id, host_id, status, triggered_by, started_at) VALUES (0,(SELECT MAX(id) FROM hosts),'completed','manual',1)`)
	if err != nil {
		t.Fatal(err)
	}
	closeA()
	if a2, _ := newAppIn(t, dir2); !a2.vuln.Enabled() {
		t.Fatal("scan history must migrate to enabled")
	}
	dir3 := t.TempDir()
	a3, close3 := newAppIn(t, dir3)
	if err := a3.settings.Save(context.Background(), 0, keyVuln, VulnSettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	close3()
	_ = os.MkdirAll(filepath.Join(dir3, "vulndb"), 0o750)
	_ = os.WriteFile(filepath.Join(dir3, "vulndb", "x"), []byte("x"), 0o600)
	if a4, _ := newAppIn(t, dir3); a4.vuln.Enabled() {
		t.Fatal("explicit disabled setting must be respected")
	}
}
