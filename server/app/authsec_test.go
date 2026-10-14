package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// tenantUser creates a tenant with one tenant_admin and returns the tenant id.
func tenantUser(t *testing.T, root *client, name, user string) int64 {
	t.Helper()
	tid := int64(okDo(t, root, "POST", "/tenants", map[string]any{"name": name})["id"].(float64))
	okDo(t, root, "POST", "/users", map[string]any{"tenant_id": tid, "username": user, "password": testPassword, "role": "tenant_admin"})
	return tid
}

func newKey(t *testing.T, c *client, scopes ...string) string {
	t.Helper()
	return okDo(t, c, "POST", "/api-keys", map[string]any{"name": "k", "scopes": scopes})["key"].(string)
}

func keyClient(e *testEnv, key string) *client {
	c := e.client()
	c.key = key
	return c
}

func errCode(m map[string]any) string { s, _ := m["code"].(string); return s }

// F04: a suspended tenant loses sessions and keys, and the keys revive on re-enable.
func TestDisabledTenantRejectsKeysAndSessions(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	tid := tenantUser(t, root, "Acme", "acme-admin")
	sess := e.login("acme-admin", testPassword)
	key := newKey(t, sess, "write")
	kc := keyClient(e, key)
	if code, _, raw := kc.do("GET", "/hosts", nil); code != 200 {
		t.Fatalf("key before suspension: %d %s", code, raw)
	}

	okDo(t, root, "PUT", "/tenants/"+itoa(tid), map[string]any{"name": "Acme", "disabled": true})
	if code, _, _ := kc.do("GET", "/hosts", nil); code != 401 {
		t.Fatalf("key of a disabled tenant accepted: %d", code)
	}
	if code, _, _ := kc.do("POST", "/groups", map[string]any{"name": "x"}); code != 401 {
		t.Fatalf("write with key of a disabled tenant accepted: %d", code)
	}
	if code, _, _ := sess.do("GET", "/hosts", nil); code != 401 {
		t.Fatalf("session of a disabled tenant accepted: %d", code)
	}
	// New logins are refused too.
	c := e.client()
	if code, _, _ := c.do("POST", "/auth/login", map[string]any{"username": "acme-admin", "password": testPassword}); code == 200 {
		t.Fatal("login to a disabled tenant succeeded")
	}
	// The provider keeps working.
	if code, _, _ := root.do("GET", "/hosts", nil); code != 200 {
		t.Fatalf("provider session: %d", code)
	}

	okDo(t, root, "PUT", "/tenants/"+itoa(tid), map[string]any{"name": "Acme", "disabled": false})
	if code, _, raw := kc.do("GET", "/hosts", nil); code != 200 {
		t.Fatalf("key after re-enable should work again: %d %s", code, raw)
	}
	e.login("acme-admin", testPassword)
}

// F05: account-security routes refuse API keys of any scope.
func TestAccountSecurityNeedsInteractiveSession(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	adminKey := newKey(t, root, "admin")
	readKey := newKey(t, root, "read")
	other := e.login("admin", testPassword) // a second browser session of the same user
	for _, key := range []string{adminKey, readKey} {
		kc := keyClient(e, key)
		for _, r := range []struct{ method, path string }{
			{"DELETE", "/auth/passkeys/1"}, {"GET", "/auth/passkeys"}, {"GET", "/auth/sessions"},
			{"POST", "/auth/sessions/revoke-others"}, {"POST", "/auth/totp/disable"}, {"POST", "/auth/totp/setup"},
			{"POST", "/auth/password"}, {"PUT", "/auth/me"}, {"POST", "/api-keys"}, {"DELETE", "/api-keys/1"}, {"GET", "/api-keys"},
		} {
			code, m, raw := kc.do(r.method, r.path, map[string]any{})
			if code != 403 || errCode(m) != "interactive_session_required" {
				t.Errorf("%s %s with key: %d %s", r.method, r.path, code, raw)
			}
		}
	}
	// Nothing was revoked and the keys still work for ordinary reads.
	if code, _, _ := other.do("GET", "/auth/me", nil); code != 200 {
		t.Fatalf("other session was revoked by an API key: %d", code)
	}
	if code, _, _ := keyClient(e, readKey).do("GET", "/hosts", nil); code != 200 {
		t.Fatal("read key stopped working")
	}
	// A cookie session still can do it.
	okDo(t, root, "POST", "/auth/sessions/revoke-others", nil)
	if code, _, _ := other.do("GET", "/auth/me", nil); code != 401 {
		t.Fatalf("cookie revoke-others did not revoke: %d", code)
	}
	okDo(t, root, "PUT", "/auth/me", map[string]any{"display_name": "Root", "theme": "dark"})
}

