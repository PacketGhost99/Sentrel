package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// kindTable maps an object kind to the table that holds it. Snapshots of B's
// rows are taken from these tables before and after each cross-tenant call.
var kindTable = map[string]string{
	"host": "hosts", "check": "checks", "alert": "alerts", "group": "host_groups",
	"maintenance": "maintenance_windows", "channel": "notification_channels", "status_page": "status_pages",
	"threshold": "threshold_rules", "job": "jobs", "vuln_finding": "vuln_findings", "vuln_schedule": "vuln_schedules",
	"enrollment_token": "enrollment_tokens", "push": "push_installs", "user": "users", "tenant": "tenants",
	"api_key": "api_keys",
}

// routeKinds maps the first path segment of a route with an {id} parameter to
// the object kind that the id names. A route whose segment is missing here
// fails the test, so a new resource cannot slip past unnoticed.
var routeKinds = map[string]string{
	"hosts": "host", "checks": "check", "alerts": "alert", "groups": "group", "maintenance": "maintenance",
	"notification-channels": "channel", "status-pages": "status_page", "thresholds": "threshold", "jobs": "job",
	"vulnerabilities": "vuln_finding", "vuln-schedules": "vuln_schedule", "enrollment-tokens": "enrollment_token",
	"push": "push", "users": "user", "tenants": "tenant", "api-keys": "api_key",
}

// skip lists routes with {id} that this test does not call, keyed by
// "METHOD /path". Each needs a reason.
var skip = map[string]string{
	"DELETE /auth/passkeys/{id}": "a passkey belongs to the caller's own user, not to a tenant; no passkey is seeded",
}

// knownLeak marks a route+kind case that currently leaks across tenants. The
// handler is not changed by this test; the case is skipped until it is fixed.
var knownLeak = map[string]string{}

type tenantFixture struct {
	id      int64
	hostID  int64
	admin   *client           // tenant_admin of the tenant, logged in
	objects map[string]string // object kind -> id
}

func okDo(t *testing.T, c *client, method, path string, body any) map[string]any {
	t.Helper()
	code, m, raw := c.do(method, path, body)
	if code < 200 || code > 299 {
		t.Fatalf("%s %s: %d %s", method, path, code, raw)
	}
	return m
}

func idOf(t *testing.T, m map[string]any) string {
	t.Helper()
	v, ok := m["id"].(float64)
	if !ok {
		t.Fatalf("response has no numeric id: %v", m)
	}
	return itoa(int64(v))
}

