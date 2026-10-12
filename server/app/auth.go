package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"

	"heartbeat/server/authn"
	"heartbeat/server/db"
)

const sessionCookie = "hb_session"

type authService struct {
	app      *App
	failures *rateLimiter // username|ip -> failed attempts
	ipFails  *rateLimiter

	waMu    sync.Mutex
	waSess  map[string]waPending
	waCache map[string]*webauthn.WebAuthn
}

type waPending struct {
	data    webauthn.SessionData
	userID  int64
	expires time.Time
	purpose string // register, mfa, login
}

func newAuthService(a *App) *authService {
	sec := a.settings.Security()
	window := time.Duration(sec.LockoutMinutes) * time.Minute
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &authService{
		app:      a,
		failures: newRateLimiter(1<<30, window),
		ipFails:  newRateLimiter(1<<30, window),
		waSess:   map[string]waPending{},
		waCache:  map[string]*webauthn.WebAuthn{},
	}
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// clientIP returns the caller IP, honouring X-Forwarded-For only when the
// server is configured to trust a proxy.
func (a *App) clientIP(r *http.Request) string {
	if a.settings.TLS().TrustProxy {
		if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
			return cf
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return xr
		}
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (a *App) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return a.settings.TLS().TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// middleware authenticates the request (API key or session) and enforces
// CSRF for cookie-authenticated state-changing requests.
func (s *authService) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.app.clientIP(r)
		var p *Principal
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
			var err error
			p, err = s.apiKeyPrincipal(r.Context(), strings.TrimSpace(strings.TrimPrefix(ah, "Bearer ")))
			if err != nil {
				writeError(w, unauthorized("invalid API key"))
				return
			}
		} else if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			p = s.sessionPrincipal(r.Context(), c.Value)
			if p != nil && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(p.CSRF)) != 1 {
					writeError(w, forbidden("missing or invalid CSRF token"))
					return
				}
			}
		}
		if p != nil {
			p.IP = ip
			r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
		}
		next.ServeHTTP(w, r)
	})
}

type sessionRow struct {
	IDHash     string `db:"id_hash"`
	UserID     int64  `db:"user_id"`
	MFAPending int    `db:"mfa_pending"`
	CSRF       string `db:"csrf_token"`
	ExpiresAt  int64  `db:"expires_at"`
	LastSeenAt int64  `db:"last_seen_at"`
}

