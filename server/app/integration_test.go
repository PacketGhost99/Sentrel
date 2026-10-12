package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "heartbeat/gen/heartbeatv1"
	"heartbeat/server/metrics"
)

// testEnv is a running App with an HTTP test server.
type testEnv struct {
	t   *testing.T
	a   *App
	srv *httptest.Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := New(Config{DataDir: t.TempDir(), Log: log})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.alerts.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.ensureSetupToken()
	a.srv = newHTTPServer(a)
	ts := httptest.NewServer(a.srv.handler)
	t.Cleanup(func() {
		ts.Close()
		a.Close()
	})
	return &testEnv{t: t, a: a, srv: ts}
}

// client is a browser-like client with a cookie jar and CSRF token.
type client struct {
	env  *testEnv
	http *http.Client
	csrf string
	key  string
}

func (e *testEnv) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{env: e, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any, []byte) {
	c.env.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.env.srv.URL+"/api/v1"+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.env.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, raw
}

const testPassword = "correct-horse-battery-1"

func (e *testEnv) setupAdmin() *client {
	c := e.client()
	code, m, raw := c.do("POST", "/setup", map[string]any{"token": e.a.setupToken, "username": "admin", "password": testPassword, "server_url": "http://monitor.test"})
	if code != 200 {
		e.t.Fatalf("setup: %d %s", code, raw)
	}
	c.csrf = m["csrf_token"].(string)
	return c
}

func (e *testEnv) login(user, pw string) *client {
	c := e.client()
	code, m, raw := c.do("POST", "/auth/login", map[string]any{"username": user, "password": pw})
	if code != 200 {
		e.t.Fatalf("login %s: %d %s", user, code, raw)
	}
	c.csrf, _ = m["csrf_token"].(string)
	return c
}