// seedTenantFixture creates one tenant with a tenant_admin, a tenant_viewer
// and one object of every kind the harness can create.
func seedTenantFixture(t *testing.T, e *testEnv, root *client, name, label string) *tenantFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()
	f := &tenantFixture{objects: map[string]string{}}
	o := f.objects

	f.id = int64(okDo(t, root, "POST", "/tenants", map[string]any{"name": name})["id"].(float64))
	tid := f.id
	okDo(t, root, "POST", "/users", map[string]any{"tenant_id": tid, "username": label + "-viewer", "password": testPassword, "role": "tenant_viewer"})
	admin := okDo(t, root, "POST", "/users", map[string]any{"tenant_id": tid, "username": label + "-admin", "password": testPassword, "role": "tenant_admin"})
	o["user"] = idOf(t, admin)
	f.admin = e.login(label+"-admin", testPassword)

	host := okDo(t, root, "POST", "/hosts/target", map[string]any{"name": label + "-host", "address": "192.0.2.10", "tenant_id": tid})
	o["host"] = idOf(t, host)
	hostID := int64(host["id"].(float64))
	f.hostID = hostID

	o["check"] = idOf(t, okDo(t, root, "POST", "/checks", map[string]any{"name": label + "-check", "type": "tcp", "target": "192.0.2.10:443", "tenant_id": tid}))
	o["group"] = idOf(t, okDo(t, root, "POST", "/groups", map[string]any{"name": label + "-group", "tenant_id": tid}))
	o["maintenance"] = idOf(t, okDo(t, root, "POST", "/maintenance", map[string]any{
		"tenant_id": tid, "name": label + "-maint", "scope": "host", "scope_ids": []int64{hostID},
		"starts_at": now - 60, "ends_at": now + 3600, "recurrence": "none", "timezone": "UTC"}))
	o["channel"] = idOf(t, okDo(t, root, "POST", "/notification-channels", map[string]any{
		"tenant_id": tid, "name": label + "-channel", "type": "webhook", "config": map[string]any{"url": "https://hooks.example.com/" + label}}))
	o["status_page"] = idOf(t, okDo(t, root, "POST", "/status-pages", map[string]any{
		"tenant_id": tid, "slug": label + "-status", "title": label + " status", "public": true}))
	o["threshold"] = idOf(t, okDo(t, root, "POST", "/thresholds", map[string]any{
		"tenant_id": tid, "scope": "global", "metric": "cpu", "warn": 80, "crit": 90}))
	o["vuln_schedule"] = idOf(t, okDo(t, root, "POST", "/vuln-schedules", map[string]any{
		"tenant_id": tid, "scope": "tenant", "frequency": "daily", "time_of_day": "03:00"}))
	o["enrollment_token"] = idOf(t, okDo(t, root, "POST", "/enrollment-tokens", map[string]any{
		"tenant_id": tid, "host_name": label + "-new", "ttl_minutes": 15}))
	okDo(t, f.admin, "POST", "/api-keys", map[string]any{"name": label + "-key", "scopes": []string{"read"}})
	var keyID int64
	if err := e.a.db.GetContext(ctx, &keyID, `SELECT id FROM api_keys WHERE name=?`, label+"-key"); err != nil {
		t.Fatalf("api key row: %v", err)
	}
	o["api_key"] = itoa(keyID)
	// Jobs need a connected agent to be created through the API, so the row is
	// inserted directly. Status "queued" is what the job loop would leave it in.
	jobID := "job-" + label
	if _, err := e.a.db.ExecContext(ctx, `INSERT INTO jobs (id, tenant_id, host_id, type, status, created_at, requested_by)
		VALUES (?,?,?,?,?,?,?)`, jobID, tid, hostID, "refresh_inventory", "queued", now, "seed"); err != nil {
		t.Fatal(err)
	}
	o["job"] = jobID

	// Rows that have no creation endpoint the harness can drive.
	e.a.alerts.apply(ctx, condition{Key: label + "-alert", TenantID: tid, Source: "system", Severity: "critical", Title: label + " alert", ForCount: 1})
	var alertID int64
	if err := e.a.db.GetContext(ctx, &alertID, `SELECT id FROM alerts WHERE key=?`, label+"-alert"); err != nil {
		t.Fatalf("alert row: %v", err)
	}
	o["alert"] = itoa(alertID)

	res, err := e.a.db.ExecContext(ctx, `INSERT INTO vuln_findings (tenant_id, host_id, vuln_id, package, version, first_seen_at, last_seen_at)
		VALUES (?,?,?,?,?,?,?)`, tid, hostID, "CVE-2099-"+label, "openssl", "1.0", now, now)
	if err != nil {
		t.Fatal(err)
	}
	fid, _ := res.LastInsertId()
	o["vuln_finding"] = itoa(fid)

	res, err = e.a.db.ExecContext(ctx, `INSERT INTO push_installs (tenant_id, method, target, username, status, created_at)
		VALUES (?,?,?,?,?,?)`, tid, "ssh", "192.0.2.10", "root", "succeeded", now)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := res.LastInsertId()
	o["push"] = itoa(pid)

	o["tenant"] = itoa(tid)

	// Every kind must now have an id and a row.
	for kind := range kindTable {
		if o[kind] == "" {
			t.Fatalf("fixture %s: no object of kind %s", label, kind)
		}
	}
	return f
}