func (s *authService) sessionPrincipal(ctx context.Context, token string) *Principal {
	a := s.app
	var sr sessionRow
	if err := a.db.GetContext(ctx, &sr, `SELECT id_hash, user_id, mfa_pending, csrf_token, expires_at, last_seen_at FROM sessions WHERE id_hash=?`, hashToken(token)); err != nil {
		return nil
	}
	now := db.Now()
	if sr.ExpiresAt < now {
		_, _ = a.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, sr.IDHash)
		return nil
	}
	u, err := a.getUser(ctx, sr.UserID)
	if err != nil || u.Disabled {
		return nil
	}
	var tenantDisabled bool
	if u.TenantID != 0 {
		_ = a.db.GetContext(ctx, &tenantDisabled, `SELECT disabled FROM tenants WHERE id=?`, u.TenantID)
		if tenantDisabled {
			return nil
		}
	}
	if now-sr.LastSeenAt > 60 {
		hours := a.settings.Security().SessionHours
		if hours <= 0 {
			hours = 12
		}
		_, _ = a.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=?, expires_at=? WHERE id_hash=?`, now, now+int64(hours)*3600, sr.IDHash)
	}
	uid := u.ID
	return &Principal{UserID: &uid, Name: u.Username, Role: u.Role, TenantID: u.TenantID, SessionID: sr.IDHash,
		MFAPending: sr.MFAPending != 0, MFAEnroll: sr.MFAPending == 2, CSRF: sr.CSRF, perms: newPrincipal(u.Role, nil)}
}

// API keys look like hb_<8 char prefix>_<secret>.
func (s *authService) apiKeyPrincipal(ctx context.Context, key string) (*Principal, error) {
	parts := strings.SplitN(key, "_", 3)
	if len(parts) != 3 || parts[0] != "hb" {
		return nil, errors.New("malformed")
	}
	a := s.app
	var k APIKey
	if err := a.db.GetContext(ctx, &k, `SELECT * FROM api_keys WHERE prefix=?`, parts[1]); err != nil {
		authn.EqualiseTiming(key)
		return nil, errors.New("unknown")
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(parts[2])), []byte(k.KeyHash)) != 1 {
		return nil, errors.New("mismatch")
	}
	now := db.Now()
	if k.ExpiresAt != 0 && k.ExpiresAt < now {
		return nil, errors.New("expired")
	}
	u, err := a.getUser(ctx, k.UserID)
	if err != nil || u.Disabled {
		return nil, errors.New("user disabled")
	}
	if now-k.LastUsedAt > 60 {
		_, _ = a.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, now, k.ID)
	}
	uid := u.ID
	return &Principal{UserID: &uid, Name: u.Username + " (key " + k.Name + ")", Role: u.Role, TenantID: u.TenantID, APIKeyID: k.ID,
		perms: newPrincipal(u.Role, k.Scopes)}, nil
}

func (a *App) getUser(ctx context.Context, id int64) (*User, error) {
	var u User
	if err := a.db.GetContext(ctx, &u, `SELECT * FROM users WHERE id=?`, id); err != nil {
		return nil, err
	}
	return &u, nil
}

// createSession issues a session cookie. mfa: 0 complete, 1 second factor
// pending, 2 second-factor enrolment required by policy.
func (s *authService) createSession(w http.ResponseWriter, r *http.Request, u *User, mfa int) (string, error) {
	a := s.app
	tok := randomToken(32)
	csrf := randomToken(24)
	now := db.Now()
	hours := a.settings.Security().SessionHours
	if hours <= 0 {
		hours = 12
	}
	exp := now + int64(hours)*3600
	if mfa == 1 {
		exp = now + 600 // ten minutes to complete the second factor
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO sessions (id_hash, tenant_id, user_id, mfa_pending, csrf_token, ip, user_agent, created_at, expires_at, last_seen_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, hashToken(tok), u.TenantID, u.ID, mfa, csrf, a.clientIP(r), truncateStr(r.UserAgent(), 255), now, exp, now)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: a.isHTTPS(r),
		SameSite: http.SameSiteLaxMode, Expires: time.Unix(exp, 0)})
	return csrf, nil
}

func (s *authService) upgradeSession(ctx context.Context, p *Principal, mfa int) error {
	hours := s.app.settings.Security().SessionHours
	if hours <= 0 {
		hours = 12
	}
	_, err := s.app.db.ExecContext(ctx, `UPDATE sessions SET mfa_pending=?, expires_at=? WHERE id_hash=?`, mfa, db.Now()+int64(hours)*3600, p.SessionID)
	return err
}

// needsMFA reports which second factors a user has.
func (s *authService) mfaMethods(ctx context.Context, u *User) []string {
	var m []string
	if u.TOTPEnabled {
		m = append(m, "totp")
	}
	var n int
	_ = s.app.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM webauthn_credentials WHERE user_id=?`, u.ID)
	if n > 0 {
		m = append(m, "passkey")
	}
	return m
}

func (s *authService) policyRequires2FA(u *User) bool {
	switch s.app.settings.Security().Require2FA {
	case "all":
		return true
	case "admins":
		return u.Role == RoleAdmin || u.Role == RoleTenantAdmin
	}
	return false
}

// --- login ---

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResp struct {
	MFARequired      bool     `json:"mfa_required"`
	MFAMethods       []string `json:"mfa_methods,omitempty"`
	MFASetupRequired bool     `json:"mfa_setup_required"`
	CSRF             string   `json:"csrf_token"`
	User             *User    `json:"user"`
}