func TestSetupAndAuth(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	if code, _, _ := c.do("GET", "/hosts", nil); code != 401 {
		t.Fatalf("unauthenticated hosts: %d", code)
	}
	if code, _, _ := c.do("POST", "/setup", map[string]any{"token": "WRONGTOKENWRONGTOKEN", "username": "admin", "password": testPassword}); code != 403 {
		t.Fatalf("bad setup token accepted: %d", code)
	}
	admin := e.setupAdmin()
	if code, _, _ := c.do("POST", "/setup", map[string]any{"token": e.a.setupToken, "username": "x", "password": testPassword}); code != 409 {
		t.Fatalf("second setup allowed: %d", code)
	}
	if code, m, _ := admin.do("GET", "/auth/me", nil); code != 200 || m["user"].(map[string]any)["role"] != "admin" {
		t.Fatalf("me: %d %v", code, m)
	}
	// CSRF: a cookie-authenticated write without the header is refused.
	saved := admin.csrf
	admin.csrf = ""
	if code, _, _ := admin.do("POST", "/groups", map[string]any{"name": "x"}); code != 403 {
		t.Fatalf("missing CSRF accepted: %d", code)
	}
	admin.csrf = saved
	if code, _, _ := admin.do("POST", "/groups", map[string]any{"name": "web"}); code != 201 {
		t.Fatalf("create group: %d", code)
	}
	// Wrong password, then lockout after repeated failures.
	bad := e.client()
	for i := 0; i < 8; i++ {
		if code, _, _ := bad.do("POST", "/auth/login", map[string]any{"username": "admin", "password": "nope"}); code != 401 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code, _, _ := bad.do("POST", "/auth/login", map[string]any{"username": "admin", "password": testPassword}); code != 429 {
		t.Fatalf("expected lockout, got %d", code)
	}
}

func TestRBACAndTenantIsolation(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	_, ten, _ := admin.do("POST", "/tenants", map[string]any{"name": "Acme Ltd"})
	tid := ten["id"].(float64)
	_, other, _ := admin.do("POST", "/tenants", map[string]any{"name": "Globex"})
	oid := other["id"].(float64)
	for _, u := range []map[string]any{
		{"username": "viewer", "password": testPassword, "role": "viewer", "tenant_id": 0},
		{"username": "acme-admin", "password": testPassword, "role": "tenant_admin", "tenant_id": tid},
	} {
		if code, _, raw := admin.do("POST", "/users", u); code != 201 {
			t.Fatalf("create user: %d %s", code, raw)
		}
	}
	// Invalid role for tenant.
	if code, _, _ := admin.do("POST", "/users", map[string]any{"username": "bad", "password": testPassword, "role": "admin", "tenant_id": tid}); code != 400 {
		t.Fatalf("admin role in tenant allowed: %d", code)
	}
	// Hosts in each tenant.
	mk := func(tenant float64, name string) float64 {
		code, m, raw := admin.do("POST", "/hosts/target", map[string]any{"name": name, "address": "192.0.2.1", "tenant_id": tenant})
		if code != 201 {
			t.Fatalf("target: %d %s", code, raw)
		}
		return m["id"].(float64)
	}
	acmeHost := mk(tid, "acme-web")
	globexHost := mk(oid, "globex-db")

	viewer := e.login("viewer", testPassword)
	if code, _, _ := viewer.do("POST", "/groups", map[string]any{"name": "nope"}); code != 403 {
		t.Fatalf("viewer could write: %d", code)
	}
	if code, _, raw := viewer.do("GET", "/hosts", nil); code != 200 || !strings.Contains(string(raw), "globex-db") {
		t.Fatalf("provider viewer should see all hosts: %s", raw)
	}

	acme := e.login("acme-admin", testPassword)
	code, _, raw := acme.do("GET", "/hosts", nil)
	if code != 200 || !strings.Contains(string(raw), "acme-web") || strings.Contains(string(raw), "globex-db") {
		t.Fatalf("tenant isolation broken: %s", raw)
	}
	if code, _, _ := acme.do("GET", "/hosts/"+ftoa(globexHost), nil); code != 404 {
		t.Fatalf("tenant admin read other tenant's host: %d", code)
	}
	if code, _, _ := acme.do("DELETE", "/hosts/"+ftoa(acmeHost), nil); code != 403 {
		t.Fatalf("tenant admin deleted host (hosts perm is provider-only): %d", code)
	}
	if code, _, _ := acme.do("PUT", "/settings/general", map[string]any{"server_url": "http://evil"}); code != 403 {
		t.Fatalf("tenant admin changed settings: %d", code)
	}
	// Tenant admin creates a check: it is forced into their tenant.
	code, chk, raw := acme.do("POST", "/checks", map[string]any{"name": "web", "type": "tcp", "target": "192.0.2.1:443", "tenant_id": oid})
	if code != 201 || chk["tenant_id"].(float64) != tid {
		t.Fatalf("check tenant not forced: %d %s", code, raw)
	}
	// PromQL tenant injection: a tenant cannot read other tenants' series.
	_ = e.a.ts.Append(int64(globexHost), int64(oid), []metrics.Sample{{Name: "secret_metric", Value: 42}})
	_ = e.a.ts.Append(int64(acmeHost), int64(tid), []metrics.Sample{{Name: "secret_metric", Value: 7}})
	code, res, raw := acme.do("GET", `/metrics/query?query=hb_secret_metric&start=600s`, nil)
	if code != 200 {
		t.Fatalf("query: %d %s", code, raw)
	}
	if series := res["series"].([]any); len(series) != 1 || !strings.Contains(string(raw), `"7"`) && !strings.Contains(string(raw), ",7]") {
		t.Fatalf("tenant saw foreign series: %s", raw)
	}
}

func ftoa(f float64) string { return itoa(int64(f)) }

func TestAPIKeyScopes(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	_, m, raw := admin.do("POST", "/api-keys", map[string]any{"name": "ro", "scopes": []string{"read"}})
	key, ok := m["key"].(string)
	if !ok {
		t.Fatalf("no key: %s", raw)
	}
	k := e.client()
	k.key = key
	if code, _, _ := k.do("GET", "/hosts", nil); code != 200 {
		t.Fatalf("read with key: %d", code)
	}
	if code, _, _ := k.do("POST", "/groups", map[string]any{"name": "x"}); code != 403 {
		t.Fatalf("read-only key could write: %d", code)
	}
	k.key = key + "x"
	if code, _, _ := k.do("GET", "/hosts", nil); code != 401 {
		t.Fatalf("tampered key accepted: %d", code)
	}
}

// --- agent registration & ingest ---

func csrPEM(t *testing.T) []byte {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "test"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func (e *testEnv) enrol(admin *client) (int64, *pb.RegisterResponse) {
	e.t.Helper()
	code, tok, raw := admin.do("POST", "/enrollment-tokens", map[string]any{"ttl_minutes": 15})
	if code != 200 {
		e.t.Fatalf("token: %d %s", code, raw)
	}
	resp, err := e.a.hub.Register(context.Background(), &pb.RegisterRequest{
		Token: strings.ToLower(tok["token"].(string)), CsrPem: csrPEM(e.t), SchemaVersion: 1,
		Host: &pb.HostInfo{Hostname: "agent-1", Os: "linux", Arch: "amd64", OsId: "ubuntu", OsVersionId: "24.04", CpuCores: 2},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	h, err := e.a.getHostByAgentID(context.Background(), resp.AgentId)
	if err != nil {
		e.t.Fatal(err)
	}
	// A token works exactly once.
	if _, err := e.a.hub.Register(context.Background(), &pb.RegisterRequest{Token: tok["token"].(string), CsrPem: csrPEM(e.t), SchemaVersion: 1, Host: &pb.HostInfo{}}); err == nil {
		e.t.Fatal("token reused")
	}
	return h.ID, resp
}

func TestRegisterIngestAndAlerting(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, resp := e.enrol(admin)
	cert, err := x509.ParseCertificate(pemBytes(t, resp.CertPem))
	if err != nil || cert.Subject.CommonName != resp.AgentId {
		t.Fatalf("cert: %v", err)
	}
	ctx := context.Background()
	e.a.alerts.refreshHosts(ctx)

	cpu := func(v float64) {
		e.a.handleAgentMessage(ctx, hostID, &pb.AgentMessage{Msg: &pb.AgentMessage_Metrics{Metrics: &pb.MetricBatch{Samples: []*pb.Sample{
			{Name: "cpu_usage_percent", Value: v, TsMs: time.Now().UnixMilli()},
			{Name: "disk_used_percent", Value: 50, Labels: map[string]string{"mount": "/"}, TsMs: time.Now().UnixMilli()},
		}}}})
	}
	firing := func() *Alert {
		for _, al := range e.a.alerts.Active() {
			if al.Metric == "cpu" {
				cp := al
				return &cp
			}
		}
		return nil
	}
	// Default CPU rule: warn 85 / crit 95, for 4 evaluations.
	for i := 0; i < 3; i++ {
		cpu(99)
	}
	if al := firing(); al == nil || al.Status != "pending" {
		t.Fatalf("expected pending after 3 evaluations, got %+v", al)
	}
	cpu(99)
	al := firing()
	if al == nil || al.Status != "firing" || al.Severity != "critical" {
		t.Fatalf("expected firing critical, got %+v", al)
	}
	// One good sample resolves it.
	cpu(10)
	if firing() != nil {
		t.Fatal("alert did not resolve")
	}
	// Flap suppression: a single spike never fires.
	cpu(99)
	cpu(10)
	if firing() != nil {
		t.Fatal("single spike produced an alert")
	}

	// Ack via API.
	for i := 0; i < 4; i++ {
		cpu(90)
	}
	al = firing()
	if al == nil || al.Severity != "warning" {
		t.Fatalf("expected warning: %+v", al)
	}
	if code, _, raw := admin.do("POST", "/alerts/"+itoa(al.ID)+"/ack", map[string]any{"comment": "looking"}); code != 200 {
		t.Fatalf("ack: %d %s", code, raw)
	}
	if al = firing(); al.AckedBy != "admin" {
		t.Fatalf("ack not recorded: %+v", al)
	}
	// Escalation to critical keeps the alert, raises severity.
	cpu(99)
	if al = firing(); al.Severity != "critical" {
		t.Fatalf("no escalation: %+v", al)
	}
	// TSDB has the samples; host list shows summary.
	_, hosts, raw := admin.do("GET", "/hosts/"+itoa(hostID)+"/metrics?panel=cpu&start=600s", nil)
	if !strings.Contains(string(raw), "cpu_usage_percent") {
		t.Fatalf("metrics: %v", hosts)
	}

	// Maintenance silences notifications but alerts still fire.
	code, _, raw := admin.do("POST", "/maintenance", map[string]any{"name": "patch", "scope": "host", "scope_ids": []int64{hostID},
		"starts_at": time.Now().Add(-time.Minute).Unix(), "ends_at": time.Now().Add(time.Hour).Unix(), "recurrence": "none", "timezone": "UTC"})
	if code != 201 {
		t.Fatalf("maintenance: %d %s", code, raw)
	}
	if !e.a.maint.hostSuppressed(hostID) {
		t.Fatal("host not suppressed during window")
	}
}

func pemBytes(t *testing.T, p []byte) []byte {
	b, _ := pem.Decode(p)
	if b == nil {
		t.Fatal("bad pem")
	}
	return b.Bytes
}

func TestJobsAreSignedAndGated(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, resp := e.enrol(admin)
	// Upgrades are opt-in per host.
	code, _, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "upgrade", "confirm": true})
	if code != 403 {
		t.Fatalf("upgrade without opt-in: %d", code)
	}
	// Dry run is allowed without opt-in.
	if code, _, raw := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "upgrade", "dry_run": true}); code != 201 {
		t.Fatalf("dry run: %d %s", code, raw)
	}
	admin.do("PUT", "/hosts/"+itoa(hostID), map[string]any{"allow_upgrades": true})
	if code, m, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "upgrade"}); code != 409 || m["code"] != "confirmation_required" {
		t.Fatalf("unconfirmed upgrade: %d %v", code, m)
	}
	// Invalid package names are rejected server-side.
	if code, _, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "upgrade", "confirm": true, "params": map[string]string{"packages": "openssl;rm -rf /"}}); code != 400 {
		t.Fatalf("bad package accepted: %d", code)
	}
	// Dispatch: register a fake connection and check the signature.
	conn := &agentConn{hostID: hostID, send: make(chan *pb.ServerMessage, 4), cancel: func() {}}
	e.a.hub.mu.Lock()
	e.a.hub.conns[hostID] = conn
	e.a.hub.mu.Unlock()
	e.a.jobs.tick(context.Background())
	select {
	case m := <-conn.send:
		sj := m.GetJob()
		if sj == nil || !ed25519.Verify(ed25519.PublicKey(resp.JobPublicKey), sj.Spec, sj.Signature) {
			t.Fatal("job not signed with the registered key")
		}
		var spec pb.JobSpec
		_ = proto.Unmarshal(sj.Spec, &spec)
		if spec.AgentId != resp.AgentId || spec.Type != "upgrade" || !spec.DryRun {
			t.Fatalf("spec: %+v", &spec)
		}
		// Tampering breaks the signature.
		sj.Spec[len(sj.Spec)-1] ^= 1
		if ed25519.Verify(ed25519.PublicKey(resp.JobPublicKey), sj.Spec, sj.Signature) {
			t.Fatal("tampered job verified")
		}
		// Result from a different host is ignored.
		e.a.jobs.result(context.Background(), hostID+99, &pb.JobResult{JobId: spec.Id, Status: "succeeded"})
		j, _ := e.a.getJob(context.Background(), spec.Id)
		if j.Status != "sent" {
			t.Fatalf("foreign result accepted: %s", j.Status)
		}
		e.a.jobs.output(context.Background(), hostID, &pb.JobOutput{JobId: spec.Id, Data: []byte("Inst openssl\n")})
		e.a.jobs.result(context.Background(), hostID, &pb.JobResult{JobId: spec.Id, Status: "succeeded"})
		j, _ = e.a.getJob(context.Background(), spec.Id)
		if j.Status != "succeeded" || !strings.Contains(j.Output, "openssl") {
			t.Fatalf("job: %+v", j)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job not dispatched")
	}
	// Scripts need the per-host opt-in even for admins.
	if code, _, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "script", "confirm": true, "params": map[string]string{"script": "id"}}); code != 403 {
		t.Fatalf("script without opt-in: %d", code)
	}
}

