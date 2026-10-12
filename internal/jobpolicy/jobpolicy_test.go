package jobpolicy

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestClasses(t *testing.T) {
	for _, s := range []string{"refresh_inventory", "collect_now", "disk_usage", "unblock_ip"} {
		if !IsSafe(s) || IsWrite(s) {
			t.Errorf("%s should be safe", s)
		}
	}
	for _, w := range []string{"upgrade", "script", "block_ip", "disk_cleanup", "self_update", "remove_kernel", "", "whatever"} {
		if IsSafe(w) || !IsWrite(w) {
			t.Errorf("%q should be write", w)
		}
	}
}

func TestPolicyAndKeys(t *testing.T) {
	if p, err := NormalizePolicy(""); err != nil || p != PolicyServer {
		t.Fatal(p, err)
	}
	if p, _ := NormalizePolicy("read-only"); p != PolicyNone {
		t.Fatal(p)
	}
	if _, err := NormalizePolicy("bogus"); err == nil {
		t.Fatal("bogus accepted")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b64 := base64.StdEncoding.EncodeToString(pub)
	k, err := ParseKey(b64)
	if err != nil {
		t.Fatal(err)
	}
	spec := []byte("spec")
	sig := ed25519.Sign(priv, spec)
	if id, ok := VerifyOperator([]ed25519.PublicKey{k}, "", spec, sig); !ok || id != KeyID(k) {
		t.Fatal("valid signature rejected")
	}
	if _, ok := VerifyOperator([]ed25519.PublicKey{k}, "deadbeefdeadbeef", spec, sig); ok {
		t.Fatal("wrong key id accepted")
	}
	if _, ok := VerifyOperator([]ed25519.PublicKey{k}, "", []byte("other"), sig); ok {
		t.Fatal("wrong spec accepted")
	}
	if _, err := ParseKey("AAAA"); err == nil {
		t.Fatal("short key accepted")
	}
}