func (s *authService) lockoutKey(username, ip string) string {
	return strings.ToLower(username) + "|" + ip
}

func (s *authService) login(c *Ctx) (any, error) {
	a := s.app
	var req loginReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	req.Username = strings.TrimSpace(req.Username)
	ip := a.clientIP(c.R)
	sec := a.settings.Security()
	limit := sec.LockoutAttempts
	if limit <= 0 {
		limit = 8
	}
	if s.failures.Count(s.lockoutKey(req.Username, ip)) >= limit || s.ipFails.Count(ip) >= limit*5 {
		a.audit(c.ctx(), auditEntry{Actor: req.Username, Action: "auth.locked_out", IP: ip})
		return nil, &apiError{Status: 429, Code: "locked_out", Msg: "too many failed attempts; try again later"}
	}
	u, err := s.authenticate(c.ctx(), req.Username, req.Password, ip)
	if err != nil {
		s.failures.Allow(s.lockoutKey(req.Username, ip))
		s.ipFails.Allow(ip)
		a.audit(c.ctx(), auditEntry{Actor: req.Username, Action: "auth.login_failed", IP: ip, Details: map[string]any{"reason": err.Error()}})
		if errors.Is(err, authn.ErrInvalidCredentials) || errors.Is(err, errUserDisabled) {
			return nil, unauthorized("invalid username or password")
		}
		a.log.Warn("login backend error", "user", req.Username, "err", err)
		return nil, unauthorized("invalid username or password")
	}
	s.failures.Reset(s.lockoutKey(req.Username, ip))
	return s.finishPrimaryLogin(c, u)
}

func (s *authService) finishPrimaryLogin(c *Ctx, u *User) (any, error) {
	methods := s.mfaMethods(c.ctx(), u)
	mfa := 0
	if len(methods) > 0 {
		mfa = 1
	} else if s.policyRequires2FA(u) {
		mfa = 2
	}
	csrf, err := s.createSession(c.W, c.R, u, mfa)
	if err != nil {
		return nil, err
	}
	if mfa != 1 {
		s.loginCompleted(c, u, "password")
	}
	return loginResp{MFARequired: mfa == 1, MFAMethods: methods, MFASetupRequired: mfa == 2, CSRF: csrf, User: u}, nil
}

func (s *authService) loginCompleted(c *Ctx, u *User, method string) {
	_, _ = s.app.db.ExecContext(c.ctx(), `UPDATE users SET last_login_at=? WHERE id=?`, db.Now(), u.ID)
	s.app.audit(c.ctx(), auditEntry{TenantID: u.TenantID, UserID: &u.ID, Actor: u.Username, Action: "auth.login", IP: s.app.clientIP(c.R),
		Details: map[string]any{"method": method}})
}

var errUserDisabled = errors.New("user disabled")