// F13: a flagged account can only change its password; its keys are rejected.
func TestMustChangePasswordEnforced(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	okDo(t, root, "POST", "/users", map[string]any{"username": "temp", "password": testPassword, "role": "viewer"})
	u := e.login("temp", testPassword)
	key := newKey(t, u, "read")
	kc := keyClient(e, key)
	if code, _, _ := kc.do("GET", "/hosts", nil); code != 200 {
		t.Fatalf("key before flag: %d", code)
	}
	var uid int64
	if err := e.a.db.Get(&uid, `SELECT id FROM users WHERE username='temp'`); err != nil {
		t.Fatal(err)
	}
	okDo(t, root, "PUT", "/users/"+itoa(uid), map[string]any{"must_change_password": true})

	for _, path := range []string{"/hosts", "/dashboard", "/checks", "/auth/sessions", "/api-keys"} {
		if code, m, raw := u.do("GET", path, nil); code != 403 || errCode(m) != "password_change_required" {
			t.Errorf("GET %s while flagged: %d %s", path, code, raw)
		}
	}
	if code, m, _ := u.do("POST", "/api-keys", map[string]any{"name": "bypass"}); code != 403 || errCode(m) != "password_change_required" {
		t.Errorf("flagged user created a key: %d", code)
	}
	if code, m, raw := kc.do("GET", "/hosts", nil); code != 403 || errCode(m) != "password_change_required" {
		t.Errorf("key of a flagged user: %d %s", code, raw)
	}
	code, me, _ := u.do("GET", "/auth/me", nil)
	if code != 200 || me["password_change_required"] != true {
		t.Fatalf("/auth/me while flagged: %d %v", code, me)
	}
	// A wrong current password changes nothing.
	if code, _, _ := u.do("POST", "/auth/password", map[string]any{"current_password": "nope", "new_password": "a-brand-new-pass-9"}); code != 400 {
		t.Fatalf("bad current password: %d", code)
	}
	okDo(t, u, "POST", "/auth/password", map[string]any{"current_password": testPassword, "new_password": "a-brand-new-pass-9"})
	if code, _, raw := u.do("GET", "/hosts", nil); code != 200 {
		t.Fatalf("session after change: %d %s", code, raw)
	}
	if code, _, raw := kc.do("GET", "/hosts", nil); code != 200 {
		t.Fatalf("key after change: %d %s", code, raw)
	}
	// Logout is always allowed.
	okDo(t, root, "PUT", "/users/"+itoa(uid), map[string]any{"must_change_password": true})
	okDo(t, u, "POST", "/auth/logout", nil)
}

func seedRecoveryUser(t *testing.T, e *testEnv, codes ...string) {
	t.Helper()
	root := e.setupAdmin()
	okDo(t, root, "POST", "/users", map[string]any{"username": "mfa", "password": testPassword, "role": "viewer"})
	list := "["
	for i, c := range codes {
		if i > 0 {
			list += ","
		}
		list += `"` + hashToken(c) + `"`
	}
	list += "]"
	if _, err := e.a.db.Exec(`UPDATE users SET totp_enabled=1, totp_secret='JBSWY3DPEHPK3PXP', recovery_codes=? WHERE username='mfa'`, list); err != nil {
		t.Fatal(err)
	}
}

// pendingLogin returns a client holding an MFA-pending session.
func pendingLogin(t *testing.T, e *testEnv) *client {
	t.Helper()
	c := e.client()
	code, m, raw := c.do("POST", "/auth/login", map[string]any{"username": "mfa", "password": testPassword})
	if code != 200 || m["mfa_required"] != true {
		t.Fatalf("pending login: %d %s", code, raw)
	}
	c.csrf, _ = m["csrf_token"].(string)
	return c
}

