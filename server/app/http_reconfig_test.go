package app

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// tlsServing reports whether addr currently answers a TLS handshake.
func tlsServing(addr string) bool {
	d := &net.Dialer{Timeout: 2 * time.Second}
	c, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func reconfigEnv(t *testing.T) (*testEnv, *httpServer, string) {
	t.Helper()
	e := newTestEnv(t)
	good := freeAddr(t)
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "selfsigned", Listen: good})
	s := newHTTPServer(e.a)
	errCh := make(chan error, 4)
	if err := s.boot(errCh); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.stopServers)
	if !tlsServing(good) || s.Mode() != "selfsigned" {
		t.Fatal("baseline TLS listener is not up")
	}
	return e, s, good
}

func assertStillTLS(t *testing.T, e *testEnv, s *httpServer, good string) {
	t.Helper()
	if s.Mode() != "selfsigned" || !tlsServing(good) {
		t.Fatalf("working TLS listener lost (mode %q)", s.Mode())
	}
	if e.a.cfg.ForceHTTP {
		t.Fatal("ForceHTTP was switched on automatically")
	}
	if got := e.a.settings.TLS(); got.Mode != "selfsigned" || got.Listen != good {
		t.Fatalf("stored settings not restored: %+v", got)
	}
	if !auditContains(t, e, "settings.tls_reload_failed", "stage") {
		t.Fatal("failure not audited")
	}
}

func TestTLSReconfigCertificateFailure(t *testing.T) {
	e, s, good := reconfigEnv(t)
	other := freeAddr(t)
	s.serverCert = func([]string) (*tls.Certificate, error) { return nil, errors.New("cannot load key pair") }
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "selfsigned", Listen: other, Hostname: "new.example"})
	if err := s.reconfigure(make(chan error, 4)); err != nil {
		t.Fatal(err)
	}
	assertStillTLS(t, e, s, good)
	if tlsServing(other) {
		t.Fatal("the rejected configuration came up")
	}
}

func TestTLSReconfigBindFailure(t *testing.T) {
	e, s, good := reconfigEnv(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "selfsigned", Listen: busy.Addr().String()})
	if err := s.reconfigure(make(chan error, 4)); err != nil {
		t.Fatal(err)
	}
	assertStillTLS(t, e, s, good)
}

func TestTLSReconfigInvalidMode(t *testing.T) {
	e, s, good := reconfigEnv(t)
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "acme", Listen: good}) // ACME without a hostname
	if err := s.reconfigure(make(chan error, 4)); err != nil {
		t.Fatal(err)
	}
	assertStillTLS(t, e, s, good)
}

// A failure that only shows up when the listeners start (after validation
// passed) restores the previous TLS listener instead of enabling HTTP.
func TestTLSReconfigStartFailureRestores(t *testing.T) {
	e, s, good := reconfigEnv(t)
	next := freeAddr(t)
	s.failStart = func(lc listenCfg) error {
		if lc.addr == next {
			return errors.New("bind race")
		}
		return nil
	}
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "selfsigned", Listen: next})
	if err := s.reconfigure(make(chan error, 4)); err != nil {
		t.Fatal(err)
	}
	assertStillTLS(t, e, s, good)
	if tlsServing(next) {
		t.Fatal("failed configuration is serving")
	}
}

// When even the restore fails the server fails closed.
func TestTLSReconfigFailsClosed(t *testing.T) {
	e, s, good := reconfigEnv(t)
	next := freeAddr(t)
	s.failStart = func(listenCfg) error { return errors.New("everything is broken") }
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "selfsigned", Listen: next})
	if err := s.reconfigure(make(chan error, 4)); err == nil {
		t.Fatal("expected a fatal error")
	}
	if e.a.cfg.ForceHTTP {
		t.Fatal("ForceHTTP enabled")
	}
	if s.Mode() == "http" {
		t.Fatal("plain HTTP active")
	}
	for _, addr := range []string{good, next} {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			t.Fatalf("something is listening on %s", addr)
		}
	}
}

func TestTLSReconfigSuccess(t *testing.T) {
	e, s, good := reconfigEnv(t)
	next := freeAddr(t)
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "selfsigned", Listen: next})
	if err := s.reconfigure(make(chan error, 4)); err != nil {
		t.Fatal(err)
	}
	if !tlsServing(next) || tlsServing(good) {
		t.Fatal("listener did not move")
	}
	// The new configuration is now the rollback target.
	s.failStart = func(lc listenCfg) error {
		if lc.addr != next {
			return errors.New("fail")
		}
		return nil
	}
	saveSettings(t, e, keyTLS, &TLSSettings{Mode: "selfsigned", Listen: good})
	_ = s.reconfigure(make(chan error, 4))
	if !tlsServing(next) || e.a.settings.TLS().Listen != next {
		t.Fatal("rollback did not return to the last successful configuration")
	}
}
