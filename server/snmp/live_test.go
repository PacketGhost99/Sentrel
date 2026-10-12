package snmp

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestPollLive polls a real SNMP agent. Set HB_SNMP_ADDR (and optionally
// HB_SNMP_COMMUNITY, HB_SNMP_V3_USER/AUTH/PRIV) to run it, e.g. against a
// local net-snmp snmpd.
func TestPollLive(t *testing.T) {
	addr := os.Getenv("HB_SNMP_ADDR")
	if addr == "" {
		t.Skip("HB_SNMP_ADDR not set")
	}
	ps, err := LoadProfiles("")
	if err != nil {
		t.Fatal(err)
	}
	cfgs := map[string]Config{"v2c": {Version: "2c", Community: os.Getenv("HB_SNMP_COMMUNITY"), Profile: "auto", TimeoutSec: 3}}
	if u := os.Getenv("HB_SNMP_V3_USER"); u != "" {
		cfgs["v3"] = Config{Version: "3", Username: u, SecurityLevel: "authPriv", AuthProtocol: "SHA256", AuthPassword: os.Getenv("HB_SNMP_V3_AUTH"),
			PrivProtocol: "AES", PrivPassword: os.Getenv("HB_SNMP_V3_PRIV"), Profile: "auto", TimeoutSec: 3}
	}
	for name, cfg := range cfgs {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		st := NewState()
		if _, err := Poll(ctx, addr, cfg, ps, st); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		time.Sleep(2 * time.Second) // counter metrics need a second sample
		r, err := Poll(ctx, addr, cfg, ps, st)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := map[string]bool{}
		for _, m := range r.Metrics {
			got[m.Name] = true
		}
		t.Logf("%s: profile=%s sys=%q ifaces=%d metrics=%v errors=%v", name, r.Profile, r.System.Name, len(r.Interfaces), got, r.Errors)
		if r.System.Name == "" || len(r.Interfaces) == 0 {
			t.Errorf("%s: missing system or interfaces", name)
		}
		for _, want := range []string{"cpu_usage_percent", "mem_used_percent", "disk_used_percent"} {
			if !got[want] {
				t.Errorf("%s: metric %s missing", name, want)
			}
		}
	}
	// Wrong credentials fail cleanly.
	bad := Config{Version: "2c", Community: "definitely-wrong", TimeoutSec: 1}
	if _, err := Poll(context.Background(), addr, bad, ps, nil); err == nil {
		t.Error("wrong community accepted")
	}
}
