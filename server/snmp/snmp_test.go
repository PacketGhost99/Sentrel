package snmp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinProfilesLoadAndValidate(t *testing.T) {
	ps, err := LoadProfiles("")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"generic", "net-snmp", "cisco", "mikrotik", "ubiquiti", "fortinet", "hp"} {
		if ps[n] == nil {
			t.Errorf("missing profile %s", n)
		}
	}
	// Includes resolve without cycles.
	if ms := expand(ps["ubiquiti"], ps, map[string]bool{}); len(ms) < 6 {
		t.Errorf("ubiquiti expanded to %d metrics", len(ms))
	}
}

func TestSelectProfile(t *testing.T) {
	ps, _ := LoadProfiles("")
	cases := map[string]System{
		"cisco":    {ObjectID: "1.3.6.1.4.1.9.1.2066", Descr: "Cisco IOS Software"},
		"mikrotik": {ObjectID: "1.3.6.1.4.1.14988.1", Descr: "RouterOS CCR2004"},
		"fortinet": {ObjectID: "1.3.6.1.4.1.12356.101.1.3002"},
		"net-snmp": {ObjectID: ".1.3.6.1.4.1.8072.3.2.10", Descr: "Linux nas 6.1"},
		"generic":  {ObjectID: "1.3.6.1.4.1.99999.1", Descr: "Something else"},
	}
	for want, sys := range cases {
		if p := Select(ps, "auto", sys); p == nil || p.Name != want {
			t.Errorf("%v: got %v want %s", sys, p, want)
		}
	}
	if p := Select(ps, "hp", System{}); p.Name != "hp" {
		t.Error("explicit profile ignored")
	}
}

func TestCustomProfileOverridesAndValidation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "acme.yaml"), []byte("name: acme\nmetrics:\n  - name: widgets\n    oid: 1.3.6.1.4.1.5555.1.0\n"), 0o644)
	ps, err := LoadProfiles(dir)
	if err != nil || ps["acme"] == nil || ps["acme"].Source != "custom" {
		t.Fatalf("custom profile: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("name: bad\nmetrics:\n  - name: x\n    expr: \"a +\"\n    vars: {a: 1.3.6.1.2.1.1.3.0}\n"), 0o644)
	if _, err := LoadProfiles(dir); err == nil {
		t.Fatal("invalid expression accepted")
	}
}
