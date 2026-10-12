package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sentrel/server/netguard"
)

func TestStatusOK(t *testing.T) {
	cases := []struct {
		code int
		spec string
		want bool
	}{
		{200, "", true}, {302, "", true}, {404, "", false}, {201, "2xx", true}, {301, "200-299,301", true}, {418, "418", true}, {500, "2xx,3xx", false},
	}
	for _, c := range cases {
		if got := statusOK(c.code, c.spec); got != c.want {
			t.Errorf("%d %q: %v", c.code, c.spec, got)
		}
	}
}

func TestHTTPCheck(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("status: healthy"))
	}))
	defer srv.Close()
	ctx := context.Background()
	if r := HTTP(ctx, netguard.Allow, HTTPConfig{URL: srv.URL, Keyword: "healthy", IgnoreTLS: true}, 5*time.Second); !r.Up || r.Values["cert_days"] <= 0 {
		t.Fatalf("expected up with cert days: %+v", r)
	}
	if r := HTTP(ctx, netguard.Allow, HTTPConfig{URL: srv.URL}, 5*time.Second); r.Up {
		t.Fatal("untrusted certificate accepted without ignore_tls")
	}
	if r := HTTP(ctx, netguard.Allow, HTTPConfig{URL: srv.URL, Keyword: "broken", IgnoreTLS: true}, 5*time.Second); r.Up || !strings.Contains(r.Message, "not found") {
		t.Fatalf("missing keyword: %+v", r)
	}
	if r := HTTP(ctx, netguard.Allow, HTTPConfig{URL: srv.URL, Keyword: "healthy", KeywordAbsent: true, IgnoreTLS: true}, 5*time.Second); r.Up {
		t.Fatal("forbidden keyword not detected")
	}
	if r := HTTP(ctx, netguard.Allow, HTTPConfig{URL: srv.URL + "/fail", IgnoreTLS: true}, 5*time.Second); r.Up {
		t.Fatal("503 treated as up")
	}
}

func TestTCPAndCert(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if r := TCP(context.Background(), netguard.Allow, addr, time.Second); r.Up {
		t.Fatal("closed port reported up")
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	if r := TCP(context.Background(), netguard.Allow, host, time.Second); !r.Up {
		t.Fatalf("open port down: %s", r.Message)
	}
	r := Cert(context.Background(), netguard.Allow, host, "example.com", 2*time.Second, true)
	if !r.Up || r.Values["cert_days"] < 1 {
		t.Fatalf("cert: %+v", r)
	}
	if r := Cert(context.Background(), netguard.Allow, host, "example.com", 2*time.Second, false); r.Up {
		t.Fatal("untrusted chain accepted")
	}
}

func TestTenantProbesBlockedFromLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	ctx := context.Background()
	if r := HTTP(ctx, netguard.Strict, HTTPConfig{URL: srv.URL}, 3*time.Second); r.Up || !strings.Contains(r.Message, "not allowed") {
		t.Fatalf("tenant HTTP to loopback: %+v", r)
	}
	if r := HTTP(ctx, netguard.Allow, HTTPConfig{URL: srv.URL}, 3*time.Second); !r.Up {
		t.Fatalf("provider HTTP to loopback: %+v", r)
	}
	addr := srv.Listener.Addr().String()
	if r := TCP(ctx, netguard.Strict, addr, time.Second); r.Up || !strings.Contains(r.Message, "not allowed") {
		t.Fatalf("tenant TCP to loopback: %+v", r)
	}
	if r := TCP(ctx, netguard.Allow, addr, time.Second); !r.Up {
		t.Fatalf("provider TCP to loopback: %+v", r)
	}
	if r := Cert(ctx, netguard.Strict, addr, "x", time.Second, true); r.Up || !strings.Contains(r.Message, "not allowed") {
		t.Fatalf("tenant cert to loopback: %+v", r)
	}
	if r := Ping(ctx, netguard.Strict, "127.0.0.1", 1, time.Second); r.Up || !strings.Contains(r.Message, "not allowed") {
		t.Fatalf("tenant ping to loopback: %+v", r)
	}
}
