package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	pb "sentrel/gen/sentrelv1"
	"sentrel/internal/sigs"
	"sentrel/server/pki"
)

type helloAgent struct {
	id      string
	key     *ecdsa.PrivateKey
	certPEM []byte
	hostID  int64
}

func (e *testEnv) enrolWithKey(admin *client) helloAgent {
	e.t.Helper()
	code, tok, raw := admin.do("POST", "/enrollment-tokens", map[string]any{"ttl_minutes": 15})
	if code != 200 {
		e.t.Fatalf("token: %d %s", code, raw)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "t"}}, key)
	if err != nil {
		e.t.Fatal(err)
	}
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	resp, err := e.a.hub.Register(context.Background(), &pb.RegisterRequest{
		Token: strings.ToLower(tok["token"].(string)), CsrPem: csr, SchemaVersion: 1,
		Host: &pb.HostInfo{Hostname: "h", Os: "linux", Arch: "amd64"},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	h, err := e.a.getHostByAgentID(context.Background(), resp.AgentId)
	if err != nil {
		e.t.Fatal(err)
	}
	return helloAgent{id: resp.AgentId, key: key, certPEM: resp.CertPem, hostID: h.ID}
}

func signedHello(t *testing.T, claimID string, key *ecdsa.PrivateKey, certPEM []byte, ms int64) *pb.Hello {
	t.Helper()
	sig, err := pki.Sign(key, sigs.HelloMessage(claimID, ms))
	if err != nil {
		t.Fatal(err)
	}
	return &pb.Hello{AgentId: claimID, UnixMs: ms, CertPem: certPEM, Signature: sig, SchemaVersion: 1}
}

func (e *testEnv) lastHelloState(id string) (mem int64, persisted int64) {
	e.a.hub.helloMu.Lock()
	mem = e.a.hub.lastHello[id]
	e.a.hub.helloMu.Unlock()
	_ = e.a.db.Get(&persisted, `SELECT COALESCE((SELECT last_ms FROM agent_hello WHERE agent_id=?),0)`, id)
	return
}

func TestHelloFailedAuthDoesNotTouchReplayState(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	victim := e.enrolWithKey(admin)
	other := e.enrolWithKey(admin)
	ctx := context.Background()
	future := time.Now().Add(4 * time.Minute).UnixMilli()

	// Attacker-made self-signed cert claiming the victim's ID.
	akey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: victim.id},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	ader, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &akey.PublicKey, akey)
	apem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ader})

	assertUntouched := func(label string) {
		t.Helper()
		mem, per := e.lastHelloState(victim.id)
		if mem != 0 || per != 0 {
			t.Fatalf("%s: replay state changed (mem=%d persisted=%d)", label, mem, per)
		}
	}

	if _, _, err := e.a.hub.authenticate(ctx, signedHello(t, victim.id, akey, apem, future)); err == nil {
		t.Fatal("forged cert accepted")
	}
	assertUntouched("invalid certificate")

	// A valid certificate of another agent claiming the victim's ID.
	if _, _, err := e.a.hub.authenticate(ctx, signedHello(t, victim.id, other.key, other.certPEM, future)); err == nil {
		t.Fatal("mismatched ID accepted")
	}
	assertUntouched("mismatched id")

	// Revoked host.
	if _, err := e.a.db.Exec(`UPDATE hosts SET revoked=1 WHERE id=?`, victim.hostID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.a.hub.authenticate(ctx, signedHello(t, victim.id, victim.key, victim.certPEM, future)); err == nil {
		t.Fatal("revoked host accepted")
	}
	assertUntouched("revoked")
	if _, err := e.a.db.Exec(`UPDATE hosts SET revoked=0 WHERE id=?`, victim.hostID); err != nil {
		t.Fatal(err)
	}

	// Legitimate hello still succeeds, then a replay is rejected.
	now := time.Now().UnixMilli()
	if _, _, err := e.a.hub.authenticate(ctx, signedHello(t, victim.id, victim.key, victim.certPEM, now)); err != nil {
		t.Fatalf("legitimate hello rejected after failed attempts: %v", err)
	}
	if mem, per := e.lastHelloState(victim.id); mem != now || per != now {
		t.Fatalf("state not recorded: mem=%d persisted=%d", mem, per)
	}
	if _, _, err := e.a.hub.authenticate(ctx, signedHello(t, victim.id, victim.key, victim.certPEM, now)); err == nil || !strings.Contains(err.Error(), "replayed") {
		t.Fatalf("replay not rejected: %v", err)
	}
}