// F12: one recovery code, many concurrent sessions, exactly one winner.
func TestRecoveryCodeConsumedOnce(t *testing.T) {
	e := newTestEnv(t)
	seedRecoveryUser(t, e, "aaaaa-bbbbb", "ccccc-ddddd")
	const n = 6
	clients := make([]*client, n)
	for i := range clients {
		clients[i] = pendingLogin(t, e)
	}
	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	wins := make(chan int, n)
	wg.Add(n)
	ready.Add(n)
	for i := range clients {
		go func(c *client) {
			defer wg.Done()
			ready.Done()
			<-start
			req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/auth/mfa/totp", strings.NewReader(`{"code":"aaaaa-bbbbb"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", c.csrf)
			resp, err := c.http.Do(req)
			if err != nil {
				wins <- -1
				return
			}
			resp.Body.Close()
			wins <- resp.StatusCode
		}(clients[i])
	}
	ready.Wait()
	close(start)
	wg.Wait()
	close(wins)
	ok := 0
	for code := range wins {
		if code == 200 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("recovery code accepted %d times, want exactly 1", ok)
	}
	var left string
	_ = e.a.db.Get(&left, `SELECT recovery_codes FROM users WHERE username='mfa'`)
	if strings.Contains(left, hashToken("aaaaa-bbbbb")) || !strings.Contains(left, hashToken("ccccc-ddddd")) {
		t.Fatalf("recovery codes after use: %s", left)
	}
	// The other code still works once.
	c := pendingLogin(t, e)
	okDo(t, c, "POST", "/auth/mfa/totp", map[string]any{"code": "ccccc-ddddd"})
}

// F12: a storage failure must not elevate the session.
func TestRecoveryCodeFailsClosedOnDBError(t *testing.T) {
	e := newTestEnv(t)
	seedRecoveryUser(t, e, "aaaaa-bbbbb")
	c := pendingLogin(t, e)
	if _, err := e.a.db.Exec(`CREATE TRIGGER rc_fail BEFORE UPDATE OF recovery_codes ON users BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := c.do("POST", "/auth/mfa/totp", map[string]any{"code": "aaaaa-bbbbb"}); code == 200 {
		t.Fatal("code accepted although it could not be consumed")
	}
	if code, m, _ := c.do("GET", "/hosts", nil); code != 401 || errCode(m) != "mfa_required" {
		t.Fatalf("session was elevated after a storage error: %d %v", code, m)
	}
}

// F14: a rejected user update persists nothing and writes no audit entry.
func TestUserUpdateIsAllOrNothing(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	u := okDo(t, root, "POST", "/users", map[string]any{"username": "bob", "password": testPassword, "role": "viewer", "display_name": "Bob"})
	id := idOf(t, u)
	count := func(action string) int {
		var n int
		_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM audit_log WHERE action=?`, action)
		return n
	}
	for name, body := range map[string]map[string]any{
		"bad password":     {"display_name": "Evil", "role": "operator", "password": "x"},
		"auth source":      {"display_name": "Evil", "role": "operator", "auth_source": "ldap"},
		"unknown source":   {"display_name": "Evil", "auth_source": "magic"},
		"tenant move":      {"display_name": "Evil", "role": "operator", "tenant_id": 9},
		"role for tenant":  {"display_name": "Evil", "role": "tenant_admin"},
		"short password": {"display_name": "Evil", "role": "operator", "password": "short"},
	} {
		if code, _, raw := root.do("PUT", "/users/"+id, body); code != 400 {
			t.Fatalf("%s: %d %s", name, code, raw)
		}
		var dn, role string
		_ = e.a.db.QueryRow(`SELECT display_name, role FROM users WHERE id=?`, id).Scan(&dn, &role)
		if dn != "Bob" || role != "viewer" {
			t.Fatalf("%s persisted changes: %q %q", name, dn, role)
		}
	}
	if count("user.updated") != 0 {
		t.Fatal("rejected update was audited as applied")
	}
	// A valid update commits everything, audits once and drops sessions.
	bob := e.login("bob", testPassword)
	okDo(t, root, "PUT", "/users/"+id, map[string]any{"display_name": "Robert", "role": "operator", "password": "another-long-pass-2"})
	if count("user.updated") != 1 {
		t.Fatalf("want one user.updated entry, have %d", count("user.updated"))
	}
	var dn, role string
	_ = e.a.db.QueryRow(`SELECT display_name, role FROM users WHERE id=?`, id).Scan(&dn, &role)
	if dn != "Robert" || role != "operator" {
		t.Fatalf("update not applied: %q %q", dn, role)
	}
	if code, _, _ := bob.do("GET", "/hosts", nil); code != 401 {
		t.Fatalf("old session survived a password reset: %d", code)
	}
}

