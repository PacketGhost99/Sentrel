package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheck(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "::1", "fe80::1", "fd00::1", "224.0.0.1", "100.64.0.1", "::ffff:127.0.0.1"}
	for _, s := range blocked {
		if err := Strict.Check(net.ParseIP(s)); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s should be blocked, got %v", s, err)
		}
		if err := Allow.Check(net.ParseIP(s)); err != nil {
			t.Errorf("%s should be allowed for the provider: %v", s, err)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if err := Strict.Check(net.ParseIP(s)); err != nil {
			t.Errorf("%s should be allowed: %v", s, err)
		}
	}
}

func TestLoopbackBlockedForTenantAllowedForProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addr := srv.Listener.Addr().String()

	if c, err := Strict.Dialer(2*time.Second).DialContext(ctx, "tcp", addr); err == nil {
		c.Close()
		t.Fatal("tenant dial to loopback succeeded")
	} else if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
	c, err := Allow.Dialer(2*time.Second).DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("provider dial to loopback failed: %v", err)
	}
	c.Close()
}

// A hostname that resolves to 127.0.0.1 must be rejected after resolution.
func TestHostnameResolvingToLoopbackBlocked(t *testing.T) {
	if ips, err := net.LookupIP("localhost"); err != nil || len(ips) == 0 {
		t.Skip("localhost does not resolve")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Strict.Dialer(2*time.Second).DialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err == nil {
		c.Close()
		t.Fatal("dial via hostname to loopback succeeded")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
	cl := &http.Client{Transport: Strict.Transport(&http.Transport{}), Timeout: 3 * time.Second}
	if _, err := cl.Get("http://localhost:" + port + "/"); err == nil {
		t.Fatal("http client reached loopback")
	}
}