// authenticate checks credentials against local users, then LDAP, then
// RADIUS, auto-provisioning external users.
func (s *authService) authenticate(ctx context.Context, username, password, ip string) (*User, error) {
	a := s.app
	var u User
	err := a.db.GetContext(ctx, &u, `SELECT * FROM users WHERE username=?`, username)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if found && u.Disabled {
		authn.EqualiseTiming(password)
		return nil, errUserDisabled
	}
	if found && u.AuthSource == "local" {
		if u.PasswordHash == "" || !authn.CheckPassword(u.PasswordHash, password) {
			return nil, authn.ErrInvalidCredentials
		}
		return &u, nil
	}
	ldapCfg := a.settings.LDAP()
	if ldapCfg.Enabled && (!found || u.AuthSource == "ldap") {
		lu, err := authn.LDAPAuthenticate(ldapConfig(ldapCfg), username, password)
		if err == nil {
			role, tenant, ok := mapLDAPRole(ldapCfg, lu.Groups)
			if found {
				if ok {
					u.Role, u.TenantID = role, tenant
				}
				_, _ = a.db.ExecContext(ctx, `UPDATE users SET email=?, display_name=?, role=?, tenant_id=? WHERE id=?`, lu.Email, lu.Name, u.Role, u.TenantID, u.ID)
				return a.getUser(ctx, u.ID)
			}
			if !ok {
				return nil, errors.New("LDAP user has no mapped role")
			}
			return s.provision(ctx, username, lu.Email, lu.Name, role, tenant, "ldap")
		}
		if found || !errors.Is(err, authn.ErrInvalidCredentials) {
			if !errors.Is(err, authn.ErrInvalidCredentials) {
				a.log.Warn("ldap", "err", err)
			}
			if found {
				return nil, authn.ErrInvalidCredentials
			}
		}
	}
	rc := a.settings.RADIUS()
	if rc.Enabled && (!found || u.AuthSource == "radius") {
		res, err := authn.RADIUSAuthenticate(ctx, authn.RADIUSConfig{Server: rc.Server, Secret: rc.Secret, NASIdentifier: rc.NASIdentifier,
			Timeout: time.Duration(rc.TimeoutSec) * time.Second, RoleAttribute: rc.RoleAttribute}, username, password)
		if err == nil {
			role, tenant := rc.DefaultRole, rc.DefaultTenant
			// Filter-Id / Class may carry "heartbeat:<role>" or just "<role>".
			if hint := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(res.RoleHint)), "heartbeat:"); ValidRole(hint, tenant) {
				role = hint
			}
			if found {
				return &u, nil
			}
			if !ValidRole(role, tenant) {
				return nil, errors.New("RADIUS user has no valid role")
			}
			return s.provision(ctx, username, "", username, role, tenant, "radius")
		}
		if !errors.Is(err, authn.ErrInvalidCredentials) {
			a.log.Warn("radius", "err", err)
		}
	}
	if !found {
		authn.EqualiseTiming(password)
	}
	return nil, authn.ErrInvalidCredentials
}

func ldapConfig(s LDAPSettings) authn.LDAPConfig {
	return authn.LDAPConfig{URL: s.URL, StartTLS: s.StartTLS, SkipVerify: s.SkipVerify, BindDN: s.BindDN, BindPassword: s.BindPassword,
		BaseDN: s.BaseDN, UserFilter: s.UserFilter, EmailAttr: s.EmailAttr, NameAttr: s.NameAttr, GroupAttr: s.GroupAttr}
}

// mapLDAPRole picks the most privileged mapped role.
func mapLDAPRole(s LDAPSettings, groups []string) (string, int64, bool) {
	rank := map[string]int{RoleAdmin: 5, RoleOperator: 4, RoleTenantAdmin: 3, RoleViewer: 2, RoleTenantViewer: 1}
	best, bestT, ok := "", int64(0), false
	for _, m := range s.RoleMapping {
		for _, g := range groups {
			if strings.EqualFold(strings.TrimSpace(g), strings.TrimSpace(m.Group)) && ValidRole(m.Role, m.TenantID) && rank[m.Role] > rank[best] {
				best, bestT, ok = m.Role, m.TenantID, true
			}
		}
	}
	if !ok && s.DefaultRole != "" && ValidRole(s.DefaultRole, s.DefaultTenant) {
		return s.DefaultRole, s.DefaultTenant, true
	}
	return best, bestT, ok
}

