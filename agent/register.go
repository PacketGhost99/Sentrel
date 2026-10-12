package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	pb "heartbeat/gen/heartbeatv1"
	"heartbeat/internal/jobpolicy"
)

// RegisterOptions are the inputs to Register.
type RegisterOptions struct {
	ServerURL     string
	Token         string
	ConfigPath    string
	StateDir      string
	CAFingerprint string
	Name          string
	WriteJobs     string   // server (default), operator or none
	OperatorKeys  []string // base64 Ed25519 public keys for the operator policy
}

// Register enrols this host: it generates a key pair, sends a CSR with the
// one-time token and stores the signed certificate.
func Register(ctx context.Context, o RegisterOptions) (*Config, error) {
	if o.ConfigPath == "" {
		o.ConfigPath = DefaultConfigPath()
	}
	if o.StateDir == "" {
		o.StateDir = DefaultStateDir()
	}
	policy, err := jobpolicy.NormalizePolicy(o.WriteJobs)
	if err != nil {
		return nil, err
	}
	if _, err := jobpolicy.ParseKeys(o.OperatorKeys); err != nil {
		return nil, err
	}
	if policy == jobpolicy.PolicyOperator && len(o.OperatorKeys) == 0 {
		return nil, errors.New("write-jobs operator needs at least one --operator-key")
	}
	if err := os.MkdirAll(o.StateDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(o.ConfigPath), 0o750); err != nil {
		return nil, err
	}
	// Windows: lock the folders down before any key material is written.
	if err := hardenStateDirs(o.StateDir, o.ConfigPath); err != nil {
		return nil, fmt.Errorf("protect state directory: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: hostname}}, key)
	if err != nil {
		return nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	conn, err := dial(ctx, o.ServerURL, nil, nil, o.CAFingerprint)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	info := CollectHostInfo(ctx)
	if o.Name != "" {
		info.Hostname = o.Name
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := pb.NewAgentServiceClient(conn).Register(rctx, &pb.RegisterRequest{
		Token: o.Token, CsrPem: csrPEM, Host: info, SchemaVersion: uint32(pb.SchemaVersion_SCHEMA_VERSION_CURRENT), Mode: "agent",
	})
	if err != nil {
		return nil, fmt.Errorf("registration failed: %w", friendlyErr(err))
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	cfg := &Config{ServerURL: o.ServerURL, AgentID: resp.AgentId, StateDir: o.StateDir, CAFingerprint: o.CAFingerprint, Name: o.Name, WriteJobs: policy}
	if policy == jobpolicy.PolicyServer {
		cfg.WriteJobs = ""
	}
	if policy == jobpolicy.PolicyOperator {
		cfg.OperatorKeys = o.OperatorKeys
	}
	if err := os.WriteFile(cfg.keyFile(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfg.certFile(), resp.CertPem, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfg.caFile(), resp.CaPem, 0o644); err != nil {
		return nil, err
	}
	if err := cfg.Save(o.ConfigPath); err != nil {
		return nil, err
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "HEARTBEAT JOB KEY", Bytes: resp.JobPublicKey})
	if err := os.WriteFile(filepath.Join(filepath.Dir(o.ConfigPath), "job.pub"), pub, 0o644); err != nil {
		return nil, err
	}
	_ = hardenStateDirs(o.StateDir, o.ConfigPath) // cover files created above
	return cfg, nil
}
