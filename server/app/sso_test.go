package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIdP is an in-process OpenID Connect provider: discovery, JWKS, an
// authorize step driven by the test, and a token endpoint that checks PKCE.
type fakeIdP struct {
	t        *testing.T
	srv      *httptest.Server
	key      *rsa.PrivateKey
	mu       sync.Mutex
	codes    map[string]fakeCode
	claims   map[string]any // merged into every ID token
	badNonce bool
}

type fakeCode struct {
	nonce, challenge, redirect string
}

const fakeClientID = "sentrel-test"

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, codes: map[string]fakeCode{}, claims: map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	fc, ok := f.codes[r.Form.Get("code")]
	delete(f.codes, r.Form.Get("code"))
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || r.Form.Get("redirect_uri") != fc.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != fc.challenge {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
		return
	}
	nonce := fc.nonce
	if f.badNonce {
		nonce = "something-else"
	}
	claims := map[string]any{"iss": f.srv.URL, "sub": "sub-" + fakeClientID, "aud": fakeClientID, "nonce": nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	if pu, ok := f.claims["preferred_username"].(string); ok {
		claims["sub"] = "sub-" + pu // a distinct, stable subject per test user
	}
	for k, v := range f.claims {
		claims[k] = v
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": raw})
}

// configure points the app at this provider.
func (f *fakeIdP) configure(e *testEnv, admin *client, extra map[string]any) {
	body := map[string]any{"enabled": true, "issuer_url": f.srv.URL, "client_id": fakeClientID, "client_secret": "s3cret",
		"allow_provision": true, "group_role_map": []map[string]any{
			{"group": "hb-admins", "role": "admin", "tenant_id": 0},
			{"group": "hb-viewers", "role": "viewer", "tenant_id": 0}}}
	for k, v := range extra {
		body[k] = v
	}
	if code, _, raw := admin.do("PUT", "/settings/oidc", body); code != 200 {
		e.t.Fatalf("save oidc settings: %d %s", code, raw)
	}
}

// signIn runs start and callback as a browser would and returns the
// callback's Location and a client holding any session cookie.
func (f *fakeIdP) signIn(e *testEnv, mutateState func(string) string) (string, *client) {
	e.t.Helper()
	cl := e.client()
	cl.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := cl.http.Get(e.srv.URL + "/api/v1/auth/oidc/start")
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		e.t.Fatalf("start: %d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), f.srv.URL+"/authorize") {
		e.t.Fatalf("start redirected to %s", loc)
	}
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		e.t.Fatalf("authorize request missing PKCE/state/nonce: %v", q)
	}
	code := randomToken(12)
	f.mu.Lock()
	f.codes[code] = fakeCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	f.mu.Unlock()
	state := q.Get("state")
	if mutateState != nil {
		state = mutateState(state)
	}
	resp, err = cl.http.Get(e.srv.URL + "/api/v1/auth/oidc/callback?code=" + code + "&state=" + url.QueryEscape(state))
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		e.t.Fatalf("callback: %d", resp.StatusCode)
	}
	return resp.Header.Get("Location"), cl
}

func meUser(t *testing.T, cl *client) map[string]any {
	t.Helper()
	code, m, _ := cl.do("GET", "/auth/me", nil)
	if code != 200 {
		return nil
	}
	return m["user"].(map[string]any)
}

