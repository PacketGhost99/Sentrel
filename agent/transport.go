package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// serverTarget parses the server URL into a gRPC dial target.
func serverTarget(raw string) (target string, useTLS bool, host string, err error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" {
		return "", false, "", fmt.Errorf("invalid server URL %q (expected https://host[:port])", raw)
	}
	switch u.Scheme {
	case "https":
		useTLS = true
	case "http":
	default:
		return "", false, "", fmt.Errorf("server URL must start with http:// or https://")
	}
	host = u.Hostname()
	port := u.Port()
	if port == "" {
		port = "80"
		if useTLS {
			port = "443"
		}
	}
	return net.JoinHostPort(host, port), useTLS, host, nil
}

func normalisePin(pin string) string {
	pin = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(pin), "sha256:"))
	return strings.ReplaceAll(pin, ":", "")
}

func certMatchesPin(der []byte, pin string) bool {
	s := sha256.Sum256(der)
	return hex.EncodeToString(s[:]) == pin
}

// caPEMMatchesPin reports whether the first certificate in caPEM has the
// pinned SHA-256 fingerprint.
func caPEMMatchesPin(caPEM []byte, pin string) bool {
	b, _ := pem.Decode(caPEM)
	return b != nil && b.Type == "CERTIFICATE" && certMatchesPin(b.Bytes, normalisePin(pin))
}

// pinVerifier verifies the server chain against a trust pool that holds only
// the certificate with the pinned SHA-256 fingerprint (the internal CA in
// self-signed mode). The presence of the CA in the chain proves nothing: the
// leaf must verify against it, for host (a name or an IP literal), with the
// server-auth usage and the current time.
func pinVerifier(pin, host string) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	pin = normalisePin(pin)
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("server presented no certificate")
		}
		certs := make([]*x509.Certificate, 0, len(rawCerts))
		for _, raw := range rawCerts {
			c, err := x509.ParseCertificate(raw)
			if err != nil {
				return fmt.Errorf("parse server certificate: %w", err)
			}
			certs = append(certs, c)
		}
		roots := x509.NewCertPool()
		pinned := false
		inter := x509.NewCertPool()
		for _, c := range certs {
			if certMatchesPin(c.Raw, pin) {
				roots.AddCert(c)
				pinned = true
			} else {
				inter.AddCert(c)
			}
		}
		if !pinned {
			return errors.New("server certificate chain does not contain the pinned CA (--ca-fingerprint)")
		}
		_, err := certs[0].Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: inter,
			DNSName:       host,
			CurrentTime:   time.Now(),
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		if err != nil {
			return fmt.Errorf("server certificate does not verify against the pinned CA: %w", err)
		}
		return nil
	}
}

// tlsConfig builds the client TLS config. caPEM, when present, is trusted in
// addition to system roots; clientCert is presented for mTLS.
func tlsConfig(host string, caPEM []byte, clientCert *tls.Certificate, pin string) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if len(caPEM) > 0 {
		pool.AppendCertsFromPEM(caPEM)
	}
	cfg := &tls.Config{ServerName: host, RootCAs: pool, MinVersion: tls.VersionTLS12}
	if clientCert != nil {
		cfg.Certificates = []tls.Certificate{*clientCert}
	}
	if pin != "" && len(caPEM) == 0 {
		// Registration with a pinned self-signed CA: normal verification would
		// fail, so verify the pin instead.
		cfg.InsecureSkipVerify = true
		cfg.VerifyPeerCertificate = pinVerifier(pin, host)
	}
	return cfg, nil
}

// dial opens a gRPC connection to the server.
func dial(ctx context.Context, serverURL string, caPEM []byte, clientCert *tls.Certificate, pin string) (*grpc.ClientConn, error) {
	target, useTLS, host, err := serverTarget(serverURL)
	if err != nil {
		return nil, err
	}
	var creds credentials.TransportCredentials
	if useTLS {
		tc, err := tlsConfig(host, caPEM, clientCert, pin)
		if err != nil {
			return nil, err
		}
		creds = credentials.NewTLS(tc)
	} else {
		creds = insecure.NewCredentials()
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 15 * time.Second, PermitWithoutStream: true}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20), grpc.MaxCallSendMsgSize(64<<20)),
		grpc.WithUserAgent("sentrel-agent"),
	}
	// grpc-go honours HTTPS_PROXY / NO_PROXY from the environment.
	return grpc.NewClient(target, opts...)
}

// httpClient returns an HTTP client trusting the same roots as gRPC; used
// for binary downloads.
func (c *Config) httpClient(timeout time.Duration) (*http.Client, error) {
	_, useTLS, host, err := serverTarget(c.ServerURL)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if useTLS {
		ca, _ := os.ReadFile(c.caFile())
		tc, err := tlsConfig(host, ca, nil, "")
		if err != nil {
			return nil, err
		}
		tr.TLSClientConfig = tc
	}
	return &http.Client{Timeout: timeout, Transport: tr}, nil
}
