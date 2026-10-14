package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sentrel/server/notify"
	"sentrel/server/probe"
)

// --- F06: stored credentials never leave the server ---

const (
	sPass  = "SENTINEL-basic-pass-7731"
	sAuth  = "SENTINEL-bearer-4418"
	sKey   = "SENTINEL-apikey-9920"
	sURLPW = "SENTINEL-urlpw-1357"
	sTok   = "SENTINEL-qtoken-2468"
	sDisc  = "SENTINEL-discord-3579"
	sSlack = "SENTINEL-slack-8642"
	sBot   = "SENTINEL-bot-5791"
)

var allSentinels = []string{sPass, sAuth, sKey, sURLPW, sTok, sDisc, sSlack, sBot}

func noSentinel(t *testing.T, where string, raw []byte) {
	t.Helper()
	for _, s := range allSentinels {
		if strings.Contains(string(raw), s) {
			t.Fatalf("%s leaks %s: %s", where, s, raw)
		}
	}
}

func mustAtoi(s string) int64 {
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}

func mkViewer(t *testing.T, root *client) string {
	t.Helper()
	okDo(t, root, "POST", "/users", map[string]any{"username": "viewer", "password": testPassword, "role": "viewer"})
	return testPassword
}

func assertAuditClean(t *testing.T, e *testEnv) {
	t.Helper()
	var rows []string
	_ = e.a.db.Select(&rows, `SELECT details FROM audit_log`)
	for _, r := range rows {
		noSentinel(t, "audit", []byte(r))
	}
}

func TestCheckResponsesNeverContainStoredCredentials(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	cfg := map[string]any{"basic_user": "u", "basic_password": sPass,
		"headers": map[string]any{"authorization": "Bearer " + sAuth, "X-Api-Key": sKey, "Accept": "text/plain"}}
	code, m, raw := root.do("POST", "/checks", map[string]any{"name": "web", "type": "http", "target": "https://example.com/", "config": cfg})
	if code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	noSentinel(t, "create response", raw)
	id := idOf(t, m)
	if !strings.Contains(string(raw), "text/plain") {
		t.Fatalf("harmless header should stay visible: %s", raw)
	}
	_, _, raw = root.do("GET", "/checks/"+id, nil)
	noSentinel(t, "get", raw)
	_, _, raw = root.do("GET", "/checks", nil)
	noSentinel(t, "list", raw)
	viewer := e.login("viewer", mkViewer(t, root))
	_, _, raw = viewer.do("GET", "/checks/"+id, nil)
	noSentinel(t, "viewer get", raw)

	// Round-trip the redacted view unchanged, as the UI does: the stored
	// secrets survive and the response still hides them.
	_, got, _ := root.do("GET", "/checks/"+id, nil)
	body := map[string]any{"name": "web2", "type": "http", "target": "https://example.com/", "config": got["config"]}
	code, _, raw = root.do("PUT", "/checks/"+id, body)
	if code != 200 {
		t.Fatalf("update: %d %s", code, raw)
	}
	noSentinel(t, "update response", raw)
	k := e.checkRow(t, mustAtoi(id))
	if checkConfig(&k).BasicPassword != sPass || checkConfig(&k).Headers["X-Api-Key"] != sKey {
		t.Fatalf("stored secrets lost on a no-op update: %s", k.Config)
	}
	// Omitting the password keeps it too.
	code, _, raw = root.do("PUT", "/checks/"+id, map[string]any{"name": "web3", "type": "http", "target": "https://example.com/", "config": map[string]any{"basic_user": "u"}})
	if code != 200 {
		t.Fatalf("update: %d %s", code, raw)
	}
	noSentinel(t, "update response (omitted)", raw)
	k = e.checkRow(t, mustAtoi(id))
	if checkConfig(&k).BasicPassword != sPass {
		t.Fatalf("stored password lost: %s", k.Config)
	}
	// Changing it stores the new value.
	code, _, _ = root.do("PUT", "/checks/"+id, map[string]any{"name": "web3", "type": "http", "target": "https://example.com/",
		"config": map[string]any{"basic_password": "NEW-pass", "headers": map[string]any{"Authorization": "Bearer NEW-token"}}})
	k = e.checkRow(t, mustAtoi(id))
	if code != 200 || checkConfig(&k).BasicPassword != "NEW-pass" || checkConfig(&k).Headers["Authorization"] != "Bearer NEW-token" {
		t.Fatalf("new secrets not stored: %d %s", code, k.Config)
	}
	// A masked header keeps the stored value, matched case-insensitively.
	root.do("PUT", "/checks/"+id, map[string]any{"name": "web3", "type": "http", "target": "https://example.com/",
		"config": map[string]any{"headers": map[string]any{"authorization": notify.Mask}}})
	k = e.checkRow(t, mustAtoi(id))
	if checkConfig(&k).Headers["authorization"] != "Bearer NEW-token" {
		t.Fatalf("masked header replaced the stored one: %s", k.Config)
	}

	// Push-token regeneration returns the same safe view.
	code, m, raw = root.do("POST", "/checks", map[string]any{"name": "beat", "type": "push", "config": cfg})
	if code != 201 {
		t.Fatalf("push: %d %s", code, raw)
	}
	noSentinel(t, "push create", raw)
	_, _, raw = root.do("POST", "/checks/"+idOf(t, m)+"/regenerate-token", nil)
	noSentinel(t, "regenerate", raw)
	assertAuditClean(t, e)
}