func (s *authService) provision(ctx context.Context, username, email, name, role string, tenant int64, source string) (*User, error) {
	res, err := s.app.db.ExecContext(ctx, `INSERT INTO users (tenant_id, username, email, display_name, role, auth_source, created_at) VALUES (?,?,?,?,?,?,?)`,
		tenant, username, email, name, role, source, db.Now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	s.app.audit(ctx, auditEntry{TenantID: tenant, Actor: "system", Action: "user.provisioned", TargetType: "user", TargetID: itoa(id),
		Details: map[string]any{"username": username, "source": source, "role": role}})
	return s.app.getUser(ctx, id)
}

func (s *authService) logout(c *Ctx) (any, error) {
	if c.P != nil && c.P.SessionID != "" {
		_, _ = s.app.db.ExecContext(c.ctx(), `DELETE FROM sessions WHERE id_hash=?`, c.P.SessionID)
	}
	http.SetCookie(c.W, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	return map[string]bool{"ok": true}, nil
}

// --- TOTP ---

func (s *authService) issuer() string { return s.app.settings.Branding(0).ProductName }

type totpSetupResp struct {
	Secret string `json:"secret"`
	URL    string `json:"otpauth_url"`
	QR     string `json:"qr_data_url"`
}

// enrolAllowed rejects enrolment while a second factor still has to be
// verified (otherwise enrolling a new factor would bypass the existing one).
func enrolAllowed(p *Principal) error {
	if p.MFAPending && !p.MFAEnroll {
		return &apiError{Status: 401, Code: "mfa_required", Msg: "verify your existing second factor first"}
	}
	if p.APIKeyID != 0 {
		return forbidden("not available with API keys")
	}
	return nil
}

func verifyAllowed(p *Principal) error {
	if !p.MFAPending || p.MFAEnroll {
		return badRequest("no second factor pending")
	}
	return nil
}

func (s *authService) totpSetup(c *Ctx) (any, error) {
	if err := enrolAllowed(c.P); err != nil {
		return nil, err
	}
	u, err := s.app.getUser(c.ctx(), *c.P.UserID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, conflict("TOTP is already enabled; disable it first")
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: s.issuer(), AccountName: u.Username})
	if err != nil {
		return nil, err
	}
	if _, err := s.app.db.ExecContext(c.ctx(), `UPDATE users SET totp_secret=? WHERE id=?`, key.Secret(), u.ID); err != nil {
		return nil, err
	}
	img, err := key.Image(220, 220)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return totpSetupResp{Secret: key.Secret(), URL: key.URL(), QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())}, nil
}

type codeReq struct {
	Code string `json:"code"`
}

func (s *authService) totpEnable(c *Ctx) (any, error) {
	if err := enrolAllowed(c.P); err != nil {
		return nil, err
	}
	var req codeReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	u, err := s.app.getUser(c.ctx(), *c.P.UserID)
	if err != nil {
		return nil, err
	}
	if u.TOTPSecret == "" || !totp.Validate(strings.ReplaceAll(req.Code, " ", ""), u.TOTPSecret) {
		return nil, badRequest("invalid code; check your device clock")
	}
	codes, hashes := recoveryCodes()
	if _, err := s.app.db.ExecContext(c.ctx(), `UPDATE users SET totp_enabled=1, recovery_codes=? WHERE id=?`, hashes, u.ID); err != nil {
		return nil, err
	}
	if c.P.MFAPending {
		_ = s.upgradeSession(c.ctx(), c.P, 0)
	}
	c.auditT(u.TenantID, "user.totp_enabled", "user", itoa(u.ID), nil)
	return map[string]any{"recovery_codes": codes}, nil
}

func recoveryCodes() ([]string, StrList) {
	var codes []string
	var hashes StrList
	for i := 0; i < 10; i++ {
		code := strings.ToLower(randomCode(5) + "-" + randomCode(5))
		codes = append(codes, code)
		hashes = append(hashes, hashToken(code))
	}
	return codes, hashes
}

type pwReq struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (s *authService) totpDisable(c *Ctx) (any, error) {
	var req pwReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	u, err := s.app.getUser(c.ctx(), *c.P.UserID)
	if err != nil {
		return nil, err
	}
	if !s.verifySecondFactorOrPassword(u, req.Password, req.Code) {
		return nil, badRequest("current password or a valid code is required")
	}
	_, err = s.app.db.ExecContext(c.ctx(), `UPDATE users SET totp_enabled=0, totp_secret='', recovery_codes='[]' WHERE id=?`, u.ID)
	c.auditT(u.TenantID, "user.totp_disabled", "user", itoa(u.ID), nil)
	return nil, err
}

