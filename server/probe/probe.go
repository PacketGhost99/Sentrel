// Package probe runs server-side network checks.
package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"heartbeat/server/netguard"
)

// Result of one probe.
type Result struct {
	Up        bool               `json:"up"`
	Message   string             `json:"message"`
	LatencyMs float64            `json:"latency_ms"`
	Values    map[string]float64 `json:"values,omitempty"` // extra numbers: cert_days, status_code, packet_loss
}

func down(format string, a ...any) Result { return Result{Message: fmt.Sprintf(format, a...)} }

// privileged ping is attempted first; after a permission error we fall back
// to unprivileged (UDP) ICMP for the rest of the process lifetime.
var unprivileged atomic.Bool

// Ping sends count echo requests.
func Ping(ctx context.Context, g netguard.Guard, target string, count int, timeout time.Duration) Result {
	if count <= 0 {
		count = 3
	}
	run := func(priv bool) (*probing.Statistics, error) {
		p, err := probing.NewPinger(target)
		if err != nil {
			return nil, err
		}
		if err := g.Check(p.IPAddr().IP); err != nil {
			return nil, err
		}
		p.SetPrivileged(priv)
		p.Count = count
		p.Interval = 300 * time.Millisecond
		p.Timeout = timeout
		if err := p.RunWithContext(ctx); err != nil {
			return nil, err
		}
		return p.Statistics(), nil
	}
	var st *probing.Statistics
	var err error
	if !unprivileged.Load() {
		st, err = run(true)
		if err != nil && (strings.Contains(err.Error(), "permitted") || strings.Contains(err.Error(), "permission")) {
			unprivileged.Store(true)
		}
	}
	if unprivileged.Load() {
		st, err = run(false)
	}
	if err != nil {
		if strings.Contains(err.Error(), "permitted") || strings.Contains(err.Error(), "permission") {
			return down("ICMP not permitted: grant cap_net_raw to the server binary or widen net.ipv4.ping_group_range")
		}
		return down("%v", err)
	}
	r := Result{Values: map[string]float64{"packet_loss": st.PacketLoss, "packets_received": float64(st.PacketsRecv)}}
	if st.PacketsRecv == 0 {
		r.Message = fmt.Sprintf("no reply from %s (%d sent)", target, st.PacketsSent)
		return r
	}
	r.Up = true
	r.LatencyMs = float64(st.AvgRtt.Microseconds()) / 1000
	r.Message = fmt.Sprintf("%d/%d replies, avg %.1f ms", st.PacketsRecv, st.PacketsSent, r.LatencyMs)
	if st.PacketLoss > 0 {
		r.Message += fmt.Sprintf(", %.0f%% loss", st.PacketLoss)
	}
	return r
}

// TCP checks that a TCP connection can be established.
func TCP(ctx context.Context, g netguard.Guard, addr string, timeout time.Duration) Result {
	start := time.Now()
	d := g.Dialer(timeout)
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return down("connect %s: %v", addr, simplify(err))
	}
	c.Close()
	ms := float64(time.Since(start).Microseconds()) / 1000
	return Result{Up: true, LatencyMs: ms, Message: fmt.Sprintf("connected in %.1f ms", ms)}
}

// HTTPConfig configures an HTTP check.
type HTTPConfig struct {
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	ExpectStatus    string            `json:"expect_status"` // "200", "2xx", "200-299,301"; default 2xx/3xx
	Keyword         string            `json:"keyword"`
	KeywordAbsent   bool              `json:"keyword_absent"` // fail if keyword IS present
	KeywordRegex    bool              `json:"keyword_regex"`
	FollowRedirects *bool             `json:"follow_redirects"`
	IgnoreTLS       bool              `json:"ignore_tls"`
	BasicUser       string            `json:"basic_user"`
	BasicPassword   string            `json:"basic_password"`
	MaxLatencyMs    float64           `json:"max_latency_ms"` // fail if slower
}

