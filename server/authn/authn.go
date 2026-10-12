// Package authn implements password hashing and external authentication
// backends (LDAP, RADIUS).
package authn

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"golang.org/x/crypto/argon2"
	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

// Argon2id parameters (OWASP 2024 recommendation: m=19MiB,t=2,p=1 minimum).
const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns an encoded argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword verifies pw against an encoded hash in constant time.
func CheckPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is used to equalise timing when a user does not exist.
var dummyHash, _ = HashPassword("timing-equaliser")

// EqualiseTiming burns roughly the time of one password check.
func EqualiseTiming(pw string) { CheckPassword(dummyHash, pw) }

// ---------- LDAP ----------

// LDAPConfig configures LDAP authentication.
type LDAPConfig struct {
	URL          string
	StartTLS     bool
	SkipVerify   bool
	BindDN       string
	BindPassword string
	BaseDN       string
	UserFilter   string // must contain %s
	EmailAttr    string
	NameAttr     string
	GroupAttr    string
}

// LDAPUser is the result of a successful LDAP login.
type LDAPUser struct {
	DN     string
	Email  string
	Name   string
	Groups []string
}

// ErrInvalidCredentials is returned for a wrong username or password.
var ErrInvalidCredentials = errors.New("invalid credentials")

func ldapDial(c LDAPConfig) (*ldap.Conn, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: c.SkipVerify}
	if u := strings.TrimPrefix(strings.TrimPrefix(c.URL, "ldaps://"), "ldap://"); u != "" {
		host, _, err := net.SplitHostPort(u)
		if err != nil {
			host = u
		}
		tlsCfg.ServerName = host
	}
	conn, err := ldap.DialURL(c.URL, ldap.DialWithTLSConfig(tlsCfg), ldap.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}))
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(15 * time.Second)
	if c.StartTLS && strings.HasPrefix(c.URL, "ldap://") {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	return conn, nil
}

// LDAPAuthenticate searches for the user with the service account, then
// binds as the user to verify the password.
func LDAPAuthenticate(c LDAPConfig, username, password string) (*LDAPUser, error) {
	if password == "" {
		return nil, ErrInvalidCredentials // unauthenticated bind would "succeed"
	}
	conn, err := ldapDial(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if c.BindDN != "" {
		if err := conn.Bind(c.BindDN, c.BindPassword); err != nil {
			return nil, fmt.Errorf("service bind: %w", err)
		}
	}
	filter := c.UserFilter
	if !strings.Contains(filter, "%s") {
		return nil, errors.New("LDAP user filter must contain %s")
	}
	filter = strings.ReplaceAll(filter, "%s", ldap.EscapeFilter(username))
	attrs := []string{"dn"}
	for _, a := range []string{c.EmailAttr, c.NameAttr, c.GroupAttr} {
		if a != "" {
			attrs = append(attrs, a)
		}
	}
	sr, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 15, false, filter, attrs, nil))
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if len(sr.Entries) != 1 {
		return nil, ErrInvalidCredentials
	}
	e := sr.Entries[0]
	if err := conn.Bind(e.DN, password); err != nil {
		var le *ldap.Error
		if errors.As(err, &le) && le.ResultCode == ldap.LDAPResultInvalidCredentials {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	u := &LDAPUser{DN: e.DN}
	if c.EmailAttr != "" {
		u.Email = e.GetAttributeValue(c.EmailAttr)
	}
	if c.NameAttr != "" {
		u.Name = e.GetAttributeValue(c.NameAttr)
	}
	if c.GroupAttr != "" {
		u.Groups = e.GetAttributeValues(c.GroupAttr)
	}
	return u, nil
}

// LDAPTest verifies connectivity and the service bind.
func LDAPTest(c LDAPConfig) error {
	conn, err := ldapDial(c)
	if err != nil {
		return err
	}
	defer conn.Close()
	if c.BindDN != "" {
		return conn.Bind(c.BindDN, c.BindPassword)
	}
	return nil
}

// ---------- RADIUS ----------

// RADIUSConfig configures RADIUS authentication.
type RADIUSConfig struct {
	Server        string
	Secret        string
	NASIdentifier string
	Timeout       time.Duration
	RoleAttribute string // Filter-Id or Class
}

// RADIUSResult carries attributes from Access-Accept.
type RADIUSResult struct {
	RoleHint string
}

// RADIUSAuthenticate performs a PAP Access-Request.
func RADIUSAuthenticate(ctx context.Context, c RADIUSConfig, username, password string) (*RADIUSResult, error) {
	if password == "" {
		return nil, ErrInvalidCredentials
	}
	server := c.Server
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "1812")
	}
	p := radius.New(radius.CodeAccessRequest, []byte(c.Secret))
	if err := rfc2865.UserName_SetString(p, username); err != nil {
		return nil, err
	}
	if err := rfc2865.UserPassword_SetString(p, password); err != nil {
		return nil, err
	}
	nas := c.NASIdentifier
	if nas == "" {
		nas = "sentrel"
	}
	_ = rfc2865.NASIdentifier_SetString(p, nas)
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := radius.Client{Retry: time.Second}
	resp, err := client.Exchange(cctx, p, server)
	if err != nil {
		return nil, err
	}
	if resp.Code != radius.CodeAccessAccept {
		return nil, ErrInvalidCredentials
	}
	res := &RADIUSResult{}
	switch strings.ToLower(c.RoleAttribute) {
	case "class":
		res.RoleHint = rfc2865.Class_GetString(resp)
	default:
		res.RoleHint = rfc2865.FilterID_GetString(resp)
	}
	return res, nil
}
