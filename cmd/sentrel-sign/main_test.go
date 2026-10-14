package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
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
	fixed := time.Unix(1_800_000_000, 0)
	now = func() time.Time { return fixed }
	defer func() { now = time.Now }()
	mk := func(s *pb.JobSpec) ([]byte, string) {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return b, base64.StdEncoding.EncodeToString(b)
	}
	spec, specB64 := mk(&pb.JobSpec{Id: "job-1", AgentId: "agent-7", Type: "upgrade", Params: map[string]string{"package": "openssl"},
		IssuedUnix: fixed.Unix() - 1, ExpiresUnix: fixed.Unix() + 3600, DryRun: true, TimeoutSeconds: 120})

	// capture runs the command with injected IO (no network is involved anywhere).
	capture := func(in string, args ...string) (out, errOut string, err error) {
		var o, e bytes.Buffer
		stdin, stdout, stderr = strings.NewReader(in), &o, &e
		defer func() { stdin, stdout, stderr = os.Stdin, os.Stdout, os.Stderr }()
		err = run(args)
		return o.String(), e.String(), err
	}
	verifies := func(sigText string, msg []byte) bool {
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(sigText))
		return ed25519.Verify(ed25519.PublicKey(pubRaw), msg, raw)
	}

	// Confirmed with "yes": every field is shown and the signature covers the exact bytes.
	out, errOut, err := capture("yes\n", "job", "-key", keyFile, "-spec", specB64)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"job-1"`, `"agent-7"`, `"upgrade"`, "WRITE", "dry run:    true", "120 s", `"package" = "openssl"`, "expires:", "issued:"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("summary missing %q:\n%s", want, errOut)
		}
	}
	if !verifies(out, spec) || verifies(out, []byte("other")) {
		t.Fatal("signature does not verify against exactly the decoded bytes")
	}

	// Spec from a file, sign-job alias, -yes.
	specFile := filepath.Join(dir, "spec.b64")
	os.WriteFile(specFile, []byte(specB64+"\n"), 0o644)
	if out, _, err = capture("", "sign-job", "--key", keyFile, "--spec", specFile, "-yes"); err != nil || !verifies(out, spec) {
		t.Fatalf("-yes via file: %v", err)
	}

	// -json output.
	out, _, err = capture("", "job", "-key", keyFile, "-spec", specB64, "-yes", "-json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		JobID     string `json:"job_id"`
		Type      string `json:"type"`
		DryRun    bool   `json:"dry_run"`
		Signature string `json:"signature"`
		KeyID     string `json:"key_id"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil || js.JobID != "job-1" || js.Type != "upgrade" || !js.DryRun || !verifies(js.Signature, spec) || js.KeyID == "" {
		t.Fatalf("json: %v %+v", err, js)
	}

	// Anything but "yes" signs nothing.
	for _, in := range []string{"", "no\n", "y\n", "YES\n"} {
		if out, _, err = capture(in, "job", "-key", keyFile, "-spec", specB64); err == nil || out != "" {
			t.Errorf("input %q: signed without confirmation", in)
		}
	}

	// Rejected specs are never signed, even with -yes.
	_, badUnknown := mk(&pb.JobSpec{Id: "j", AgentId: "a", Type: "script", ExpiresUnix: fixed.Unix() + 60})
	_, badExpired := mk(&pb.JobSpec{Id: "j", AgentId: "a", Type: "upgrade", ExpiresUnix: fixed.Unix() - 1})
	for name, s := range map[string]string{
		"malformed": base64.StdEncoding.EncodeToString([]byte("exact job spec bytes")), "unknown type": badUnknown,
		"expired": badExpired, "not base64": "!!notbase64",
	} {
		if out, _, err = capture("", "job", "-key", keyFile, "-spec", s, "-yes"); err == nil || out != "" {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := run([]string{"job", "-key", keyFile}); err == nil {
		t.Fatal("missing spec accepted")
	}
}
