package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(remote string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestResolveClientIP(t *testing.T) {
	cases := []struct {
		name    string
		r       *http.Request
		trust   bool
		proxies []string
		want    string
	}{
		{"trust off ignores headers", req("203.0.113.9:1234", map[string]string{"X-Forwarded-For": "1.2.3.4"}), false, nil, "203.0.113.9"},
		{"untrusted peer ignores headers", req("203.0.113.9:1234", map[string]string{"X-Forwarded-For": "1.2.3.4", "CF-Connecting-IP": "5.6.7.8", "X-Real-IP": "9.9.9.9"}), true, nil, "203.0.113.9"},
		{"loopback default honours xff", req("127.0.0.1:5000", map[string]string{"X-Forwarded-For": "198.51.100.7"}), true, nil, "198.51.100.7"},
		{"spoofed leftmost entry ignored", req("127.0.0.1:5000", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7"}), true, nil, "198.51.100.7"},
		{"walk skips trusted hops", req("10.0.0.2:5000", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7, 10.0.0.9"}), true, []string{"10.0.0.0/8"}, "198.51.100.7"},
		{"malformed chain falls back to peer", req("127.0.0.1:5000", map[string]string{"X-Forwarded-For": "198.51.100.7, bogus"}), true, nil, "127.0.0.1"},
		{"cf header from trusted peer without xff", req("127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "198.51.100.8"}), true, nil, "198.51.100.8"},
		{"real ip fallback", req("127.0.0.1:5000", map[string]string{"X-Real-IP": "198.51.100.9"}), true, nil, "198.51.100.9"},
		{"ipv6 peer", req("[::1]:5000", map[string]string{"X-Forwarded-For": "2001:db8::5"}), true, nil, "2001:db8::5"},
		{"custom list excludes loopback", req("127.0.0.1:5000", map[string]string{"X-Forwarded-For": "198.51.100.7"}), true, []string{"192.168.0.0/16"}, "127.0.0.1"},
	}
	for _, c := range cases {
		if got := resolveClientIP(c.r, c.trust, c.proxies); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestNormalizeProxyList(t *testing.T) {
	got, err := normalizeProxyList([]string{" 10.1.2.3/8 ", "192.0.2.1", ""})
	if err != nil || len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "192.0.2.1" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := normalizeProxyList([]string{"nope"}); err == nil {
		t.Fatal("expected error")
	}
}