// L02: role changes from the directory are applied and lower privilege revokes sessions.
func TestExternalRoleSync(t *testing.T) {
	e := newTestEnv(t)
	root := e.setupAdmin()
	okDo(t, root, "POST", "/users", map[string]any{"username": "ldapper", "role": "admin", "auth_source": "ldap"})
	ctx := context.Background()
	u := mustUser(t, e, "ldapper")
	sess := func() int {
		now := time.Now().Unix()
		if _, err := e.a.db.Exec(`INSERT INTO sessions (id_hash, tenant_id, user_id, mfa_pending, csrf_token, ip, user_agent, created_at, expires_at, last_seen_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			randomToken(8), 0, u.ID, 0, "c", "", "", now, now+3600, now); err != nil {
			t.Fatal(err)
		}
		var n int
		_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM sessions WHERE user_id=?`, u.ID)
		return n
	}
	sess()
	// Same role: sessions kept.
	if _, err := e.a.auth.syncExternalUser(ctx, u, "a@b.c", "L", RoleAdmin, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM sessions WHERE user_id=?`, u.ID)
	if n < 1 {
		t.Fatal("sessions dropped although the role did not change")
	}
	// Lowered role: saved, sessions revoked.
	got, err := e.a.auth.syncExternalUser(ctx, u, "a@b.c", "L", RoleViewer, 0)
	if err != nil || got.Role != RoleViewer {
		t.Fatalf("role not saved: %v %+v", err, got)
	}
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM sessions WHERE user_id=?`, u.ID)
	if n != 0 {
		t.Fatalf("sessions survived a role downgrade: %d", n)
	}
	// Raised role is saved without revoking.
	u = got
	sess()
	got, err = e.a.auth.syncExternalUser(ctx, u, "a@b.c", "L", RoleOperator, 0)
	if err != nil || got.Role != RoleOperator {
		t.Fatalf("role raise: %v %+v", err, got)
	}
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM sessions WHERE user_id=?`, u.ID)
	if n < 1 {
		t.Fatal("sessions dropped on a role raise")
	}
}

func mustUser(t *testing.T, e *testEnv, name string) *User {
	t.Helper()
	var u User
	if err := e.a.db.Get(&u, `SELECT * FROM users WHERE username=?`, name); err != nil {
		t.Fatal(err)
	}
	return &u
}

// L03: unknown or oversized API keys cost a SHA-256, not an Argon2 hash.
func TestUnknownAPIKeyIsCheap(t *testing.T) {
	e := newTestEnv(t)
	e.setupAdmin()
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 300; i++ {
		if _, err := e.a.auth.apiKeyPrincipal(ctx, "sentrel_zzzzzzzz_"+randomToken(30)); err == nil {
			t.Fatal("unknown key accepted")
		}
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("300 unknown keys took %v; the unknown-prefix path is doing expensive hashing", d)
	}
	if _, err := e.a.auth.apiKeyPrincipal(ctx, "sentrel_zzzzzzzz_"+strings.Repeat("a", 5000)); err == nil {
		t.Fatal("oversized key accepted")
	}
}

// L04: the WebAuthn caches stay bounded when the origin comes from the request.
func TestWebAuthnStateIsBounded(t *testing.T) {
	e := newTestEnv(t)
	s := e.a.auth
	for i := 0; i < 200; i++ {
		r := httptest.NewRequest("POST", "/api/v1/auth/passkey/begin", nil)
		r.Host = "h" + itoa(int64(i)) + ".example.com:8443"
		if _, err := s.webAuthn(r); err != nil {
			t.Fatal(err)
		}
	}
	s.waMu.Lock()
	n := len(s.waCache)
	s.waMu.Unlock()
	if n > waCacheMax {
		t.Fatalf("origin cache holds %d entries, cap %d", n, waCacheMax)
	}
	for _, bad := range []string{"", "evil.com/path", "u@evil.com", "a b.com", strings.Repeat("a", 300) + ".com"} {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Host = bad
		if _, err := s.webAuthn(r); err == nil {
			t.Fatalf("host %q accepted", bad)
		}
	}
	// A normal proxy/port host still works, repeatedly.
	r := httptest.NewRequest("POST", "/x", nil)
	r.Host = "monitor.example.com:8443"
	for i := 0; i < 3; i++ {
		if _, err := s.webAuthn(r); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < waPendingMax*2; i++ {
		s.putWA(waPending{purpose: "login"})
	}
	s.waMu.Lock()
	n = len(s.waSess)
	s.waMu.Unlock()
	if n > waPendingMax {
		t.Fatalf("pending challenges %d, cap %d", n, waPendingMax)
	}
}
