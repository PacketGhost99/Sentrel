// Package pki implements the server's internal certificate authority, which
// signs agent client certificates and (in self-signed TLS mode) the server's
// own certificate, plus the Ed25519 key that signs jobs and agent binaries.
package pki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AgentCertValidity is how long agent client certificates are valid.
// Certificates are revocable server-side, so a long lifetime avoids a
// renewal dance for hosts that are offline for months.
const AgentCertValidity = 10 * 365 * 24 * time.Hour

// CA holds the internal CA and signing keys.
type CA struct {
	dir     string
	Cert    *x509.Certificate
	CertPEM []byte
	key     *ecdsa.PrivateKey
	JobKey  ed25519.PrivateKey

	mu         sync.Mutex
	serverCert *tls.Certificate
	serverSANs string
}

// Load loads the CA from dir, creating it on first run.
func Load(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ca := &CA{dir: dir}
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		if err := ca.create(certPath, keyPath); err != nil {
			return nil, err
		}
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("pki: corrupt CA files")
	}
	ca.Cert, err = x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	ca.key, err = x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	ca.CertPEM = certPEM

	jobPath := filepath.Join(dir, "job.key")
	if _, err := os.Stat(jobPath); errors.Is(err, os.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		der, _ := x509.MarshalPKCS8PrivateKey(priv)
		if err := os.WriteFile(jobPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			return nil, err
		}
	}
	jb, err := os.ReadFile(jobPath)
	if err != nil {
		return nil, err
	}
	jp, _ := pem.Decode(jb)
	if jp == nil {
		return nil, errors.New("pki: corrupt job key")
	}
	k, err := x509.ParsePKCS8PrivateKey(jp.Bytes)
	if err != nil {
		return nil, err
	}
	var ok bool
	if ca.JobKey, ok = k.(ed25519.PrivateKey); !ok {
		return nil, errors.New("pki: job key is not ed25519")
	}
	return ca, nil
}

func (ca *CA) create(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(),
		Subject:               pkix.Name{CommonName: "Sentrel Internal CA", Organization: []string{"Sentrel"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(20 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func randSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	return n
}

// Fingerprint returns the SHA-256 fingerprint of the CA certificate, which
// agents can pin during registration in self-signed mode.
func (ca *CA) Fingerprint() string {
	return Fingerprint(ca.Cert.Raw)
}

// Fingerprint returns "sha256:<hex>" of DER bytes.
func Fingerprint(der []byte) string {
	s := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(s[:])
}

// Pool returns a cert pool containing only the CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

// JobPublicKey returns the Ed25519 public key agents use to verify jobs.
func (ca *CA) JobPublicKey() ed25519.PublicKey {
	return ca.JobKey.Public().(ed25519.PublicKey)
}

// SignJob signs serialized job bytes.
func (ca *CA) SignJob(b []byte) []byte {
	return ed25519.Sign(ca.JobKey, b)
}

// SignAgentCSR validates a PEM CSR and returns a client certificate whose
// CommonName is agentID.
func (ca *CA) SignAgentCSR(csrPEM []byte, agentID string) (certPEM []byte, serial string, notAfter time.Time, err error) {
	b, _ := pem.Decode(csrPEM)
	if b == nil || b.Type != "CERTIFICATE REQUEST" {
		return nil, "", time.Time{}, errors.New("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(b.Bytes)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", time.Time{}, fmt.Errorf("CSR signature: %w", err)
	}
	switch csr.PublicKey.(type) {
	case *ecdsa.PublicKey, ed25519.PublicKey:
	default:
		return nil, "", time.Time{}, errors.New("CSR key must be ECDSA or Ed25519")
	}
	sn := randSerial()
	notAfter = time.Now().Add(AgentCertValidity)
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: agentID, OrganizationalUnit: []string{"sentrel-agent"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, csr.PublicKey, ca.key)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), sn.Text(16), notAfter, nil
}

// VerifyAgentCert checks that cert chains to the CA and is a client cert.
// It returns the agent ID (CommonName) and hex serial.
func (ca *CA) VerifyAgentCert(cert *x509.Certificate) (agentID, serial string, err error) {
	_, err = cert.Verify(x509.VerifyOptions{
		Roots:     ca.Pool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return "", "", err
	}
	return cert.Subject.CommonName, cert.SerialNumber.Text(16), nil
}

// ParseCertPEM parses the first certificate in PEM data.
func ParseCertPEM(p []byte) (*x509.Certificate, error) {
	b, _ := pem.Decode(p)
	if b == nil || b.Type != "CERTIFICATE" {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(b.Bytes)
}

// VerifySignature verifies sig over msg with the certificate's public key.
func VerifySignature(cert *x509.Certificate, msg, sig []byte) error {
	switch pub := cert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		h := sha256.Sum256(msg)
		if !ecdsa.VerifyASN1(pub, h[:], sig) {
			return errors.New("bad signature")
		}
		return nil
	case ed25519.PublicKey:
		if !ed25519.Verify(pub, msg, sig) {
			return errors.New("bad signature")
		}
		return nil
	}
	return errors.New("unsupported key type")
}

// Sign signs msg with an agent private key in the same scheme VerifySignature
// expects.
func Sign(key crypto.Signer, msg []byte) ([]byte, error) {
	switch key.(type) {
	case ed25519.PrivateKey:
		return key.Sign(rand.Reader, msg, crypto.Hash(0))
	default:
		h := sha256.Sum256(msg)
		return key.Sign(rand.Reader, h[:], crypto.SHA256)
	}
}

// ServerCertificate returns a TLS certificate for the given hostnames/IPs,
// signed by the internal CA. It is cached on disk and regenerated when the
// SAN list changes or it is within 30 days of expiry.
func (ca *CA) ServerCertificate(names []string) (*tls.Certificate, error) {
	names = normaliseNames(names)
	sans := strings.Join(names, ",")
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if ca.serverCert != nil && ca.serverSANs == sans && time.Until(ca.serverCert.Leaf.NotAfter) > 30*24*time.Hour {
		return ca.serverCert, nil
	}
	certPath := filepath.Join(ca.dir, "server.crt")
	keyPath := filepath.Join(ca.dir, "server.key")
	sanPath := filepath.Join(ca.dir, "server.sans")
	if prev, err := os.ReadFile(sanPath); err == nil && string(prev) == sans {
		if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
			c.Leaf, _ = x509.ParseCertificate(c.Certificate[0])
			if c.Leaf != nil && time.Until(c.Leaf.NotAfter) > 30*24*time.Hour {
				ca.serverCert, ca.serverSANs = &c, sans
				return &c, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"Sentrel"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(397 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = append(certPEM, ca.CertPEM...)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	_ = os.WriteFile(sanPath, []byte(sans), 0o644)
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	c.Leaf, _ = x509.ParseCertificate(c.Certificate[0])
	ca.serverCert, ca.serverSANs = &c, sans
	return &c, nil
}

func normaliseNames(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(strings.ToLower(n))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		out = []string{"localhost"}
	}
	first := out[0]
	rest := out[1:]
	sort.Strings(rest)
	return append([]string{first}, rest...)
}