func (s *authService) verifySecondFactorOrPassword(u *User, pw, code string) bool {
	if pw != "" && u.AuthSource == "local" && authn.CheckPassword(u.PasswordHash, pw) {
		return true
	}
	if code != "" && u.TOTPEnabled && totp.Validate(code, u.TOTPSecret) {
		return true
	}
	return false
}

// mfaTOTP completes login with a TOTP or recovery code.
func (s *authService) mfaTOTP(c *Ctx) (any, error) {
	if err := verifyAllowed(c.P); err != nil {
		return nil, err
	}
	var req codeReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	key := "mfa|" + itoa(*c.P.UserID)
	if s.failures.Count(key) >= 10 {
		return nil, &apiError{Status: 429, Code: "locked_out", Msg: "too many attempts; log in again later"}
	}
	u, err := s.app.getUser(c.ctx(), *c.P.UserID)
	if err != nil {
		return nil, err
	}
	code := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(req.Code), " ", ""))
	ok := u.TOTPEnabled && totp.Validate(code, u.TOTPSecret)
	method := "totp"
	if !ok {
		h := hashToken(code)
		for i, rc := range u.RecoveryCodes {
			if subtle.ConstantTimeCompare([]byte(rc), []byte(h)) == 1 {
				rest := append(StrList{}, u.RecoveryCodes[:i]...)
				rest = append(rest, u.RecoveryCodes[i+1:]...)
				_, _ = s.app.db.ExecContext(c.ctx(), `UPDATE users SET recovery_codes=? WHERE id=?`, rest, u.ID)
				ok, method = true, "recovery_code"
				break
			}
		}
	}
	if !ok {
		s.failures.Allow(key)
		s.app.audit(c.ctx(), auditEntry{TenantID: u.TenantID, UserID: &u.ID, Actor: u.Username, Action: "auth.mfa_failed", IP: c.P.IP})
		return nil, unauthorized("invalid code")
	}
	s.failures.Reset(key)
	if err := s.upgradeSession(c.ctx(), c.P, 0); err != nil {
		return nil, err
	}
	s.loginCompleted(c, u, method)
	return loginResp{CSRF: c.P.CSRF, User: u}, nil
}

// --- WebAuthn / passkeys ---

type waUser struct {
	u     *User
	creds []webauthn.Credential
}

func (w *waUser) WebAuthnID() []byte                         { return w.u.WebAuthnID }
func (w *waUser) WebAuthnName() string                       { return w.u.Username }
func (w *waUser) WebAuthnDisplayName() string                { return firstNonEmpty(w.u.DisplayName, w.u.Username) }
func (w *waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *authService) waUser(ctx context.Context, userID int64) (*waUser, error) {
	u, err := s.app.getUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(u.WebAuthnID) == 0 {
		id := make([]byte, 32)
		_, _ = rand.Read(id)
		if _, err := s.app.db.ExecContext(ctx, `UPDATE users SET webauthn_id=? WHERE id=?`, id, u.ID); err != nil {
			return nil, err
		}
		u.WebAuthnID = id
	}
	var rows []struct {
		JSON string `db:"credential_json"`
	}
	_ = s.app.db.SelectContext(ctx, &rows, `SELECT credential_json FROM webauthn_credentials WHERE user_id=?`, u.ID)
	wu := &waUser{u: u}
	for _, r := range rows {
		var cr webauthn.Credential
		if json.Unmarshal([]byte(r.JSON), &cr) == nil {
			wu.creds = append(wu.creds, cr)
		}
	}
	return wu, nil
}