func TestOIDCLogin(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	f := newFakeIdP(t)
	f.configure(e, admin, nil)

	// The client secret is redacted on read and kept across partial updates.
	_, s, _ := admin.do("GET", "/settings", nil)
	oc := s["oidc"].(map[string]any)
	if oc["client_secret"] != "" || oc["client_secret_set"] != true {
		t.Fatalf("secret not redacted: %v", oc)
	}
	if e.a.settings.OIDC().ClientSecret != "s3cret" {
		t.Fatal("secret not stored")
	}
	if code, m, _ := e.client().do("GET", "/public/info", nil); code != 200 || m["oidc_enabled"] != true {
		t.Fatalf("public info: %v", m)
	}
	if _, m, _ := admin.do("POST", "/settings/oidc/test", nil); m["ok"] != true || m["callback_url"] != "http://monitor.test/api/v1/auth/oidc/callback" {
		t.Fatalf("oidc test: %v", m)
	}

	t.Run("happy path and role mapping", func(t *testing.T) {
		f.claims = map[string]any{"preferred_username": "alice", "email": "alice@example.com", "name": "Alice", "groups": []string{"hb-viewers", "hb-admins"}}
		loc, cl := f.signIn(e, nil)
		if loc != "/" {
			t.Fatalf("callback redirected to %q", loc)
		}
		u := meUser(t, cl)
		if u == nil || u["username"] != "alice" || u["role"] != "admin" || u["auth_source"] != "oidc" || u["email"] != "alice@example.com" {
			t.Fatalf("unexpected user: %v", u)
		}
		// Later logins re-map the role from the current groups, and a lowered
		// role ends the sessions that still carry the old one.
		adminSession := cl
		f.claims["groups"] = []string{"hb-viewers"}
		if _, cl = f.signIn(e, nil); meUser(t, cl)["role"] != "viewer" {
			t.Fatalf("role not updated: %v", meUser(t, cl))
		}
		if meUser(t, adminSession) != nil {
			t.Fatal("session from before the role was lowered still works")
		}
	})

	t.Run("oidc users cannot use a password", func(t *testing.T) {
		for _, pw := range []string{"", "anything", testPassword} {
			if code, _, _ := e.client().do("POST", "/auth/login", map[string]any{"username": "alice", "password": pw}); code != 401 {
				t.Fatalf("password login for oidc user: %d", code)
			}
		}
	})

	t.Run("bad state", func(t *testing.T) {
		loc, cl := f.signIn(e, func(s string) string { return s + "x" })
		if !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
			t.Fatalf("bad state accepted: %s", loc)
		}
	})

	t.Run("bad nonce", func(t *testing.T) {
		f.badNonce = true
		defer func() { f.badNonce = false }()
		loc, cl := f.signIn(e, nil)
		if !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
			t.Fatalf("bad nonce accepted: %s", loc)
		}
	})

	t.Run("unmapped group denied", func(t *testing.T) {
		f.claims = map[string]any{"preferred_username": "mallory", "groups": []string{"strangers"}}
		loc, cl := f.signIn(e, nil)
		if !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
			t.Fatalf("unmapped user accepted: %s", loc)
		}
		var n int
		_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM users WHERE username='mallory'`)
		if n != 0 {
			t.Fatal("denied user was provisioned")
		}
		// A default role admits unmapped users.
		f.configure(e, admin, map[string]any{"default_role": "viewer"})
		if loc, cl = f.signIn(e, nil); loc != "/" || meUser(t, cl)["role"] != "viewer" {
			t.Fatalf("default role not applied: %s", loc)
		}
		f.configure(e, admin, map[string]any{"default_role": ""})
	})

	t.Run("provisioning can be disabled", func(t *testing.T) {
		f.configure(e, admin, map[string]any{"allow_provision": false})
		f.claims = map[string]any{"preferred_username": "newbie", "groups": []string{"hb-admins"}}
		if loc, _ := f.signIn(e, nil); !strings.Contains(loc, "sso_error") {
			t.Fatalf("provisioned despite allow_provision=false: %s", loc)
		}
		f.configure(e, admin, map[string]any{"allow_provision": true})
	})

	t.Run("cannot take over a local account", func(t *testing.T) {
		f.claims = map[string]any{"preferred_username": "admin", "groups": []string{"hb-admins"}}
		if loc, cl := f.signIn(e, nil); !strings.Contains(loc, "sso_error") || meUser(t, cl) != nil {
			t.Fatalf("local admin hijacked: %s", loc)
		}
	})

	t.Run("verified email required", func(t *testing.T) {
		f.configure(e, admin, map[string]any{"require_verified_email": true})
		f.claims = map[string]any{"preferred_username": "carol", "email": "c@example.com", "email_verified": false, "groups": []string{"hb-viewers"}}
		if loc, _ := f.signIn(e, nil); !strings.Contains(loc, "sso_error") {
			t.Fatalf("unverified email accepted: %s", loc)
		}
		f.claims["email_verified"] = true
		if loc, _ := f.signIn(e, nil); loc != "/" {
			t.Fatalf("verified email refused: %s", loc)
		}
		f.configure(e, admin, map[string]any{"require_verified_email": false})
	})
}

func TestOIDCMFATrust(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	f := newFakeIdP(t)
	f.configure(e, admin, map[string]any{"trust_idp_mfa": true})
	if code, _, raw := admin.do("PUT", "/settings/security", map[string]any{"require_2fa": "all"}); code != 200 {
		t.Fatalf("security: %d %s", code, raw)
	}
	mfaState := func(cl *client) (bool, bool) {
		_, m, _ := cl.do("GET", "/auth/me", nil)
		return m["mfa_pending"] == true, m["mfa_setup_required"] == true
	}
	f.claims = map[string]any{"preferred_username": "dave", "groups": []string{"hb-viewers"}, "amr": []string{"pwd", "otp"}}
	_, cl := f.signIn(e, nil)
	if p, s := mfaState(cl); p || s {
		t.Fatal("IdP-asserted MFA was not trusted")
	}
	f.claims["amr"] = []string{"pwd"}
	_, cl = f.signIn(e, nil)
	if _, s := mfaState(cl); !s {
		t.Fatal("2FA policy not applied without an MFA assertion")
	}
	f.configure(e, admin, map[string]any{"trust_idp_mfa": false})
	f.claims["amr"] = []string{"mfa"}
	_, cl = f.signIn(e, nil)
	if _, s := mfaState(cl); !s {
		t.Fatal("amr trusted although trust_idp_mfa is off")
	}
}

func saveSettings(t *testing.T, e *testEnv, key string, v any) {
	t.Helper()
	if err := e.a.settings.Save(context.Background(), 0, key, v); err != nil {
		t.Fatal(err)
	}
}

func TestForwardAuth(t *testing.T) {
	e := newTestEnv(t)
	e.setupAdmin()
	saveSettings(t, e, keyProxyAuth, &ProxyAuthSettings{Enabled: true, UserHeader: "Remote-User", EmailHeader: "Remote-Email", NameHeader: "Remote-Name",
		GroupsHeader: "Remote-Groups", AllowProvision: true,
		GroupRoleMap: []LDAPRoleMapping{{Group: "ops", Role: "operator"}}})

	get := func(method, path, user, groups string) (int, map[string]any, *http.Client) {
		jar, _ := cookiejar.New(nil)
		hc := &http.Client{Jar: jar}
		req, _ := http.NewRequest(method, e.srv.URL+"/api/v1"+path, nil)
		if user != "" {
			req.Header.Set("Remote-User", user)
			req.Header.Set("Remote-Groups", groups)
			req.Header.Set("Remote-Email", user+"@example.com")
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m, hc
	}

	// Trusted peer (loopback is the default) with trust_proxy on.
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "http", Listen: "0.0.0.0:8080", TrustProxy: true})
	code, m, hc := get("GET", "/auth/me", "bob", "ops, other")
	if code != 200 {
		t.Fatalf("trusted proxy login: %d %v", code, m)
	}
	u := m["user"].(map[string]any)
	if u["username"] != "bob" || u["role"] != "operator" || u["auth_source"] != "proxy" {
		t.Fatalf("unexpected proxy user: %v", u)
	}
	// The session persists without the header.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/auth/me", nil)
	resp, err := hc.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("session cookie not issued: %v %v", err, resp)
	}
	resp.Body.Close()

	// A proxy-provisioned user cannot use a password.
	if code, _, _ := e.client().do("POST", "/auth/login", map[string]any{"username": "bob", "password": "x"}); code != 401 {
		t.Fatalf("password login for proxy user: %d", code)
	}
	// Unmapped groups are refused.
	if code, _, _ := get("GET", "/auth/me", "eve", "nobody"); code != 401 {
		t.Fatalf("unmapped proxy user: %d", code)
	}
	// Writes never auto-login.
	if code, _, _ := get("POST", "/auth/logout", "bob", "ops"); code != 401 {
		t.Fatalf("POST authenticated by header: %d", code)
	}

	// Peer outside the trusted list: header ignored.
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "http", Listen: "0.0.0.0:8080", TrustProxy: true, TrustedProxies: []string{"10.0.0.0/8"}})
	if code, _, _ := get("GET", "/auth/me", "mallory", "ops"); code != 401 {
		t.Fatalf("untrusted peer accepted: %d", code)
	}
	// trust_proxy off: also ignored.
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "http", Listen: "0.0.0.0:8080"})
	if code, _, _ := get("GET", "/auth/me", "mallory", "ops"); code != 401 {
		t.Fatalf("header honoured without trust_proxy: %d", code)
	}
	var n int
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM users WHERE username='mallory'`)
	if n != 0 {
		t.Fatal("untrusted header provisioned a user")
	}
}