func TestChannelResponsesNeverContainStoredCredentials(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	mk := func(typ string, cfg map[string]any) string {
		code, m, raw := root.do("POST", "/notification-channels", map[string]any{"name": typ + "-ch", "type": typ, "config": cfg})
		if code != 201 {
			t.Fatalf("create %s: %d %s", typ, code, raw)
		}
		noSentinel(t, typ+" create", raw)
		return idOf(t, m)
	}
	hook := mk("webhook", map[string]any{"url": "https://u:" + sURLPW + "@hooks.example.com/h?token=" + sTok + "&x=1",
		"headers": map[string]any{"Authorization": "Bearer " + sAuth, "X-Api-Key": sKey, "X-Plain": "ok"}})
	mk("discord", map[string]any{"url": "https://discord.com/api/webhooks/123456/" + sDisc})
	mk("slack", map[string]any{"url": "https://hooks.slack.com/services/T000/B000/" + sSlack})
	mk("telegram", map[string]any{"bot_token": sBot, "chat_id": "42"})

	_, _, raw := root.do("GET", "/notification-channels", nil)
	noSentinel(t, "list", raw)
	if !strings.Contains(string(raw), "hooks.example.com") || !strings.Contains(string(raw), "X-Plain") {
		t.Fatalf("non-secret parts should stay visible: %s", raw)
	}
	stored := func(id string) string {
		var c string
		_ = e.a.db.Get(&c, `SELECT config FROM notification_channels WHERE id=?`, id)
		return c
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 4 {
		t.Fatalf("list: %v %s", err, raw)
	}
	// Send every redacted view back unchanged, as the UI does.
	for _, ch := range list {
		id := itoa(int64(ch["id"].(float64)))
		before := stored(id)
		code, _, raw := root.do("PUT", "/notification-channels/"+id, map[string]any{"name": ch["name"], "type": ch["type"], "config": ch["config"]})
		if code != 200 {
			t.Fatalf("update %s: %d %s", id, code, raw)
		}
		noSentinel(t, "update response", raw)
		var a, b map[string]any
		_ = json.Unmarshal([]byte(before), &a)
		_ = json.Unmarshal([]byte(stored(id)), &b)
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatalf("channel %s changed by a no-op update:\n%s\n%s", id, ja, jb)
		}
	}
	// Editing one part keeps the other secrets.
	_, _, raw = root.do("PUT", "/notification-channels/"+hook, map[string]any{"name": "hook", "type": "webhook",
		"config": map[string]any{"url": "https://u:" + notify.Mask + "@hooks.example.com/h?token=" + notify.Mask + "&x=2", "headers": map[string]any{"authorization": notify.Mask}}})
	noSentinel(t, "partial update", raw)
	got := stored(hook)
	for _, want := range []string{sURLPW, sTok, "x=2", sAuth} {
		if !strings.Contains(got, want) {
			t.Fatalf("stored config lost %q: %s", want, got)
		}
	}
	assertAuditClean(t, e)
}

