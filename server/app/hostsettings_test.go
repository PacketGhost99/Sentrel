package app

import (
	"context"
	"testing"
)

func stubServerAddrs(t *testing.T, addrs ...string) {
	t.Helper()
	serverAddrMu.Lock()
	old, oldCache := serverAddrsFn, serverAddrCache
	serverAddrsFn = func(string) []string { return addrs }
	serverAddrCache = nil
	serverAddrMu.Unlock()
	t.Cleanup(func() {
		serverAddrMu.Lock()
		serverAddrsFn, serverAddrCache = old, oldCache
		serverAddrMu.Unlock()
	})
}

func settingOf(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	s, _ := m["settings"].(map[string]any)
	v, _ := s[key].(map[string]any)
	if v == nil {
		t.Fatalf("no setting %q in %v", key, m["settings"])
	}
	return v
}

func TestHostSettingsInheritance(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	hp := "/hosts/" + itoa(hostID)
	ctx := context.Background()

	m := okDo(t, admin, "GET", hp, nil)
	for key, want := range map[string]bool{"allow_ip_block": true, "allow_cleanup": true, "allow_upgrades": false, "auto_block": false} {
		s := settingOf(t, m, key)
		if s["effective"] != want || s["override"] != nil || s["inherited"] != true || s["global"] != want {
			t.Fatalf("%s: %v", key, s)
		}
		if m[key] != want {
			t.Fatalf("flat %s = %v, want %v", key, m[key], want)
		}
	}

	// Override, then clear with null.
	m = okDo(t, admin, "PUT", hp, map[string]any{"settings": map[string]any{"allow_upgrades": true, "auto_block_minutes": 15, "never_block": []string{"203.0.113.0/24"}}})
	if s := settingOf(t, m, "allow_upgrades"); s["effective"] != true || s["override"] != true || s["inherited"] != false {
		t.Fatalf("allow_upgrades: %v", s)
	}
	if s := settingOf(t, m, "auto_block_minutes"); s["effective"] != float64(15) || s["global"] != float64(60) {
		t.Fatalf("auto_block_minutes: %v", s)
	}
	if m["allow_upgrades"] != true {
		t.Fatal("flat allow_upgrades should be the effective value")
	}
	m = okDo(t, admin, "PUT", hp, map[string]any{"settings": map[string]any{"allow_upgrades": nil, "auto_block_minutes": nil}})
	if s := settingOf(t, m, "allow_upgrades"); s["effective"] != false || s["override"] != nil || s["inherited"] != true {
		t.Fatalf("cleared allow_upgrades: %v", s)
	}
	if s := settingOf(t, m, "auto_block_minutes"); s["inherited"] != true || s["effective"] != float64(60) {
		t.Fatalf("cleared auto_block_minutes: %v", s)
	}
	// Other overrides survive clearing one.
	if s := settingOf(t, m, "never_block"); s["inherited"] != false {
		t.Fatalf("never_block lost: %v", s)
	}

	// A changed global default reaches inheriting hosts only.
	d := e.a.settings.Agent()
	d.AllowCleanup = false
	d.AllowUpgrades = true
	if err := e.a.settings.Save(ctx, 0, keyAgent, d); err != nil {
		t.Fatal(err)
	}
	m = okDo(t, admin, "GET", hp, nil)
	if m["allow_cleanup"] != false || m["allow_upgrades"] != true {
		t.Fatalf("defaults not inherited: cleanup=%v upgrades=%v", m["allow_cleanup"], m["allow_upgrades"])
	}
	okDo(t, admin, "PUT", hp, map[string]any{"settings": map[string]any{"allow_cleanup": true}})
	if h, _ := e.a.getHost(ctx, hostID); !h.AllowCleanup {
		t.Fatal("override ignored")
	}

	// Bad input.
	if code, _, _ := admin.do("PUT", hp, map[string]any{"settings": map[string]any{"nope": true}}); code != 400 {
		t.Fatalf("unknown setting: %d", code)
	}
	if code, _, _ := admin.do("PUT", hp, map[string]any{"settings": map[string]any{"auto_block_minutes": 0}}); code != 400 {
		t.Fatalf("bad range: %d", code)
	}
	if code, _, _ := admin.do("PUT", hp, map[string]any{"settings": map[string]any{"never_block": []string{"nope"}}}); code != 400 {
		t.Fatalf("bad list: %d", code)
	}
}

func TestJobValidationUsesEffectiveSettings(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	hp := "/hosts/" + itoa(hostID)
	cleanup := func(dry bool) int {
		code, _, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "disk_cleanup", "dry_run": dry, "confirm": true, "params": map[string]string{"tasks": "journal"}})
		return code
	}
	upgrade := func() int {
		code, _, _ := admin.do("POST", "/jobs", map[string]any{"host_id": hostID, "type": "upgrade", "confirm": true})
		return code
	}
	// Defaults: cleanup allowed, upgrades denied.
	if code := cleanup(true); code != 201 {
		t.Fatalf("cleanup with default: %d", code)
	}
	if code := upgrade(); code != 403 {
		t.Fatalf("upgrade with default: %d", code)
	}
	// Host overrides win in both directions.
	okDo(t, admin, "PUT", hp, map[string]any{"settings": map[string]any{"allow_cleanup": false, "allow_upgrades": true}})
	if code := cleanup(true); code != 403 {
		t.Fatalf("cleanup with host override off: %d", code)
	}
	if code := upgrade(); code != 201 {
		t.Fatalf("upgrade with host override on: %d", code)
	}
	// Clearing the overrides returns to the defaults.
	okDo(t, admin, "PUT", hp, map[string]any{"settings": map[string]any{"allow_cleanup": nil, "allow_upgrades": nil}})
	if cleanup(true) != 201 || upgrade() != 403 {
		t.Fatal("defaults not restored")
	}
}