// HTTP performs a request and validates status, keyword and latency.
func HTTP(ctx context.Context, g netguard.Guard, cfg HTTPConfig, timeout time.Duration) Result {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return down("invalid URL")
	}
	method := cfg.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if cfg.Body != "" {
		body = strings.NewReader(cfg.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfg.URL, body)
	if err != nil {
		return down("%v", err)
	}
	req.Header.Set("User-Agent", "Heartbeat-Monitor/1")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	if cfg.BasicUser != "" {
		req.SetBasicAuth(cfg.BasicUser, cfg.BasicPassword)
	}
	tr := g.Transport(&http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: cfg.IgnoreTLS},
		DisableKeepAlives: true,
	})
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: timeout}
	if cfg.FollowRedirects != nil && !*cfg.FollowRedirects {
		cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	start := time.Now()
	resp, err := cl.Do(req)
	if err != nil {
		return down("%v", simplify(err))
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	ms := float64(time.Since(start).Microseconds()) / 1000
	r := Result{LatencyMs: ms, Values: map[string]float64{"status_code": float64(resp.StatusCode)}}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		r.Values["cert_days"] = time.Until(resp.TLS.PeerCertificates[0].NotAfter).Hours() / 24
	}
	if !statusOK(resp.StatusCode, cfg.ExpectStatus) {
		r.Message = fmt.Sprintf("HTTP %d (expected %s)", resp.StatusCode, firstNonEmpty(cfg.ExpectStatus, "2xx/3xx"))
		return r
	}
	if cfg.Keyword != "" {
		var found bool
		if cfg.KeywordRegex {
			re, err := regexp.Compile(cfg.Keyword)
			if err != nil {
				r.Message = "invalid keyword regex"
				return r
			}
			found = re.Match(rb)
		} else {
			found = strings.Contains(string(rb), cfg.Keyword)
		}
		if found == cfg.KeywordAbsent {
			if cfg.KeywordAbsent {
				r.Message = fmt.Sprintf("HTTP %d but forbidden keyword %q found", resp.StatusCode, cfg.Keyword)
			} else {
				r.Message = fmt.Sprintf("HTTP %d but keyword %q not found", resp.StatusCode, cfg.Keyword)
			}
			return r
		}
	}
	if cfg.MaxLatencyMs > 0 && ms > cfg.MaxLatencyMs {
		r.Message = fmt.Sprintf("HTTP %d in %.0f ms (slower than %.0f ms)", resp.StatusCode, ms, cfg.MaxLatencyMs)
		return r
	}
	r.Up = true
	r.Message = fmt.Sprintf("HTTP %d in %.0f ms", resp.StatusCode, ms)
	return r
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func statusOK(code int, spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return code >= 200 && code < 400
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		switch {
		case len(part) == 3 && strings.HasSuffix(part, "xx"):
			if code/100 == int(part[0]-'0') {
				return true
			}
		case strings.Contains(part, "-"):
			lo, hi, _ := strings.Cut(part, "-")
			l, e1 := strconv.Atoi(lo)
			h, e2 := strconv.Atoi(hi)
			if e1 == nil && e2 == nil && code >= l && code <= h {
				return true
			}
		default:
			if n, err := strconv.Atoi(part); err == nil && n == code {
				return true
			}
		}
	}
	return false
}