func TestMaskURL(t *testing.T) {
	cases := []struct{ typ, in string }{
		{"webhook", "https://a:" + sURLPW + "@h.example/p?sig=" + sTok + "&Signature=" + sTok + "&auth=" + sTok + "&secret=" + sTok + "&password=" + sTok + "&key=" + sTok},
		{"discord", "https://discord.com/api/webhooks/1/" + sDisc},
		{"teams", "https://x.webhook.office.com/webhookb2/" + sDisc},
	}
	for _, c := range cases {
		out := notify.MaskURL(c.typ, c.in)
		noSentinel(t, c.typ, []byte(out))
		if back := notify.RestoreURL(c.typ, out, c.in); back != c.in {
			t.Fatalf("restore %s: %s != %s", c.typ, back, c.in)
		}
	}
}

// --- F08: dependency precedence ---

func TestExplicitCheckParentReplacesHostParent(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostParent, hptok := e.newPushCheck(t, admin, "host-parent", nil)
	healthy, qtok := e.newPushCheck(t, admin, "healthy", nil)
	hostID, _ := e.enrol(admin)
	if code, raw := e.setParent(admin, "host", hostID, "check", hostParent); code != 200 {
		t.Fatalf("host parent: %d %s", code, raw)
	}
	child, ctok := e.newPushCheck(t, admin, "child", map[string]any{"host_id": hostID, "parent_type": "check", "parent_id": healthy})
	e.pushGet("GET", hptok, "?status=down")
	e.pushGet("GET", qtok, "")
	e.pushGet("GET", ctok, "?status=down")
	al := e.checkAlert(child)
	if al == nil {
		t.Fatal("no child alert")
	}
	if r := e.a.dependencyReason(al); r != "" {
		t.Fatalf("explicit healthy parent must override the down host parent, got %q", r)
	}
	// A down explicit parent suppresses even though the host parent is healthy.
	e.pushGet("GET", hptok, "")
	e.pushGet("GET", qtok, "?status=down")
	if r := e.a.dependencyReason(al); r != "parent healthy is down" {
		t.Fatalf("down explicit parent: %q", r)
	}
	// Clearing the override restores inheritance (host parent healthy now).
	code, _, raw := admin.do("PUT", "/checks/"+itoa(child), map[string]any{"name": "child", "type": "push", "host_id": hostID, "parent_type": ""})
	if code != 200 {
		t.Fatalf("clear: %d %s", code, raw)
	}
	if r := e.a.dependencyReason(al); r != "" {
		t.Fatalf("after clearing, healthy host parent: %q", r)
	}
	e.pushGet("GET", hptok, "?status=down")
	if r := e.a.dependencyReason(al); r != "parent host-parent is down" {
		t.Fatalf("inherited host parent: %q", r)
	}
	// Recovery.
	e.pushGet("GET", hptok, "")
	if r := e.a.dependencyReason(al); r != "" {
		t.Fatalf("after recovery: %q", r)
	}
}

// --- F09, F10: host transfer ---