func TestNeverBlock(t *testing.T) {
	e := newTestEnv(t)
	stubServerAddrs(t, "198.18.0.1")
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	hp := "/hosts/" + itoa(hostID)
	ctx := context.Background()
	e.a.alerts.refreshHosts(ctx)

	d := e.a.settings.Agent()
	d.NeverBlock = []string{"192.0.2.0/24"}
	if err := e.a.settings.Save(ctx, 0, keyAgent, d); err != nil {
		t.Fatal(err)
	}
	okDo(t, admin, "PUT", hp, map[string]any{"settings": map[string]any{"never_block": []string{"203.0.113.5"}}})

	// Manual block: global entry, host entry and the server's own address are refused.
	for _, ip := range []string{"192.0.2.77", "203.0.113.5", "198.18.0.1"} {
		code, _, raw := admin.do("POST", hp+"/block", map[string]any{"ip": ip, "confirm": true})
		if code != 400 || !contains(string(raw), "never-block list") {
			t.Fatalf("block %s: %d %s", ip, code, raw)
		}
	}
	if code, _, raw := admin.do("POST", hp+"/block", map[string]any{"ip": "203.0.113.99", "confirm": true}); code != 200 && code != 201 {
		t.Fatalf("ordinary block: %d %s", code, raw)
	}
	// The agent-side port-scan allowlist receives the never-block list.
	h, _ := e.a.getHost(ctx, hostID)
	cfg := e.a.agentConfig(h)
	if !hasAll(cfg.PortscanAllowlist, "192.0.2.0/24", "203.0.113.5", "198.18.0.1") {
		t.Fatalf("agent allowlist: %v", cfg.PortscanAllowlist)
	}

	// Auto-block skips them too.
	okDo(t, admin, "PUT", hp, map[string]any{"settings": map[string]any{"auto_block": true}})
	e.pushSecurity(hostID, scanReport("192.0.2.9", "203.0.113.5", "198.18.0.1", "198.51.100.8"))
	var ips []string
	_ = e.a.db.SelectContext(ctx, &ips, `SELECT ip FROM ip_blocks WHERE host_id=? AND created_by='system:auto-block'`, hostID)
	if len(ips) != 1 || ips[0] != "198.51.100.8" {
		t.Fatalf("auto-blocked: %v", ips)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEffectiveSecurityValues(t *testing.T) {
	d := AgentDefaults{AllowIPBlock: true, BruteforceThreshold: 10, BruteforceWindowMin: 5, AutoBlockThreshold: 20, AutoBlockMinutes: 60,
		PortscanThresholdPerMin: 20, NeverBlock: []string{"10.0.0.0/8"}}
	if s := securityFor(nil, d); !s.AllowIPBlock || s.AutoBlock || s.AutoBlockThreshold != 20 || len(s.NeverBlock) != 1 {
		t.Fatalf("defaults: %+v", s)
	}
	off, thr := false, 3
	h := &Host{OvAllowIPBlock: &off, AgentOptions: JSON(mustJSON(AgentOptions{BruteforceThreshold: &thr, NeverBlock: []string{"192.0.2.1"}}))}
	s := securityFor(h, d)
	if s.AllowIPBlock || s.BruteforceThreshold != 3 || s.AutoBlockThreshold != 20 || len(s.NeverBlock) != 2 {
		t.Fatalf("overrides: %+v", s)
	}
}

func TestBruteforceThresholdUsesEffectiveValue(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	hostID, _ := e.enrol(admin)
	ctx := context.Background()
	e.a.alerts.refreshHosts(ctx)
	ref, _ := e.a.alerts.host(hostID)
	eff := e.a.alerts.rules.resolve(ref, "bruteforce", "")
	if eff == nil || eff.Origin != "provider" {
		t.Fatalf("default rule: %+v", eff)
	}
	warn := func() float64 { return *e.a.bruteforceRule(ctx, hostID, eff).Rule.Warn }
	if w := warn(); w != 10 {
		t.Fatalf("global default warn = %v", w)
	}
	d := e.a.settings.Agent()
	d.BruteforceThreshold = 25
	_ = e.a.settings.Save(ctx, 0, keyAgent, d)
	if w := warn(); w != 25 {
		t.Fatalf("inherited warn = %v", w)
	}
	okDo(t, admin, "PUT", "/hosts/"+itoa(hostID), map[string]any{"settings": map[string]any{"bruteforce_threshold": 4}})
	if w := warn(); w != 4 {
		t.Fatalf("host override warn = %v", w)
	}
}