func TestWebhookNotificationGrouping(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	var mu sync.Mutex
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer hook.Close()
	code, _, raw := admin.do("POST", "/notification-channels", map[string]any{"name": "hook", "type": "webhook", "config": map[string]any{"url": hook.URL}})
	if code != 201 {
		t.Fatalf("channel: %d %s", code, raw)
	}
	// Test send.
	if code, m, _ := admin.do("POST", "/notification-channels/1/test", nil); code != 200 || m["ok"] != true {
		t.Fatalf("test send: %v", m)
	}
	admin.do("PUT", "/settings/alerting", map[string]any{"group_wait_seconds": 0, "flap_threshold": 6, "flap_window_minutes": 30, "resolve_count": 1, "default_repeat_minutes": 240})
	ctx := context.Background()
	hid := int64(0)
	for _, k := range []string{"a", "b"} {
		e.a.alerts.apply(ctx, condition{Key: "system:" + k, Source: "system", Severity: "critical", Title: "thing " + k, ForCount: 1, HostID: nilIf(hid)})
	}
	e.a.notifier.flushDue(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("expected test + one grouped message, got %d", len(got))
	}
	items := got[1]["alerts"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 alerts grouped in one message, got %d", len(items))
	}
}

func nilIf(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func TestOpenAPISpec(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	code, m, _ := c.do("GET", "/openapi.json", nil)
	if code != 200 || m["openapi"] != "3.0.3" {
		t.Fatalf("spec: %d", code)
	}
	paths := m["paths"].(map[string]any)
	if len(paths) < 80 {
		t.Fatalf("expected the full route table, got %d paths", len(paths))
	}
}

func TestAutoUpdateQueuesJob(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	count := func() int {
		var n int
		_ = e.a.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM jobs WHERE host_id=? AND type='self_update'`, hostID)
		return n
	}
	setVersion := func(v string) {
		if _, err := e.a.db.ExecContext(ctx, `UPDATE hosts SET agent_version=? WHERE id=?`, v, hostID); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		h, err := e.a.getHost(ctx, hostID)
		if err != nil {
			t.Fatal(err)
		}
		e.a.jobs.maybeAutoUpdate(ctx, h)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "heartbeat-agent-linux-amd64"), []byte("fake-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("2.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.a.cfg.AgentBinDir = dir
	setVersion("1.0.0")

	// Disabled: nothing happens.
	check()
	if count() != 0 {
		t.Fatal("job queued while auto_update is off")
	}
	d := e.a.settings.Agent()
	d.AutoUpdate = true
	if err := e.a.settings.Save(ctx, 0, keyAgent, d); err != nil {
		t.Fatal(err)
	}
	// Dev builds are left alone.
	setVersion("dev")
	check()
	if count() != 0 {
		t.Fatal("dev agent updated")
	}
	setVersion("1.0.0")
	check()
	if count() != 1 {
		t.Fatalf("expected one self_update job, got %d", count())
	}
	// No duplicates while one is pending.
	check()
	if count() != 1 {
		t.Fatalf("duplicate job: %d", count())
	}
	// Up-to-date agents are skipped.
	if _, err := e.a.db.ExecContext(ctx, `DELETE FROM jobs`); err != nil {
		t.Fatal(err)
	}
	setVersion("2.0.0")
	check()
	if count() != 0 {
		t.Fatal("current agent updated")
	}
}