func TestHostTransferMovesLiveState(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	a := seedTenantFixture(t, e, root, "Acme Ltd", "acme")
	b := seedTenantFixture(t, e, root, "Globex Corp", "globex")
	hostTenantCache.Range(func(k, _ any) bool { hostTenantCache.Delete(k); return true })

	code, m, raw := a.admin.do("POST", "/checks", map[string]any{"name": "beat", "type": "push", "host_id": a.hostID})
	if code != 201 {
		t.Fatalf("check: %d %s", code, raw)
	}
	cid := mustAtoi(idOf(t, m))
	tok := m["push_token"].(string)
	rule := idOf(t, okDo(t, a.admin, "POST", "/thresholds", map[string]any{"scope": "host", "scope_id": a.hostID, "metric": "cpu", "warn": 50, "crit": 60}))
	// The fixture's maintenance window would silence the alert; drop it and
	// add a fresh host-scoped window once the alert is firing.
	okDo(t, root, "DELETE", "/maintenance/"+a.objects["maintenance"], nil)
	e.pushGet("GET", tok, "?status=down&msg=first")
	now := time.Now().Unix()
	win := idOf(t, okDo(t, a.admin, "POST", "/maintenance", map[string]any{"name": "later", "scope": "host", "scope_ids": []int64{a.hostID},
		"starts_at": now + 3600, "ends_at": now + 7200, "recurrence": "none", "timezone": "UTC"}))
	before := e.queued()
	if e.checkAlert(cid) == nil || before == 0 {
		t.Fatalf("expected a firing alert with a queued notification (queued=%d)", before)
	}
	// A run that started before the transfer and finishes after it.
	stale := e.checkRow(t, cid)
	sub := e.a.events.subscribe()
	defer e.a.events.unsubscribe(sub)

	okDo(t, root, "PUT", "/hosts/"+itoa(a.hostID), map[string]any{"tenant_id": b.id, "name": "moved-host"})

	if got := e.queued(); got >= before {
		t.Fatalf("notification for the former tenant still queued: %d >= %d", got, before)
	}
	if al := e.checkAlert(cid); al == nil || al.TenantID != b.id {
		t.Fatalf("live alert not moved: %+v", al)
	}
	e.pushGet("GET", tok, "?status=down&msg=NEWMSG-after-transfer")
	e.a.checks.recordWith(context.Background(), &stale, probe.Result{Up: false, Message: "STALE-RUN"}, 0)
	if al := e.checkAlert(cid); al == nil || al.TenantID != b.id {
		t.Fatalf("stale run re-assigned the alert: %+v", al)
	}
	e.pushGet("GET", tok, "")
	e.pushGet("GET", tok, "")

	_, _, raw = a.admin.do("GET", "/alerts?status=active", nil)
	if strings.Contains(string(raw), "NEWMSG") || strings.Contains(string(raw), "moved-host") || strings.Contains(string(raw), "STALE-RUN") {
		t.Fatalf("former tenant sees post-transfer alert content: %s", raw)
	}
	if code, _, raw = a.admin.do("GET", "/checks/"+itoa(cid), nil); code != 404 {
		t.Fatalf("former tenant still sees the check: %d %s", code, raw)
	}
	_, _, raw = b.admin.do("GET", "/alerts?status=all", nil)
	_, _, rawAct := b.admin.do("GET", "/alerts?status=active", nil)
	if !strings.Contains(string(rawAct), "beat is down") && !strings.Contains(string(raw), "beat is down") {
		t.Fatalf("new tenant does not see the alert: %s", rawAct)
	}
	for drained := false; !drained; {
		select {
		case ev := <-sub:
			if ev.TenantID == a.id && ev.Type == "alert.changed" {
				t.Fatalf("former tenant received an SSE event: %+v", ev)
			}
		default:
			drained = true
		}
	}
	var ruleTenant int64
	if err := e.a.db.Get(&ruleTenant, `SELECT tenant_id FROM threshold_rules WHERE id=?`, rule); err != nil || ruleTenant != b.id {
		t.Fatalf("host rule not moved: tenant=%d err=%v", ruleTenant, err)
	}
	if code, _, _ := a.admin.do("DELETE", "/thresholds/"+rule, nil); code != 404 {
		t.Fatalf("former tenant could delete the moved rule: %d", code)
	}
	var n int
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM maintenance_windows WHERE id=?`, win)
	if n != 0 {
		t.Fatal("host-scoped maintenance window of the former tenant still exists")
	}
}

func TestHostRuleRequiresMatchingTenant(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	a := seedTenantFixture(t, e, root, "Acme Ltd", "acme")
	b := seedTenantFixture(t, e, root, "Globex Corp", "globex")
	// A rule left by a former owner: tenant A, scope host, host belongs to B.
	res, err := e.a.db.Exec(`INSERT INTO threshold_rules (tenant_id, scope, scope_id, metric, label_match, warn, crit, for_count, enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,1,0,0)`, a.id, "host", b.hostID, "cpu", "", 1.0, 2.0, 1)
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := res.LastInsertId()
	_ = e.a.alerts.ReloadRules(context.Background())
	h := hostRef{ID: b.hostID, TenantID: b.id}
	if r := e.a.alerts.rules.resolve(h, "cpu", ""); r == nil || r.Origin == "host" {
		t.Fatalf("stale rule governs the host: %+v", r)
	}
	for _, mr := range e.a.alerts.rules.merged(h) {
		for _, c := range mr.Chain {
			if c.Rule.ID == rid {
				t.Fatal("stale rule in the merged view")
			}
		}
	}
	if code, _, _ := a.admin.do("DELETE", "/thresholds/"+itoa(rid), nil); code != 404 {
		t.Fatalf("former tenant delete: %d", code)
	}
	_, _, raw := a.admin.do("GET", "/thresholds?scope=host", nil)
	if strings.Contains(string(raw), `"scope_id":`+itoa(b.hostID)) {
		t.Fatalf("stale rule listed: %s", raw)
	}
}

// --- F14: a rejected host update changes nothing ---

func TestRejectedHostUpdateChangesNothing(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	a := seedTenantFixture(t, e, root, "Acme Ltd", "acme")
	b := seedTenantFixture(t, e, root, "Globex Corp", "globex")
	snap := func() string {
		var s string
		_ = e.a.db.Get(&s, `SELECT name||'|'||allow_upgrades||'|'||tenant_id FROM hosts WHERE id=?`, a.hostID)
		return s
	}
	was := snap()
	code, _, raw := a.admin.do("PUT", "/hosts/"+itoa(a.hostID), map[string]any{"name": "sneaky", "allow_upgrades": true, "tenant_id": b.id})
	if code != 403 || snap() != was {
		t.Fatalf("tenant admin move: %d %s; %s -> %s", code, raw, was, snap())
	}
	code, _, raw = root.do("PUT", "/hosts/"+itoa(a.hostID), map[string]any{"name": "sneaky", "allow_upgrades": true, "tenant_id": 999999})
	if code != 400 || snap() != was {
		t.Fatalf("unknown destination: %d %s; %s -> %s", code, raw, was, snap())
	}
	var cnt int
	_ = e.a.db.Get(&cnt, `SELECT COUNT(*) FROM audit_log WHERE action='host.updated'`)
	if cnt != 0 {
		t.Fatalf("rejected updates were audited: %d", cnt)
	}
	code, _, raw = root.do("PUT", "/hosts/"+itoa(a.hostID), map[string]any{"name": "ok-name", "allow_upgrades": true})
	if code != 200 || snap() == was {
		t.Fatalf("valid update: %d %s", code, raw)
	}
	_ = e.a.db.Get(&cnt, `SELECT COUNT(*) FROM audit_log WHERE action='host.updated'`)
	if cnt != 1 {
		t.Fatalf("expected exactly one host.updated audit entry, got %d", cnt)
	}
}

// --- L05: manual runs share the scheduler's limits ---

func TestManualRunUsesSchedulerLimits(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	id, _ := e.newPushCheck(t, root, "beat", nil)
	viewer := e.login("viewer", mkViewer(t, root))
	if code, _, _ := viewer.do("POST", "/checks/"+itoa(id)+"/run", nil); code != 403 {
		t.Fatalf("read-only run: %d", code)
	}
	if code, _, raw := root.do("POST", "/checks/"+itoa(id)+"/run", nil); code != 200 {
		t.Fatalf("run: %d %s", code, raw)
	}
	e.a.checks.mu.Lock()
	e.a.checks.running[id] = true
	e.a.checks.mu.Unlock()
	if code, _, _ := root.do("POST", "/checks/"+itoa(id)+"/run", nil); code != 409 {
		t.Fatalf("run while running: %d", code)
	}
	e.a.checks.mu.Lock()
	delete(e.a.checks.running, id)
	e.a.checks.mu.Unlock()
	// A full semaphore makes the run wait; a cancelled wait releases the guard.
	for i := 0; i < cap(e.a.checks.sem); i++ {
		e.a.checks.sem <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	k := e.checkRow(t, id)
	if _, err := e.a.checks.RunNow(ctx, &k); err == nil {
		t.Fatal("run should have waited for a slot")
	}
	e.a.checks.mu.Lock()
	still := e.a.checks.running[id]
	e.a.checks.mu.Unlock()
	if still {
		t.Fatal("guard not released after a cancelled wait")
	}
}
