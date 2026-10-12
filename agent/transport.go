package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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

// pinVerifier accepts a server chain containing a certificate with the
// pinned SHA-256 fingerprint (the internal CA in self-signed mode).
func pinVerifier(pin string) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	pin = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(pin), "sha256:"))
	pin = strings.ReplaceAll(pin, ":", "")
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		for _, c := range rawCerts {
			s := sha256.Sum256(c)
			if hex.EncodeToString(s[:]) == pin {
				return nil
			}
		}
		return errors.New("server certificate chain does not contain the pinned CA (--ca-fingerprint)")
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
		cfg.VerifyPeerCertificate = pinVerifier(pin)
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
		grpc.WithUserAgent("heartbeat-agent"),
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