// Cert connects with TLS and reports days until the leaf certificate expires.
// The result is "up" even when expiry is near; severity is decided by the
// caller from Values["cert_days"]. Chain errors are reported as down.
func Cert(ctx context.Context, g netguard.Guard, addr, serverName string, timeout time.Duration, allowInvalid bool) Result {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
		addr = net.JoinHostPort(addr, "443")
	}
	if serverName == "" {
		serverName = host
	}
	d := g.Dialer(timeout)
	start := time.Now()
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
	if err != nil {
		return down("TLS handshake with %s: %v", addr, simplify(err))
	}
	defer conn.Close()
	ms := float64(time.Since(start).Microseconds()) / 1000
	cs := conn.ConnectionState()
	if len(cs.PeerCertificates) == 0 {
		return down("no certificate presented")
	}
	leaf := cs.PeerCertificates[0]
	days := time.Until(leaf.NotAfter).Hours() / 24
	r := Result{LatencyMs: ms, Values: map[string]float64{"cert_days": days}}
	if !allowInvalid {
		inter := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			inter.AddCert(c)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: serverName, Intermediates: inter}); err != nil {
			r.Message = "certificate invalid: " + simplify(err).Error()
			return r
		}
	}
	if days < 0 {
		r.Message = fmt.Sprintf("certificate for %s expired %s ago", serverName, (time.Duration(-days*24) * time.Hour).String())
		return r
	}
	r.Up = true
	r.Message = fmt.Sprintf("certificate for %s expires in %.0f days (%s, issuer %s)", serverName, days, leaf.NotAfter.UTC().Format("2006-01-02"), leaf.Issuer.CommonName)
	return r
}

// DNSConfig configures a DNS check.
type DNSConfig struct {
	RecordType string `json:"record_type"` // A, AAAA, CNAME, MX, TXT, NS
	Server     string `json:"server"`      // optional resolver host:port
	Expect     string `json:"expect"`      // optional value that must be present
}

// DNS resolves a name.
func DNS(ctx context.Context, g netguard.Guard, name string, cfg DNSConfig, timeout time.Duration) Result {
	r := net.DefaultResolver
	if cfg.Server != "" {
		srv := cfg.Server
		if _, _, err := net.SplitHostPort(srv); err != nil {
			srv = net.JoinHostPort(srv, "53")
		}
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := g.Dialer(timeout)
			return d.DialContext(ctx, network, srv)
		}}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	var vals []string
	var err error
	switch strings.ToUpper(cfg.RecordType) {
	case "", "A", "AAAA":
		var ips []net.IP
		network := "ip4"
		if strings.ToUpper(cfg.RecordType) == "AAAA" {
			network = "ip6"
		}
		ips, err = r.LookupIP(ctx, network, name)
		for _, ip := range ips {
			vals = append(vals, ip.String())
		}
	case "CNAME":
		var c string
		c, err = r.LookupCNAME(ctx, name)
		vals = []string{strings.TrimSuffix(c, ".")}
	case "MX":
		var mx []*net.MX
		mx, err = r.LookupMX(ctx, name)
		for _, m := range mx {
			vals = append(vals, strings.TrimSuffix(m.Host, "."))
		}
	case "TXT":
		vals, err = r.LookupTXT(ctx, name)
	case "NS":
		var ns []*net.NS
		ns, err = r.LookupNS(ctx, name)
		for _, n := range ns {
			vals = append(vals, strings.TrimSuffix(n.Host, "."))
		}
	default:
		return down("unsupported record type %s", cfg.RecordType)
	}
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return Result{LatencyMs: ms, Message: simplify(err).Error()}
	}
	if len(vals) == 0 {
		return Result{LatencyMs: ms, Message: "no records"}
	}
	if cfg.Expect != "" {
		ok := false
		for _, v := range vals {
			if strings.EqualFold(strings.TrimSuffix(v, "."), strings.TrimSuffix(cfg.Expect, ".")) || strings.Contains(v, cfg.Expect) {
				ok = true
			}
		}
		if !ok {
			return Result{LatencyMs: ms, Message: fmt.Sprintf("expected %q, got %s", cfg.Expect, strings.Join(vals, ", "))}
		}
	}
	return Result{Up: true, LatencyMs: ms, Message: strings.Join(vals, ", ")}
}

func simplify(err error) error {
	var ne *net.OpError
	if errors.As(err, &ne) && ne.Err != nil {
		return ne.Err
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return simplify(ue.Err)
	}
	return err
}