// webAuthn returns a relying party for the request's origin. The RP ID is
// the configured server URL's hostname when set, otherwise the request host.
func (s *authService) webAuthn(r *http.Request) (*webauthn.WebAuthn, error) {
	origin := ""
	if su := s.app.settings.General().ServerURL; su != "" {
		if u, err := url.Parse(su); err == nil && u.Host != "" {
			origin = u.Scheme + "://" + u.Host
		}
	}
	if origin == "" {
		scheme := "http"
		if s.app.isHTTPS(r) {
			scheme = "https"
		}
		origin = scheme + "://" + r.Host
	}
	s.waMu.Lock()
	defer s.waMu.Unlock()
	if w, ok := s.waCache[origin]; ok {
		return w, nil
	}
	u, _ := url.Parse(origin)
	w, err := webauthn.New(&webauthn.Config{RPDisplayName: s.issuer(), RPID: u.Hostname(), RPOrigins: []string{origin}})
	if err != nil {
		return nil, err
	}
	s.waCache[origin] = w
	return w, nil
}

func (s *authService) putWA(p waPending) string {
	id := randomToken(16)
	s.waMu.Lock()
	defer s.waMu.Unlock()
	now := time.Now()
	for k, v := range s.waSess {
		if now.After(v.expires) {
			delete(s.waSess, k)
		}
	}
	p.expires = now.Add(5 * time.Minute)
	s.waSess[id] = p
	return id
}

func (s *authService) takeWA(id, purpose string) (waPending, bool) {
	s.waMu.Lock()
	defer s.waMu.Unlock()
	p, ok := s.waSess[id]
	delete(s.waSess, id)
	if !ok || p.purpose != purpose || time.Now().After(p.expires) {
		return waPending{}, false
	}
	return p, true
}

type waBeginResp struct {
	Challenge string `json:"challenge_id"`
	Options   any    `json:"options"`
}

func (s *authService) passkeyRegisterBegin(c *Ctx) (any, error) {
	if err := enrolAllowed(c.P); err != nil {
		return nil, err
	}
	w, err := s.webAuthn(c.R)
	if err != nil {
		return nil, err
	}
	wu, err := s.waUser(c.ctx(), *c.P.UserID)
	if err != nil {
		return nil, err
	}
	var excl []protocol.CredentialDescriptor
	for _, cr := range wu.creds {
		excl = append(excl, cr.Descriptor())
	}
	opts, sd, err := w.BeginRegistration(wu, webauthn.WithExclusions(excl),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred))
	if err != nil {
		return nil, err
	}
	id := s.putWA(waPending{data: *sd, userID: *c.P.UserID, purpose: "register"})
	return waBeginResp{Challenge: id, Options: opts}, nil
}

func (s *authService) passkeyRegisterFinish(c *Ctx) (any, error) {
	if err := enrolAllowed(c.P); err != nil {
		return nil, err
	}
	p, ok := s.takeWA(c.Q("challenge_id"), "register")
	if !ok || p.userID != *c.P.UserID {
		return nil, badRequest("registration expired; try again")
	}
	w, err := s.webAuthn(c.R)
	if err != nil {
		return nil, err
	}
	wu, err := s.waUser(c.ctx(), p.userID)
	if err != nil {
		return nil, err
	}
	cred, err := w.FinishRegistration(wu, p.data, c.R)
	if err != nil {
		return nil, badRequest("passkey registration failed: %v", protocolErr(err))
	}
	name := strings.TrimSpace(c.Q("name"))
	if name == "" {
		name = "Passkey"
	}
	b, _ := json.Marshal(cred)
	_, err = s.app.db.ExecContext(c.ctx(), `INSERT INTO webauthn_credentials (tenant_id, user_id, name, credential_id, credential_json, created_at) VALUES (?,?,?,?,?,?)`,
		wu.u.TenantID, wu.u.ID, truncateStr(name, 64), cred.ID, string(b), db.Now())
	if err != nil {
		return nil, err
	}
	if c.P.MFAPending {
		_ = s.upgradeSession(c.ctx(), c.P, 0)
	}
	c.auditT(wu.u.TenantID, "user.passkey_added", "user", itoa(wu.u.ID), map[string]any{"name": name})
	return map[string]bool{"ok": true}, nil
}

func protocolErr(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.DevInfo != "" {
		return pe.Details + ": " + pe.DevInfo
	}
	return err.Error()
}

