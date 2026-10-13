package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sentrel/server/db"
)

// pushGet calls the public heartbeat endpoint without any credentials.
func (e *testEnv) pushGet(method, token, query string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+"/api/push/"+token+query, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func (e *testEnv) newPushCheck(t *testing.T, admin *client, name string, extra map[string]any) (int64, string) {
	t.Helper()
	body := map[string]any{"name": name, "type": "push"}
	for k, v := range extra {
		body[k] = v
	}
	code, m, raw := admin.do("POST", "/checks", body)
	if code != 201 {
		t.Fatalf("create push check: %d %s", code, raw)
	}
	tok, _ := m["push_token"].(string)
	if len(tok) < 32 {
		t.Fatalf("push token too short: %q", tok)
	}
	return int64(m["id"].(float64)), tok
}

func (e *testEnv) checkRow(t *testing.T, id int64) Check {
	t.Helper()
	var k Check
	if err := e.a.db.Get(&k, `SELECT * FROM checks WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	return k
}

func (e *testEnv) checkAlert(id int64) *Alert {
	for _, al := range e.a.alerts.Active() {
		if al.CheckID != nil && *al.CheckID == id {
			cp := al
			return &cp
		}
	}
	return nil
}

// ageBeat pretends the last beat arrived the given number of seconds ago and
// runs the evaluator.
func (e *testEnv) ageBeat(t *testing.T, id int64, secs int64) {
	t.Helper()
	if _, err := e.a.db.Exec(`UPDATE checks SET last_beat_at=? WHERE id=?`, db.Now()-secs, id); err != nil {
		t.Fatal(err)
	}
	e.a.checks.mu.Lock()
	e.a.checks.pushLoaded = time.Time{}
	e.a.checks.pushNext = map[int64]time.Time{}
	e.a.checks.mu.Unlock()
	e.a.checks.tickPush(context.Background())
}

func TestPushBeatAndTimeout(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	id, tok := e.newPushCheck(t, admin, "nightly backup", map[string]any{"interval_seconds": 60, "config": map[string]any{"grace_seconds": 30}})

	k := e.checkRow(t, id)
	if k.Status != "pending" || k.PushTokenHash != hashPushToken(tok) || k.FailureThreshold != 1 {
		t.Fatalf("new check: %+v", k)
	}
	// Waiting for the first beat is not a failure.
	e.a.checks.tickPush(context.Background())
	if al := e.checkAlert(id); al != nil || e.checkRow(t, id).Status != "pending" {
		t.Fatalf("pending check alerted or changed status")
	}

	if code, body := e.pushGet("GET", tok, ""); code != 200 || body != "ok" {
		t.Fatalf("beat: %d %q", code, body)
	}
	k = e.checkRow(t, id)
	if k.Status != "up" || k.LastBeatAt == 0 {
		t.Fatalf("after beat: %+v", k)
	}
	if code, _ := e.pushGet("POST", tok, "?msg=backup+done"); code != 200 {
		t.Fatalf("POST beat: %d", code)
	}
	if k = e.checkRow(t, id); k.LastBeatMsg != "backup done" || k.LastMessage != "backup done" {
		t.Fatalf("message not stored: %+v", k)
	}

	// Within interval + grace: still up.
	e.ageBeat(t, id, 80)
	if k = e.checkRow(t, id); k.Status != "up" || e.checkAlert(id) != nil {
		t.Fatalf("down inside the grace window: %+v", k)
	}
	// Past interval + grace: down with an alert.
	e.ageBeat(t, id, 100)
	k = e.checkRow(t, id)
	if k.Status != "down" || !strings.Contains(k.LastMessage, "No heartbeat") {
		t.Fatalf("expected down after timeout: %+v", k)
	}
	if al := e.checkAlert(id); al == nil || al.Status != "firing" {
		t.Fatalf("no firing alert after timeout: %+v", al)
	}

	// A new beat recovers it and resolves the alert.
	if code, _ := e.pushGet("GET", tok, ""); code != 200 {
		t.Fatal("recovery beat rejected")
	}
	if k = e.checkRow(t, id); k.Status != "up" || e.checkAlert(id) != nil {
		t.Fatalf("not recovered: %+v", k)
	}

	// An explicit failure report goes down at once.
	if code, _ := e.pushGet("GET", tok, "?status=down&msg=disk+full"); code != 200 {
		t.Fatal("down beat rejected")
	}
	if k = e.checkRow(t, id); k.Status != "down" || !strings.Contains(k.LastMessage, "disk full") || e.checkAlert(id) == nil {
		t.Fatalf("explicit down: %+v", k)
	}
	if code, _ := e.pushGet("GET", tok, "?status=bogus"); code != 400 {
		t.Fatalf("bad status accepted: %d", code)
	}
}

func TestPushFirstBeatNeverArrives(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	id, _ := e.newPushCheck(t, admin, "never", map[string]any{"interval_seconds": 60, "config": map[string]any{"grace_seconds": 30}})
	if _, err := e.a.db.Exec(`UPDATE checks SET created_at=? WHERE id=?`, db.Now()-1000, id); err != nil {
		t.Fatal(err)
	}
	e.a.checks.mu.Lock()
	e.a.checks.pushLoaded = time.Time{}
	e.a.checks.mu.Unlock()
	e.a.checks.tickPush(context.Background())
	if k := e.checkRow(t, id); k.Status != "down" || e.checkAlert(id) == nil {
		t.Fatalf("a check that never reported should go down: %+v", k)
	}
}

func TestPushBadTokens(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	id, tok := e.newPushCheck(t, admin, "real", nil)

	wrong := tok[:len(tok)-1] + map[bool]string{true: "A", false: "B"}[tok[len(tok)-1] != 'A']
	cases := []string{wrong, "short", strings.Repeat("a", 32), strings.Repeat("!", 32), strings.Repeat("a", 200)}
	var first string
	for i, c := range cases {
		code, body := e.pushGet("GET", c, "")
		if code != 404 {
			t.Fatalf("token %q: %d", c, code)
		}
		if i == 0 {
			first = body
		} else if body != first {
			t.Fatalf("response differs between unknown tokens: %q vs %q", body, first)
		}
	}
	if k := e.checkRow(t, id); k.LastBeatAt != 0 {
		t.Fatal("a bad token recorded a beat")
	}
	if code, _ := e.pushGet("DELETE", tok, ""); code != 405 {
		t.Fatalf("DELETE: %d", code)
	}

	// Disabled checks look the same as unknown tokens.
	code, _, raw := admin.do("PUT", "/checks/"+itoa(id), map[string]any{"name": "real", "type": "push", "enabled": false})
	if code != 200 {
		t.Fatalf("disable: %d %s", code, raw)
	}
	if code, _ := e.pushGet("GET", tok, ""); code != 404 {
		t.Fatalf("disabled check accepted a beat: %d", code)
	}
	admin.do("PUT", "/checks/"+itoa(id), map[string]any{"name": "real", "type": "push", "enabled": true})

	// Regenerating the token invalidates the old URL.
	code, m, raw := admin.do("POST", "/checks/"+itoa(id)+"/regenerate-token", nil)
	if code != 200 {
		t.Fatalf("regenerate: %d %s", code, raw)
	}
	newTok, _ := m["push_token"].(string)
	if newTok == "" || newTok == tok {
		t.Fatal("token not changed")
	}
	if code, _ := e.pushGet("GET", tok, ""); code != 404 {
		t.Fatalf("old token still works: %d", code)
	}
	if code, _ := e.pushGet("GET", newTok, ""); code != 200 {
		t.Fatalf("new token rejected: %d", code)
	}
}

func TestPushTokenHiddenFromReadOnly(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	_, tok := e.newPushCheck(t, admin, "secret url", nil)
	if code, _, raw := admin.do("POST", "/users", map[string]any{"username": "viewer", "password": testPassword, "role": "viewer"}); code != 201 {
		t.Skipf("cannot create a viewer: %d %s", code, raw)
	}
	v := e.login("viewer", testPassword)
	code, _, raw := v.do("GET", "/checks", nil)
	if code != 200 || strings.Contains(string(raw), tok) {
		t.Fatalf("viewer saw the push token: %d", code)
	}
}

func TestPushRateLimits(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	_, tok := e.newPushCheck(t, admin, "limited", nil)

	e.a.checks.pushLimit = newRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if code, _ := e.pushGet("GET", tok, ""); code != 200 {
			t.Fatalf("beat %d: %d", i, code)
		}
	}
	if code, _ := e.pushGet("GET", tok, ""); code != 429 {
		t.Fatalf("per-token limit not enforced: %d", code)
	}

	// Guessing unknown tokens is throttled per client.
	e.a.checks.pushMiss = newRateLimiter(5, time.Minute)
	got429 := false
	for i := 0; i < 10; i++ {
		code, _ := e.pushGet("GET", fmt.Sprintf("%032d", i), "")
		if code == 429 {
			got429 = true
			break
		}
		if code != 404 {
			t.Fatalf("miss %d: %d", i, code)
		}
	}
	if !got429 {
		t.Fatal("unknown-token guessing was not throttled")
	}
}

// --- dependencies ---

func (e *testEnv) setParent(admin *client, childType string, childID int64, parentType string, parentID int64) (int, []byte) {
	body := map[string]any{"parent_type": parentType, "parent_id": parentID}
	if childType == "check" {
		// Updating a check needs its full definition.
		k := e.checkRow(e.t, childID)
		body["name"], body["type"] = k.Name, k.Type
	}
	path := "/hosts/"
	if childType == "check" {
		path = "/checks/"
	}
	code, _, raw := admin.do("PUT", path+itoa(childID), body)
	return code, raw
}

func TestDependencyValidationAndCycles(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	a, _ := e.newPushCheck(t, admin, "a", nil)
	b, _ := e.newPushCheck(t, admin, "b", nil)
	c, _ := e.newPushCheck(t, admin, "c", nil)

	if code, raw := e.setParent(admin, "check", b, "check", a); code != 200 {
		t.Fatalf("b under a: %d %s", code, raw)
	}
	if code, raw := e.setParent(admin, "check", c, "check", b); code != 200 {
		t.Fatalf("c under b: %d %s", code, raw)
	}
	for _, tc := range []struct {
		name         string
		child, p     int64
		wantContains string
	}{
		{"self", a, a, "itself"},
		{"direct cycle", a, b, "cycle"},
		{"indirect cycle", a, c, "cycle"},
	} {
		code, raw := e.setParent(admin, "check", tc.child, "check", tc.p)
		if code != 400 || !strings.Contains(string(raw), tc.wantContains) {
			t.Errorf("%s: %d %s", tc.name, code, raw)
		}
	}
	if code, _ := e.setParent(admin, "check", a, "check", 99999); code != 400 {
		t.Errorf("missing parent accepted: %d", code)
	}
	if code, _ := e.setParent(admin, "check", a, "bogus", 1); code != 400 {
		t.Errorf("bad parent type accepted: %d", code)
	}

	// Cycles through a host are caught too.
	hostID, _ := e.enrol(admin)
	if code, raw := e.setParent(admin, "host", hostID, "check", a); code != 200 {
		t.Fatalf("host under check: %d %s", code, raw)
	}
	if code, raw := e.setParent(admin, "check", a, "host", hostID); code != 400 || !strings.Contains(string(raw), "cycle") {
		t.Errorf("host/check cycle: %d %s", code, raw)
	}

	// Parent is reported on the child; clearing works.
	_, m, _ := admin.do("GET", "/checks/"+itoa(b), nil)
	if p, _ := m["parent"].(map[string]any); p == nil || p["name"] != "a" {
		t.Fatalf("parent not reported: %v", m["parent"])
	}
	if code, _ := e.setParent(admin, "check", b, "", 0); code != 200 {
		t.Fatal("clear parent failed")
	}
	_, m, _ = admin.do("GET", "/checks/"+itoa(b), nil)
	if m["parent"] != nil {
		t.Fatalf("parent not cleared: %v", m["parent"])
	}
	// Deleting a parent drops the link.
	admin.do("DELETE", "/checks/"+itoa(a), nil)
	_, m, _ = admin.do("GET", "/hosts/"+itoa(hostID), nil)
	if m["parent"] != nil {
		t.Fatalf("link to a deleted parent survived: %v", m["parent"])
	}
}

func TestDependencySuppressesNotifications(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer hook.Close()
	if code, _, raw := admin.do("POST", "/notification-channels", map[string]any{"name": "hook", "type": "webhook", "config": map[string]any{"url": hook.URL}}); code != 201 {
		t.Fatalf("channel: %d %s", code, raw)
	}
	// Keep queued messages in the queue so the test can count them.
	admin.do("PUT", "/settings/alerting", map[string]any{"group_wait_seconds": 3600, "flap_threshold": 50, "flap_window_minutes": 30, "resolve_count": 1, "default_repeat_minutes": 240})

	parent, ptok := e.newPushCheck(t, admin, "core router", nil)
	child, ctok := e.newPushCheck(t, admin, "branch site", nil)
	plain, qtok := e.newPushCheck(t, admin, "unrelated", nil)
	if code, raw := e.setParent(admin, "check", child, "check", parent); code != 200 {
		t.Fatalf("set parent: %d %s", code, raw)
	}
	for _, tok := range []string{ptok, ctok, qtok} {
		e.pushGet("GET", tok, "")
	}

	// Parent up: the child alerts and notifies as usual.
	e.pushGet("GET", ctok, "?status=down")
	if al := e.checkAlert(child); al == nil || e.queued() != 1 {
		t.Fatalf("child alert with a healthy parent: alert=%v queued=%d", e.checkAlert(child), e.queued())
	}
	e.pushGet("GET", ctok, "") // resolves
	e.a.notifier.mu.Lock()
	e.a.notifier.queue = map[int64][]queuedNote{}
	e.a.notifier.mu.Unlock()

	// Parent down: its own alert notifies, the child's does not.
	e.pushGet("GET", ptok, "?status=down")
	if e.checkAlert(parent) == nil {
		t.Fatal("parent alert missing")
	}
	base := e.queued()
	if base != 1 {
		t.Fatalf("parent notification not queued: %d", base)
	}
	e.pushGet("GET", ctok, "?status=down")
	al := e.checkAlert(child)
	if al == nil || al.Status != "firing" {
		t.Fatalf("child alert should still be raised: %+v", al)
	}
	if got := e.queued(); got != base {
		t.Fatalf("child notified while its parent is down: queued %d, want %d", got, base)
	}
	var silenced string
	_ = e.a.db.Get(&silenced, `SELECT message FROM alert_events WHERE alert_id=? AND event='silenced'`, al.ID)
	if silenced != "parent core router is down" {
		t.Fatalf("silenced event: %q", silenced)
	}
	// The API marks the alert as suppressed.
	_, _, raw := admin.do("GET", "/alerts?status=active", nil)
	if !strings.Contains(string(raw), `"suppressed_by":"parent core router is down"`) {
		t.Fatalf("alert list lacks suppressed_by: %s", raw)
	}
	// An unrelated check is unaffected.
	e.pushGet("GET", qtok, "?status=down")
	if e.checkAlert(plain) == nil || e.queued() != base+1 {
		t.Fatalf("unrelated check was suppressed: queued=%d", e.queued())
	}
	// Alerts of the parent are never suppressed by its own children.
	if reason := e.a.dependencyReason(&Alert{CheckID: &parent}); reason != "" {
		t.Fatalf("parent suppressed: %q", reason)
	}
	// Once the parent recovers the child is no longer marked suppressed.
	e.pushGet("GET", ptok, "")
	if reason := e.a.dependencyReason(e.checkAlert(child)); reason != "" {
		t.Fatalf("still suppressed after the parent recovered: %q", reason)
	}
}

func TestHostDependencyInheritedByItsChecks(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	parent, ptok := e.newPushCheck(t, admin, "gateway", nil)
	hostID, _ := e.enrol(admin)
	if code, raw := e.setParent(admin, "host", hostID, "check", parent); code != 200 {
		t.Fatalf("host parent: %d %s", code, raw)
	}
	e.pushGet("GET", ptok, "?status=down")
	h := hostID
	// Host alert and any check alert on the host are suppressed.
	if r := e.a.dependencyReason(&Alert{HostID: &h}); r != "parent gateway is down" {
		t.Fatalf("host alert: %q", r)
	}
	ck := int64(424242)
	if r := e.a.dependencyReason(&Alert{HostID: &h, CheckID: &ck}); r != "parent gateway is down" {
		t.Fatalf("check on host: %q", r)
	}
	e.pushGet("GET", ptok, "")
	if r := e.a.dependencyReason(&Alert{HostID: &h}); r != "" {
		t.Fatalf("suppressed after recovery: %q", r)
	}
}

// --- repeat notifications ---

func TestRepeatNotifications(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer hook.Close()
	code, ch, raw := admin.do("POST", "/notification-channels", map[string]any{"name": "hook", "type": "webhook", "repeat_minutes": 10, "config": map[string]any{"url": hook.URL}})
	if code != 201 {
		t.Fatalf("channel: %d %s", code, raw)
	}
	chID := int64(ch["id"].(float64))
	admin.do("PUT", "/settings/alerting", map[string]any{"group_wait_seconds": 3600, "flap_threshold": 50, "flap_window_minutes": 30, "resolve_count": 1, "default_repeat_minutes": 240})

	id, tok := e.newPushCheck(t, admin, "job", nil)
	e.pushGet("GET", tok, "")
	e.pushGet("GET", tok, "?status=down")
	al := e.checkAlert(id)
	if al == nil {
		t.Fatal("no alert")
	}
	clear := func() {
		e.a.notifier.mu.Lock()
		e.a.notifier.queue = map[int64][]queuedNote{}
		e.a.notifier.mu.Unlock()
	}
	sentAgo := func(secs int64) {
		e.a.notifier.mu.Lock()
		e.a.notifier.lastSent[[2]int64{chID, al.ID}] = db.Now() - secs
		e.a.notifier.mu.Unlock()
	}
	ctx := context.Background()

	// Before the interval nothing is repeated.
	clear()
	sentAgo(5 * 60)
	e.a.notifier.reminders(ctx)
	if e.queued() != 0 {
		t.Fatal("repeated before the interval elapsed")
	}
	// After the interval it is.
	sentAgo(11 * 60)
	e.a.notifier.reminders(ctx)
	if e.queued() != 1 {
		t.Fatalf("expected a repeat after the interval, queued=%d", e.queued())
	}

	// Acknowledged alerts stop repeating.
	if code, _, raw := admin.do("POST", "/alerts/"+itoa(al.ID)+"/ack", map[string]any{"comment": "on it"}); code != 200 {
		t.Fatalf("ack: %d %s", code, raw)
	}
	clear()
	sentAgo(11 * 60)
	e.a.notifier.reminders(ctx)
	if e.queued() != 0 {
		t.Fatal("repeated an acknowledged alert")
	}
	// Removing the acknowledgement resumes them.
	admin.do("POST", "/alerts/"+itoa(al.ID)+"/unack", nil)
	sentAgo(11 * 60)
	e.a.notifier.reminders(ctx)
	if e.queued() != 1 {
		t.Fatal("did not resume after unack")
	}

	// A channel with repeat 0 never repeats.
	admin.do("PUT", "/notification-channels/"+itoa(chID), map[string]any{"name": "hook", "type": "webhook", "repeat_minutes": 0, "config": map[string]any{"url": hook.URL}})
	e.a.notifier.Invalidate()
	clear()
	sentAgo(48 * 3600)
	e.a.notifier.reminders(ctx)
	if e.queued() != 0 {
		t.Fatal("repeat 0 should be off")
	}

	// Resolution stops repeats and forgets the state.
	admin.do("PUT", "/notification-channels/"+itoa(chID), map[string]any{"name": "hook", "type": "webhook", "repeat_minutes": 10, "config": map[string]any{"url": hook.URL}})
	e.a.notifier.Invalidate()
	e.pushGet("GET", tok, "")
	clear()
	e.a.notifier.reminders(ctx)
	if e.queued() != 0 {
		t.Fatal("repeated after resolution")
	}
}

func TestRepeatStopsWhileSuppressed(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer hook.Close()
	_, ch, _ := admin.do("POST", "/notification-channels", map[string]any{"name": "hook", "type": "webhook", "repeat_minutes": 10, "config": map[string]any{"url": hook.URL}})
	chID := int64(ch["id"].(float64))
	admin.do("PUT", "/settings/alerting", map[string]any{"group_wait_seconds": 3600, "flap_threshold": 50, "flap_window_minutes": 30, "resolve_count": 1, "default_repeat_minutes": 240})

	parent, ptok := e.newPushCheck(t, admin, "upstream", nil)
	child, ctok := e.newPushCheck(t, admin, "downstream", nil)
	e.setParent(admin, "check", child, "check", parent)
	e.pushGet("GET", ptok, "")
	e.pushGet("GET", ctok, "?status=down")
	al := e.checkAlert(child)
	e.a.notifier.mu.Lock()
	e.a.notifier.queue = map[int64][]queuedNote{}
	e.a.notifier.lastSent[[2]int64{chID, al.ID}] = db.Now() - 3600
	e.a.notifier.mu.Unlock()

	e.pushGet("GET", ptok, "?status=down")
	e.a.notifier.mu.Lock()
	e.a.notifier.queue = map[int64][]queuedNote{}
	e.a.notifier.mu.Unlock()
	e.a.notifier.reminders(context.Background())
	for _, q := range e.a.notifier.queue {
		for _, n := range q {
			if n.alert.ID == al.ID {
				t.Fatal("repeated a suppressed alert")
			}
		}
	}
}
