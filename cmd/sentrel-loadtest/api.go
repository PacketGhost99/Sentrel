package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// apiClient is a minimal REST client: session login plus the two calls the
// tool needs (create an enrolment token, list hosts).
type apiClient struct {
	base, user, pass string
	hc               *http.Client
	mu               sync.Mutex
	csrf             string
}

func newAPI(server, user, pass string, insecure bool) *apiClient {
	jar, _ := cookiejar.New(nil)
	tr := &http.Transport{MaxIdleConnsPerHost: 8, TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}
	return &apiClient{base: strings.TrimRight(server, "/") + "/api/v1", user: user, pass: pass,
		hc: &http.Client{Jar: jar, Transport: tr, Timeout: 60 * time.Second}}
}

func (c *apiClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.mu.Lock()
	req.Header.Set("X-CSRF-Token", c.csrf)
	c.mu.Unlock()
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (c *apiClient) login(ctx context.Context) error {
	var r struct {
		CSRF        string `json:"csrf_token"`
		MFARequired bool   `json:"mfa_required"`
	}
	if err := c.do(ctx, "POST", "/auth/login", map[string]string{"username": c.user, "password": c.pass}, &r); err != nil {
		return err
	}
	if r.MFARequired {
		return fmt.Errorf("account %q requires MFA; use a dedicated test account without it", c.user)
	}
	c.mu.Lock()
	c.csrf = r.CSRF
	c.mu.Unlock()
	return nil
}

func (c *apiClient) newToken(ctx context.Context) (string, error) {
	var r struct {
		Token string `json:"token"`
	}
	err := c.do(ctx, "POST", "/enrollment-tokens", map[string]any{"ttl_minutes": 30}, &r)
	return r.Token, err
}

// timeHosts times a full GET /hosts including reading the body.
func (c *apiClient) timeHosts(ctx context.Context) (time.Duration, int, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/hosts", nil)
	t0 := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, 0, fmt.Errorf("status %s", resp.Status)
	}
	return time.Since(t0), len(b), nil
}

func isLoopback(server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
}