// passkeyMFABegin starts a passkey assertion for a pending MFA session.
func (s *authService) passkeyMFABegin(c *Ctx) (any, error) {
	if err := verifyAllowed(c.P); err != nil {
		return nil, err
	}
	w, err := s.webAuthn(c.R)
	if err != nil {
		return nil, err
	}
	wu, err := s.waUser(c.ctx(), *c.P.UserID)
	if err != nil {
		return nil, err
	}
	if len(wu.creds) == 0 {
		return nil, badRequest("no passkeys registered")
	}
	opts, sd, err := w.BeginLogin(wu)
	if err != nil {
		return nil, err
	}
	return waBeginResp{Challenge: s.putWA(waPending{data: *sd, userID: wu.u.ID, purpose: "mfa"}), Options: opts}, nil
}

func (s *authService) passkeyMFAFinish(c *Ctx) (any, error) {
	p, ok := s.takeWA(c.Q("challenge_id"), "mfa")
	if !ok || verifyAllowed(c.P) != nil || p.userID != *c.P.UserID {
		return nil, badRequest("challenge expired; try again")
	}
	w, err := s.webAuthn(c.R)
	if err != nil {
		return nil, err
	}
	wu, err := s.waUser(c.ctx(), p.userID)
	if err != nil {
		return nil, err
	}
	cred, err := w.FinishLogin(wu, p.data, c.R)
	if err != nil {
		return nil, unauthorized("passkey verification failed")
	}
	s.touchCredential(c.ctx(), cred)
	if err := s.upgradeSession(c.ctx(), c.P, 0); err != nil {
		return nil, err
	}
	s.loginCompleted(c, wu.u, "password+passkey")
	return loginResp{CSRF: c.P.CSRF, User: wu.u}, nil
}

func (s *authService) touchCredential(ctx context.Context, cred *webauthn.Credential) {
	b, _ := json.Marshal(cred)
	_, _ = s.app.db.ExecContext(ctx, `UPDATE webauthn_credentials SET credential_json=?, last_used_at=? WHERE credential_id=?`, string(b), db.Now(), cred.ID)
}

// passkeyLoginBegin starts passwordless (discoverable) login.
func (s *authService) passkeyLoginBegin(c *Ctx) (any, error) {
	w, err := s.webAuthn(c.R)
	if err != nil {
		return nil, err
	}
	opts, sd, err := w.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	return waBeginResp{Challenge: s.putWA(waPending{data: *sd, purpose: "login"}), Options: opts}, nil
}

func (s *authService) passkeyLoginFinish(c *Ctx) (any, error) {
	p, ok := s.takeWA(c.Q("challenge_id"), "login")
	if !ok {
		return nil, badRequest("challenge expired; try again")
	}
	w, err := s.webAuthn(c.R)
	if err != nil {
		return nil, err
	}
	var found *waUser
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		var uid int64
		if err := s.app.db.GetContext(c.ctx(), &uid, `SELECT id FROM users WHERE webauthn_id=?`, userHandle); err != nil {
			return nil, errors.New("unknown user")
		}
		wu, err := s.waUser(c.ctx(), uid)
		if err != nil {
			return nil, err
		}
		found = wu
		return wu, nil
	}
	cred, err := w.FinishDiscoverableLogin(handler, p.data, c.R)
	if err != nil || found == nil {
		s.app.audit(c.ctx(), auditEntry{Actor: "passkey", Action: "auth.login_failed", IP: s.app.clientIP(c.R), Details: map[string]any{"method": "passkey"}})
		return nil, unauthorized("passkey not recognised")
	}
	if found.u.Disabled {
		return nil, unauthorized("account disabled")
	}
	s.touchCredential(c.ctx(), cred)
	csrf, err := s.createSession(c.W, c.R, found.u, 0)
	if err != nil {
		return nil, err
	}
	s.loginCompleted(c, found.u, "passkey")
	return loginResp{CSRF: csrf, User: found.u}, nil
}
