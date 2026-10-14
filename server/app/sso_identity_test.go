package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func identityCount(t *testing.T, e *testEnv, where string, args ...any) int {
	t.Helper()
	var n int
	if err := e.a.db.Get(&n, `SELECT COUNT(*) FROM external_identities WHERE `+where, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func auditContains(t *testing.T, e *testEnv, action, needle string) bool {
	t.Helper()
	var rows []string
	if err := e.a.db.Select(&rows, `SELECT details FROM audit_log WHERE action=?`, action); err != nil {
		t.Fatal(err)
	}
	for _, d := range rows {
		if strings.Contains(d, needle) {
			return true
		}
	}
	return false
}

func TestOIDCStableIdentity(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	f := newFakeIdP(t)
	f.configure(e, admin, nil)

	f.claims = map[string]any{"preferred_username": "alice", "email": "alice@example.com", "groups": []string{"hb-viewers"}}
	loc, cl := f.signIn(e, nil)
	if loc != "/" {
		t.Fatalf("first login: %s", loc)
	}
	alice := meUser(t, cl)
	aliceID := alice["id"]
	if identityCount(t, e, `provider='oidc' AND issuer=? AND subject='sub-alice'`, f.srv.URL) != 1 {
		t.Fatal("identity not linked on provisioning")
	}

	t.Run("different subject with the same username never reuses the account", func(t *testing.T) {
		f.claims = map[string]any{"preferred_username": "alice", "sub": "sub-evil", "groups": []string{"hb-admins"}}
		loc, cl := f.signIn(e, nil)
		if !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
			t.Fatalf("second subject took over the account: %s", loc)
		}
		var role string
		_ = e.a.db.Get(&role, `SELECT role FROM users WHERE username='alice'`)
		if role != "viewer" {
			t.Fatalf("role was overwritten: %s", role)
		}
		if !auditContains(t, e, "auth.login_failed", "already linked to a different identity") {
			t.Fatal("refusal not audited")
		}
	})

	t.Run("usernames that normalise to the same name do not collide", func(t *testing.T) {
		f.claims = map[string]any{"preferred_username": "bob smith", "groups": []string{"hb-viewers"}}
		if loc, _ := f.signIn(e, nil); loc != "/" {
			t.Fatalf("bob smith: %s", loc)
		}
		f.claims = map[string]any{"preferred_username": "bob_smith", "sub": "sub-other-bob", "groups": []string{"hb-admins"}}
		loc, cl := f.signIn(e, nil)
		if !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
			t.Fatalf("colliding name reused account: %s", loc)
		}
	})

	t.Run("a renamed subject keeps its account", func(t *testing.T) {
		f.claims = map[string]any{"preferred_username": "alice.renamed", "sub": "sub-alice", "email": "new@example.com", "name": "Alice R", "groups": []string{"hb-viewers"}}
		loc, cl := f.signIn(e, nil)
		if loc != "/" {
			t.Fatalf("renamed login: %s", loc)
		}
		u := meUser(t, cl)
		if u["id"] != aliceID || u["username"] != "alice.renamed" || u["email"] != "new@example.com" {
			t.Fatalf("rename not applied to the same account: %v", u)
		}
	})

	t.Run("a rename onto a taken name keeps the old one", func(t *testing.T) {
		f.claims = map[string]any{"preferred_username": "admin", "sub": "sub-alice", "groups": []string{"hb-viewers"}}
		loc, cl := f.signIn(e, nil)
		if loc != "/" {
			t.Fatalf("login: %s", loc)
		}
		if u := meUser(t, cl); u["id"] != aliceID || u["username"] != "alice.renamed" {
			t.Fatalf("username moved onto another account: %v", u)
		}
	})

	t.Run("a changed issuer does not bind to old users", func(t *testing.T) {
		g := newFakeIdP(t)
		g.configure(e, admin, nil)
		g.claims = map[string]any{"preferred_username": "alice.renamed", "sub": "sub-alice", "groups": []string{"hb-admins"}}
		loc, cl := g.signIn(e, nil)
		if !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
			t.Fatalf("new issuer bound to an old user: %s", loc)
		}
		f.configure(e, admin, nil)
	})

	t.Run("deleting the user removes its identities", func(t *testing.T) {
		if code, _, raw := admin.do("DELETE", "/users/"+itoa(int64(aliceID.(float64))), nil); code != 200 && code != 204 {
			t.Fatalf("delete: %d %s", code, raw)
		}
		if n := identityCount(t, e, `user_id=?`, int64(aliceID.(float64))); n != 0 {
			t.Fatalf("%d identities left behind", n)
		}
		if n := identityCount(t, e, `subject='sub-alice'`); n != 0 {
			t.Fatal("identity left behind")
		}
	})
}

