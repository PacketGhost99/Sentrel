package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
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
	t.Setenv("SENTREL_RELEASE_KEY", base64.StdEncoding.EncodeToString(pk))
	f := filepath.Join(dir, "SHA256SUMS")
	os.WriteFile(f, []byte("x"), 0o644)
	if err := run([]string{"sign", f}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f + ".sig"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENTREL_RELEASE_KEY", "")
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

func TestOperatorKeygenAndSignJob(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "op.key")
	if err := run([]string{"keygen-operator", "-out", keyFile}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"keygen-operator", "-out", keyFile}); err == nil {
		t.Fatal("overwrote an existing key")
	}
	pubText, err := os.ReadFile(keyFile + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	pubRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pubText)))
	if err != nil || len(pubRaw) != ed25519.PublicKeySize {
		t.Fatalf("public key: %v", err)
	}
	spec := []byte("exact job spec bytes")
	specB64 := base64.StdEncoding.EncodeToString(spec)
	check := func(specArg string) {
		sig, err := signJob(keyFile, specArg)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(sig)
		if !ed25519.Verify(ed25519.PublicKey(pubRaw), spec, raw) {
			t.Fatal("signature does not verify against the public key")
		}
		if ed25519.Verify(ed25519.PublicKey(pubRaw), []byte("other"), raw) {
			t.Fatal("signature verifies for other bytes")
		}
	}
	check(specB64)
	specFile := filepath.Join(dir, "spec.b64")
	os.WriteFile(specFile, []byte(specB64+"\n"), 0o644)
	check(specFile)
	if err := run([]string{"sign-job", "--key", keyFile, "--spec", specB64}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign-job", "--key", keyFile}); err == nil {
		t.Fatal("missing spec accepted")
	}
	if _, err := signJob(keyFile, "!!notbase64"); err == nil {
		t.Fatal("bad spec accepted")
	}
}
