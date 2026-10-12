package app

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// parseProxyList parses CIDRs or bare IPs. An empty list means loopback only.
func parseProxyList(list []string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
		} else if ip, err := netip.ParseAddr(s); err == nil {
			ip = ip.Unmap()
			out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
		}
	}
	if len(out) == 0 {
		out = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	}
	return out
}

// normalizeProxyList validates the trusted_proxies setting.
func normalizeProxyList(list []string) ([]string, error) {
	var out []string
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked().String())
		} else if ip, err := netip.ParseAddr(s); err == nil {
			out = append(out, ip.Unmap().String())
		} else {
			return nil, badRequest("trusted proxy %q is not an IP address or CIDR range", s)
		}
	}
	return out, nil
}

func inPrefixes(ip netip.Addr, ps []netip.Prefix) bool {
	ip = ip.Unmap()
	for _, p := range ps {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func requestPeer(r *http.Request) (netip.Addr, bool) {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// fromTrustedProxy reports whether forwarding headers on r may be believed:
// trust_proxy is on and the TCP peer is inside trusted_proxies.
func (a *App) fromTrustedProxy(r *http.Request) bool {
	t := a.settings.TLS()
	if !t.TrustProxy {
		return false
	}
	ip, ok := requestPeer(r)
	return ok && inPrefixes(ip, parseProxyList(t.TrustedProxies))
}

// resolveClientIP implements the forwarding-header policy: headers count only
// when the peer is a trusted proxy, and X-Forwarded-For is walked right to
// left, skipping trusted proxies, so entries a client prepends are ignored.
func resolveClientIP(r *http.Request, trustProxy bool, trusted []string) string {
	peer := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peer = h
	}
	pip, ok := requestPeer(r)
	if !trustProxy || !ok {
		return peer
	}
	ps := parseProxyList(trusted)
	if !inPrefixes(pip, ps) {
		return peer
	}
	if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); strings.TrimSpace(xff) != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
			if err != nil {
				return peer // malformed chain: do not guess
			}
			ip = ip.Unmap()
			if !inPrefixes(ip, ps) {
				return ip.String()
			}
		}
		// every hop is a trusted proxy: the leftmost is the best answer
		if ip, err := netip.ParseAddr(strings.TrimSpace(parts[0])); err == nil {
			return ip.Unmap().String()
		}
		return peer
	}
	for _, h := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			if ip, err := netip.ParseAddr(v); err == nil {
				return ip.Unmap().String()
			}
		}
	}
	return peer
}
