package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	pb "sentrel/gen/sentrelv1"
)

// identity is one simulated agent's enrolment result.
type identity struct {
	AgentID string `json:"agent_id"`
	KeyPEM  string `json:"key_pem"`
	CertPEM string `json:"cert_pem"`

	key *ecdsa.PrivateKey
}

type simConfig struct {
	server   string
	interval time.Duration
	packages int
	insecure bool
	spread   bool
	log      *slog.Logger
	api      *apiClient
	cacheDir string
	prefix   string
}

func (c simConfig) hostname(i int) string { return fmt.Sprintf("%s-%04d", c.prefix, i+1) }

func idPath(dir string, i int) string { return filepath.Join(dir, fmt.Sprintf("agent-%04d.json", i+1)) }

func loadIdentity(dir string, i int) (*identity, error) {
	b, err := os.ReadFile(idPath(dir, i))
	if err != nil {
		return nil, err
	}
	var id identity
	if err := json.Unmarshal(b, &id); err != nil {
		return nil, err
	}
	blk, _ := pem.Decode([]byte(id.KeyPEM))
	if blk == nil {
		return nil, fmt.Errorf("agent %d: bad key", i+1)
	}
	if id.key, err = x509.ParseECPrivateKey(blk.Bytes); err != nil {
		return nil, err
	}
	return &id, nil
}

func saveIdentity(dir string, i int, id *identity) error {
	b, _ := json.Marshal(id)
	return os.WriteFile(idPath(dir, i), b, 0o600)
}

// target parses the server URL into a gRPC target, TLS flag and host name.
func target(server string) (addr string, useTLS bool, host string, err error) {
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || u.Host == "" {
		return "", false, "", fmt.Errorf("invalid server URL %q", server)
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port), u.Scheme == "https", u.Hostname(), nil
}

// dial opens a gRPC connection. srcIP, when set, is the local address to bind.
func dial(cfg simConfig, cert *tls.Certificate, srcIP string) (*grpc.ClientConn, error) {
	addr, useTLS, host, err := target(cfg.server)
	if err != nil {
		return nil, err
	}
	var creds = credentials.TransportCredentials(insecure.NewCredentials())
	if useTLS {
		tc := &tls.Config{ServerName: host, InsecureSkipVerify: cfg.insecure, MinVersion: tls.VersionTLS12}
		if cert != nil {
			tc.Certificates = []tls.Certificate{*cert}
		}
		creds = credentials.NewTLS(tc)
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 15 * time.Second, PermitWithoutStream: true}),
		grpc.WithUserAgent("sentrel-agent"),
	}
	if srcIP != "" {
		d := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(srcIP)}, Timeout: 20 * time.Second}
		opts = append(opts, grpc.WithContextDialer(func(ctx context.Context, a string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp", a)
		}))
	}
	return grpc.NewClient(addr, opts...)
}

// enrolSource picks a loopback source address for the n-th enrolment, ten
// enrolments per address, staying under the server's per-IP limit.
func enrolSource(n int) string {
	g := n / 10
	return fmt.Sprintf("127.%d.%d.1", 1+g/256, g%256)
}

// enrolAll enrols every nil entry of ids, with up to par running at once.
func enrolAll(ctx context.Context, cfg simConfig, ids []*identity, par int) error {
	var todo []int
	for i, id := range ids {
		if id == nil {
			todo = append(todo, i)
		}
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		done     int
	)
	work := make(chan int)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for w := 0; w < par; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range work {
				i := todo[n]
				src := ""
				if cfg.spread {
					src = enrolSource(n)
				}
				id, err := enrolOne(ctx, cfg, i, src)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("enrol %s: %w", cfg.hostname(i), err)
						cancel()
					}
				} else {
					ids[i] = id
					done++
					if done%100 == 0 || done == len(todo) {
						fmt.Printf("  enrolled %d/%d\n", done, len(todo))
					}
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for n := range todo {
		select {
		case work <- n:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	return firstErr
}

func enrolOne(ctx context.Context, cfg simConfig, i int, src string) (*identity, error) {
	tok, err := cfg.api.newToken(ctx)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	name := cfg.hostname(i)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: name}}, key)
	if err != nil {
		return nil, err
	}
	conn, err := dial(cfg, nil, src)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := pb.NewAgentServiceClient(conn).Register(rctx, &pb.RegisterRequest{
		Token: tok, CsrPem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}),
		Host: hostInfo(cfg, i), SchemaVersion: uint32(pb.SchemaVersion_SCHEMA_VERSION_CURRENT), Mode: "agent",
	})
	if err != nil {
		return nil, err
	}
	der, _ := x509.MarshalECPrivateKey(key)
	id := &identity{AgentID: resp.AgentId, CertPEM: string(resp.CertPem), key: key,
		KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))}
	return id, saveIdentity(cfg.cacheDir, i, id)
}
