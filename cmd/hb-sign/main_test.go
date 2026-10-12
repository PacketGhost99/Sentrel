package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pk, _ := x509.MarshalPKCS8PrivateKey(priv)
	pb, _ := x509.MarshalPKIXPublicKey(pub)
	keyFile := filepath.Join(dir, "key")
	pubFile := filepath.Join(dir, "pub")
	os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(pk)+"\n"), 0o600)
	os.WriteFile(pubFile, []byte(base64.StdEncoding.EncodeToString(pb)), 0o644)
	sums := filepath.Join(dir, "SHA256SUMS")
	os.WriteFile(sums, []byte("abc  file\n"), 0o644)

	if err := run([]string{"sign", "-key", keyFile, sums}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-pub", pubFile, sums, sums + ".sig"}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Tampered content must fail.
	os.WriteFile(sums, []byte("abd  file\n"), 0o644)
	if err := run([]string{"verify", "-pub", pubFile, sums, sums + ".sig"}); err == nil {
		t.Fatal("tampered file verified")
	}
}

func TestSignFromEnv(t *testing.T) {
	dir := t.TempDir()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pk, _ := x509.MarshalPKCS8PrivateKey(priv)
	t.Setenv("HB_RELEASE_KEY", base64.StdEncoding.EncodeToString(pk))
	f := filepath.Join(dir, "SHA256SUMS")
	os.WriteFile(f, []byte("x"), 0o644)
	if err := run([]string{"sign", f}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f + ".sig"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HB_RELEASE_KEY", "")
	if err := run([]string{"sign", f}); err == nil {
		t.Fatal("empty key accepted")
	}
}

func TestBadArgs(t *testing.T) {
	for _, a := range [][]string{{}, {"nope"}, {"sign"}, {"verify", "-pub", "x"}} {
		if run(a) == nil {
			t.Errorf("%v: expected error", a)
		}
	}
}
