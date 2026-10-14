package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func mkCert(t *testing.T, cn string, parent *testCert, ca bool, mod func(*x509.Certificate)) *testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
	}
	if ca {
		tmpl.IsCA = true
		tmpl.KeyUsage = x509.KeyUsageCertSign
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"sentrel.example"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("192.0.2.10")}
	}
	if mod != nil {
		mod(tmpl)
	}
	signerCert, signerKey := tmpl, key
	if parent != nil {
		signerCert, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signerCert, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &testCert{cert: c, key: key, der: der}
}

func fp(c *testCert) string {
	s := sha256.Sum256(c.der)
	return hex.EncodeToString(s[:])
}

func raws(cs ...*testCert) [][]byte {
	var out [][]byte
	for _, c := range cs {
		out = append(out, c.der)
	}
	return out
}

func TestPinVerifier(t *testing.T) {
	ca := mkCert(t, "ca", nil, true, nil)
	otherCA := mkCert(t, "other-ca", nil, true, nil)
	leaf := mkCert(t, "leaf", ca, false, nil)
	unrelated := mkCert(t, "evil", otherCA, false, nil)
	expired := mkCert(t, "old", ca, false, func(c *x509.Certificate) {
		c.NotBefore = time.Now().Add(-48 * time.Hour)
		c.NotAfter = time.Now().Add(-24 * time.Hour)
	})
	clientOnly := mkCert(t, "client", ca, false, func(c *x509.Certificate) {
		c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	})
	inter := mkCert(t, "inter", ca, true, nil)
	interLeaf := mkCert(t, "leaf2", inter, false, nil)
	// An intermediate that is not actually signed by the pinned CA.
	rogueInter := mkCert(t, "inter", otherCA, true, nil)
	brokenLeaf := mkCert(t, "leaf3", rogueInter, false, nil)

	cases := []struct {
		name  string
		host  string
		pin   string
		chain [][]byte
		ok    bool
	}{
		{"correct leaf", "sentrel.example", fp(ca), raws(leaf, ca), true},
		{"correct leaf, formatted pin", "sentrel.example", "sha256:" + fp(ca), raws(leaf, ca), true},
		{"correct leaf by IP SAN", "192.0.2.10", fp(ca), raws(leaf, ca), true},
		{"valid intermediate", "sentrel.example", fp(ca), raws(interLeaf, inter, ca), true},
		{"unrelated leaf plus pinned CA", "sentrel.example", fp(ca), raws(unrelated, ca), false},
		{"wrong hostname", "other.example", fp(ca), raws(leaf, ca), false},
		{"wrong IP", "192.0.2.99", fp(ca), raws(leaf, ca), false},
		{"expired leaf", "sentrel.example", fp(ca), raws(expired, ca), false},
		{"client-auth only leaf", "sentrel.example", fp(ca), raws(clientOnly, ca), false},
		{"wrong CA pinned", "sentrel.example", fp(otherCA), raws(leaf, ca), false},
		{"CA not presented", "sentrel.example", fp(ca), raws(leaf), false},
		{"broken intermediate", "sentrel.example", fp(ca), raws(brokenLeaf, rogueInter, ca), false},
		{"no certs", "sentrel.example", fp(ca), nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pinVerifier(tc.pin, tc.host)(tc.chain, nil)
			if tc.ok && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestCAPEMMatchesPin(t *testing.T) {
	ca := mkCert(t, "ca", nil, true, nil)
	other := mkCert(t, "ca2", nil, true, nil)
	p := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.der})
	if !caPEMMatchesPin(p, fp(ca)) {
		t.Fatal("matching CA rejected")
	}
	if caPEMMatchesPin(p, fp(other)) {
		t.Fatal("mismatching CA accepted")
	}
	if caPEMMatchesPin([]byte("junk"), fp(ca)) {
		t.Fatal("junk accepted")
	}
}