// snapshotRow returns the full contents of one row, as JSON, or "[]" if the
// row does not exist.
func snapshotRow(t *testing.T, e *testEnv, kind, id string) string {
	t.Helper()
	table, ok := kindTable[kind]
	if !ok {
		t.Fatalf("no table for kind %q", kind)
	}
	rows, err := e.a.db.Queryx(`SELECT * FROM `+table+` WHERE id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		m := map[string]any{}
		if err := rows.MapScan(m); err != nil {
			t.Fatal(err)
		}
		for k, v := range m {
			if b, ok := v.([]byte); ok {
				m[k] = string(b)
			}
		}
		out = append(out, m)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// TestCrossTenantIDRoutes calls every route that takes an {id} as a tenant_admin
// of tenant A, passing the id of tenant B's object of the matching kind. The
// response must be 403 or 404, and B's row must be unchanged.
func TestCrossTenantIDRoutes(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	a := seedTenantFixture(t, e, root, "Acme Ltd", "acme")
	b := seedTenantFixture(t, e, root, "Globex Corp", "globex")

	// Sanity: B's own admin can read B's host, so a 404 for A means something.
	okDo(t, b.admin, "GET", "/hosts/"+b.objects["host"], nil)

	for _, d := range e.a.srv.api.defs {
		if !strings.Contains(d.Path, "{id}") {
			continue
		}
		key := d.Method + " " + d.Path
		if reason, ok := skip[key]; ok {
			t.Run(key, func(t *testing.T) { t.Skip("not exercised: " + reason) })
			continue
		}
		seg := strings.Split(strings.TrimPrefix(d.Path, "/"), "/")[0]
		kind, ok := routeKinds[seg]
		if !ok {
			t.Fatalf("route %s has no object kind: add it to routeKinds or skip", key)
		}
		t.Run(key, func(t *testing.T) {
			if reason, ok := knownLeak[key]; ok {
				t.Skip("KNOWN LEAK: " + reason)
			}
			target := b.objects[kind]
			path := strings.ReplaceAll(d.Path, "{id}", target)
			// The caller is A's tenant_admin, on a client bound to this subtest.
			c := &client{env: &testEnv{t: t, a: e.a, srv: e.srv}, http: a.admin.http, csrf: a.admin.csrf}
			var body any
			if d.Method == http.MethodPost || d.Method == http.MethodPut {
				body = map[string]any{}
			}
			before := snapshotRow(t, e, kind, target)
			code, _, raw := c.do(d.Method, path, body)
			if code != http.StatusForbidden && code != http.StatusNotFound {
				t.Fatalf("%s on tenant B's %s %s: status %d, want 403 or 404: %s", d.Method, kind, target, code, raw)
			}
			if after := snapshotRow(t, e, kind, target); after != before {
				t.Fatalf("tenant B's %s %s changed:\nbefore: %s\nafter:  %s", kind, target, before, after)
			}
		})
	}
}

// TestLiveEventsAreTenantScoped checks that a tenant A session subscribed to
// /api/v1/events never receives an event for tenant B.
func TestLiveEventsAreTenantScoped(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	a := seedTenantFixture(t, e, root, "Acme Ltd", "acme")
	b := seedTenantFixture(t, e, root, "Globex Corp", "globex")
	// hostTenantCache is process-wide and keyed by host id, which every test
	// database reuses. Clear it so this test sees the tenants it just created.
	hostTenantCache.Range(func(k, _ any) bool { hostTenantCache.Delete(k); return true })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/api/v1/events", nil)
	resp, err := a.admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events: %d", resp.StatusCode)
	}
	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	next := func() string {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("event stream closed")
			}
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for an event")
		}
		return ""
	}
	// The handler subscribes before it writes "hello", so after this line the
	// stream is live.
	for l := next(); l != "event: hello"; l = next() {
	}

	// Tenant B events, by tenant id and by host id only.
	e.a.events.publish(Event{Type: "check.changed", TenantID: b.id, ID: b.objects["check"]})
	e.a.events.publish(Event{Type: "host.updated", HostID: b.hostID})
	// A sentinel for tenant A. Events are delivered in order, so if a tenant B
	// event was going to arrive it arrives before this one.
	e.a.events.publish(Event{Type: "check.changed", TenantID: a.id, ID: a.objects["check"]})

	for {
		l := next()
		if !strings.HasPrefix(l, "data: ") {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "data: ")), &ev); err != nil {
			t.Fatalf("bad event %q: %v", l, err)
		}
		if ev.TenantID == b.id || (ev.HostID != 0 && ev.HostID == b.hostID) {
			t.Fatalf("tenant A received tenant B's event: %s", l)
		}
		if ev.TenantID == a.id {
			break
		}
	}

}