func TestOIDCMigrationLink(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	f := newFakeIdP(t)
	f.configure(e, admin, nil)

	// Accounts created by an earlier version have no identity recorded.
	if _, err := e.a.auth.provision(context.Background(), "legacy", "", "Legacy", "viewer", 0, "oidc"); err != nil {
		t.Fatal(err)
	}
	f.claims = map[string]any{"preferred_username": "legacy", "groups": []string{"hb-viewers"}}
	if loc, cl := f.signIn(e, nil); loc != "/" || meUser(t, cl)["username"] != "legacy" {
		t.Fatalf("migration link refused: %s", loc)
	}
	if identityCount(t, e, `subject='sub-legacy'`) != 1 {
		t.Fatal("identity not linked during migration")
	}
	// The link is one-time: another subject cannot claim the account now.
	f.claims["sub"] = "sub-impostor"
	if loc, cl := f.signIn(e, nil); !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
		t.Fatalf("linked account claimed by another subject: %s", loc)
	}

	// Local accounts are never auto-linked.
	if _, err := e.a.auth.provision(context.Background(), "localguy", "", "Local", "viewer", 0, "local"); err != nil {
		t.Fatal(err)
	}
	f.claims = map[string]any{"preferred_username": "localguy", "groups": []string{"hb-admins"}}
	if loc, _ := f.signIn(e, nil); !strings.Contains(loc, "sso_error") {
		t.Fatalf("local account linked: %s", loc)
	}
	if identityCount(t, e, `subject='sub-localguy'`) != 0 {
		t.Fatal("identity created for local account")
	}
}

func TestForwardAuthStableIdentity(t *testing.T) {
	e := newTestEnv(t)
	e.setupAdmin()
	setProxy := func(header string) {
		saveSettings(t, e, keyProxyAuth, &ProxyAuthSettings{Enabled: true, UserHeader: header, AllowProvision: true,
			DefaultRole: "viewer"})
	}
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "http", Listen: "0.0.0.0:8080", TrustProxy: true})
	status := func(header, user string) int {
		jar, _ := cookiejar.New(nil)
		hc := &http.Client{Jar: jar}
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/auth/me", nil)
		req.Header.Set(header, user)
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode
	}

	setProxy("Remote-User")
	if status("Remote-User", "pat") != 200 {
		t.Fatal("proxy login failed")
	}
	if identityCount(t, e, `provider='proxy' AND issuer='remote-user' AND subject='pat'`) != 1 {
		t.Fatal("proxy identity not linked")
	}
	// A different header name is a different identity namespace.
	setProxy("X-Auth-User")
	if status("X-Auth-User", "pat") == 200 {
		t.Fatal("different header bound to an existing proxy user")
	}
	setProxy("Remote-User")

	// Pre-upgrade proxy accounts link once.
	if _, err := e.a.auth.provision(context.Background(), "oldproxy", "", "Old", "viewer", 0, "proxy"); err != nil {
		t.Fatal(err)
	}
	if status("Remote-User", "oldproxy") != 200 || identityCount(t, e, `subject='oldproxy'`) != 1 {
		t.Fatal("legacy proxy account not linked")
	}
}
