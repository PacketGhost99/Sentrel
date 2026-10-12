package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"

	"heartbeat/internal/sigs"
)

func TestCAIssueAndVerify(t *testing.T) {
	dir := t.TempDir()
	ca, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Reload gives the same CA.
	ca2, err := Load(dir)
	if err != nil || ca2.Fingerprint() != ca.Fingerprint() {
		t.Fatal("CA not persisted")
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored"}}, key)
	certPEM, serial, _, err := ca.SignAgentCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), "agent-123")
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := ParseCertPEM(certPEM)
	id, s, err := ca.VerifyAgentCert(cert)
	if err != nil || id != "agent-123" || s != serial {
		t.Fatalf("verify: %s %s %v", id, s, err)
	}
	// Signed hello round trip.
	msg := sigs.HelloMessage(id, 12345)
	sig, err := Sign(key, msg)
	if err != nil || VerifySignature(cert, msg, sig) != nil {
		t.Fatal("hello signature")
	}
	if VerifySignature(cert, sigs.HelloMessage(id, 12346), sig) == nil {
		t.Fatal("signature not bound to timestamp")
	}
	// A cert from another CA is rejected.
	other, _ := Load(t.TempDir())
	otherPEM, _, _, _ := other.SignAgentCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), "agent-123")
	oc, _ := ParseCertPEM(otherPEM)
	if _, _, err := ca.VerifyAgentCert(oc); err == nil {
		t.Fatal("foreign CA accepted")
	}
	// Tampered CSR is rejected.
	bad := append([]byte{}, der...)
	bad[len(bad)-5] ^= 0xff
	if _, _, _, err := ca.SignAgentCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: bad}), "x"); err == nil {
		t.Fatal("tampered CSR signed")
	}
}

func TestServerCertificateSANs(t *testing.T) {
	ca, _ := Load(t.TempDir())
	c, err := ca.ServerCertificate([]string{"mon.example.com", "10.0.0.5", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Leaf.VerifyHostname("10.0.0.5"); err != nil {
		t.Error(err)
	}
	if err := c.Leaf.VerifyHostname("mon.example.com"); err != nil {
		t.Error(err)
	}
	if _, err := c.Leaf.Verify(x509.VerifyOptions{Roots: ca.Pool(), DNSName: "mon.example.com"}); err != nil {
		t.Error(err)
	}
	// Cached on identical SANs.
	c2, _ := ca.ServerCertificate([]string{"mon.example.com", "localhost", "10.0.0.5"})
	if c2 != c {
		t.Error("certificate regenerated for the same names")
	}
}
