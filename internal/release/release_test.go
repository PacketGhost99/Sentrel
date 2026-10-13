//go:build !testkey

package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"strings"
	"testing"
)

func TestSemverOrdering(t *testing.T) {
	// Strictly ascending.
	order := []string{"0.9.0", "0.9.1-dev", "0.9.1-rc.1", "0.9.1-rc.2", "0.9.1-rc.10", "0.9.1-test2", "0.9.1", "0.9.2", "0.10.0", "1.0.0-beta", "1.0.0", "v1.0.1"}
	for i := range order {
		for j := range order {
			a, ok1 := ParseVersion(order[i])
			b, ok2 := ParseVersion(order[j])
			if !ok1 || !ok2 {
				t.Fatalf("parse %q / %q", order[i], order[j])
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("Compare(%s,%s)=%d want %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestSemverParse(t *testing.T) {
	bad := []string{"", "dev", "1", "1.2.3.4", "1..3", "01.2.3", "1.2.3-", "1.2.-x", "a.b.c", "1.2.x", "-1.2.3", "1.2.3-rc..1"}
	for _, s := range bad {
		if _, ok := ParseVersion(s); ok {
			t.Errorf("ParseVersion(%q) should fail", s)
		}
	}
	if v, ok := ParseVersion(" v1.2.3+build5 "); !ok || v.Major != 1 || v.Minor != 2 || v.Patch != 3 || len(v.Pre) != 0 {
		t.Errorf("build metadata not ignored: %+v", v)
	}
	if v, ok := ParseVersion("1.2"); !ok || v.Patch != 0 {
		t.Errorf("short form: %+v", v)
	}
	a, _ := ParseVersion("1.2.3+a")
	b, _ := ParseVersion("1.2.3+b")
	if a.Compare(b) != 0 {
		t.Error("build metadata must not affect precedence")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, cand string
		want      bool
	}{
		{"0.9.0", "0.9.1", true},
		{"0.9.1-dev", "0.9.1", true},
		{"0.9.1-dev", "0.9.0", false},
		{"0.9.1", "0.9.1-dev", false},
		{"0.9.1", "0.9.1", false},
		{"0.9.1-dev", "0.9.1-dev", false},
		{"dev", "0.9.1", false},
		{"0.9.1", "garbage", false},
		{"0.9.0-rc.1", "0.9.0-rc.2", true},
		{"0.9.9", "0.10.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.cand); got != c.want {
			t.Errorf("Newer(%q,%q)=%v want %v", c.cur, c.cand, got, c.want)
		}
	}
	if !IsPrerelease("1.0.0-rc.1") || IsPrerelease("1.0.0") || IsPrerelease("zzz") {
		t.Error("IsPrerelease")
	}
}

func TestProductionKeyParses(t *testing.T) {
	if len(TrustRoot()) != ed25519.PublicKeySize {
		t.Fatal("trust root is not an Ed25519 key")
	}
}

// The fixture in testdata was produced with `sentrel-sign sign` and a
// throwaway key, so this pins the on-disk signature format.
func TestSentrelSignFixture(t *testing.T) {
	read := func(n string) []byte {
		b, err := os.ReadFile("testdata/" + n)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pub, err := ParsePublicKey(string(read("test.pub")))
	if err != nil {
		t.Fatal(err)
	}
	sums, sig, bin := read("SHA256SUMS"), read("SHA256SUMS.sig"), read("sentrel-server-linux-amd64")
	if err := VerifySignature(pub, sums, sig); err != nil {
		t.Fatalf("fixture signature: %v", err)
	}
	asset := ServerAsset("amd64")
	if err := VerifyBundle(pub, sums, sig, asset, SHA256Hex(bin)); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	// The production key must NOT accept the throwaway signature.
	if err := VerifySignature(TrustRoot(), sums, sig); err == nil {
		t.Fatal("production key accepted a foreign signature")
	}
	// Tampered sums.
	if err := VerifySignature(pub, append([]byte("x"), sums...), sig); err == nil {
		t.Fatal("tampered sums accepted")
	}
}

func TestVerifyBundleFailures(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("binary-bytes")
	asset := ServerAsset("amd64")
	sums := []byte(SHA256Hex(bin) + "  " + asset + "\n")
	sig := []byte(Sign(priv, sums) + "\n")
	if err := VerifyBundle(pub, sums, sig, asset, SHA256Hex(bin)); err != nil {
		t.Fatalf("good: %v", err)
	}
	if err := VerifyBundle(pub, sums, sig, asset, SHA256Hex([]byte("other"))); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("hash mismatch: %v", err)
	}
	if err := VerifyBundle(pub, sums, nil, asset, SHA256Hex(bin)); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("missing sig: %v", err)
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyBundle(pub, sums, []byte(Sign(otherPriv, sums)), asset, SHA256Hex(bin)); err == nil {
		t.Error("bad sig accepted")
	}
	if err := VerifyBundle(pub, sums, []byte("!!!notbase64"), asset, SHA256Hex(bin)); err == nil {
		t.Error("garbage sig accepted")
	}
	if err := VerifyBundle(pub, sums, sig, ServerAsset("arm64"), SHA256Hex(bin)); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Errorf("wrong arch: %v", err)
	}
	if err := VerifyBundle(nil, sums, sig, asset, SHA256Hex(bin)); err == nil {
		t.Error("nil key accepted")
	}
}

func TestParseSums(t *testing.T) {
	h := strings.Repeat("ab", 32)
	m := ParseSums([]byte(h + " *bin\r\n" + h + "  other\nshort  x\n\n" + strings.Repeat("zz", 32) + "  nothex\n"))
	if m["bin"] != h || m["other"] != h || len(m) != 2 {
		t.Errorf("%v", m)
	}
}
