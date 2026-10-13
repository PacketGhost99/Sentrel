package app

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Per-host security and permission settings inherit the global defaults
// (Settings -> Agents). A host stores an override only where it differs; a
// NULL column or an absent agent_options key means "inherit". Everything that
// enforces a setting reads the effective value from hostSecurity.

// hostSec is the effective security configuration of one host.
type hostSec struct {
	AllowIPBlock        bool
	AutoBlock           bool
	AllowCleanup        bool
	AllowUpgrades       bool
	BruteforceEnabled   bool
	PortscanEnabled     bool
	BruteforceThreshold int
	BruteforceWindowMin int
	AutoBlockThreshold  int
	AutoBlockMinutes    int
	PortscanThreshold   int
	NeverBlock          []string // global plus host additions, without server addresses
}

func hostOptions(h *Host) AgentOptions {
	var o AgentOptions
	if h != nil && len(h.AgentOptions) > 0 {
		_ = json.Unmarshal(h.AgentOptions, &o)
	}
	return o
}

func pickB(ov *bool, global bool) bool {
	if ov != nil {
		return *ov
	}
	return global
}

func pickI(ov *int, global int) int {
	if ov != nil {
		return *ov
	}
	return global
}

func mergeLists(a, b []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range [][]string{a, b} {
		for _, e := range l {
			e = strings.TrimSpace(e)
			if e != "" && !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out
}

// foldLegacyNeverBlock merges the pre-inheritance auto_block_whitelist into
// never_block.
func foldLegacyNeverBlock(d *AgentDefaults) {
	if len(d.AutoBlockWhitelist) > 0 {
		d.NeverBlock = mergeLists(d.NeverBlock, d.AutoBlockWhitelist)
		d.AutoBlockWhitelist = nil
	}
}

// securityFor resolves a host against the given defaults (h may be nil).
func securityFor(h *Host, d AgentDefaults) hostSec {
	o := hostOptions(h)
	s := hostSec{
		AllowIPBlock: d.AllowIPBlock, AutoBlock: d.AutoBlock, AllowCleanup: d.AllowCleanup, AllowUpgrades: d.AllowUpgrades,
		BruteforceEnabled:   pickB(o.BruteforceEnabled, d.BruteforceEnabled),
		PortscanEnabled:     pickB(o.PortscanEnabled, d.PortscanEnabled),
		BruteforceThreshold: pickI(o.BruteforceThreshold, d.BruteforceThreshold),
		BruteforceWindowMin: pickI(o.BruteforceWindowMin, d.BruteforceWindowMin),
		AutoBlockThreshold:  pickI(o.AutoBlockThreshold, d.AutoBlockThreshold),
		AutoBlockMinutes:    pickI(o.AutoBlockMinutes, d.AutoBlockMinutes),
		PortscanThreshold:   pickI(o.PortscanThresholdPerMin, d.PortscanThresholdPerMin),
		NeverBlock:          mergeLists(d.NeverBlock, o.NeverBlock),
	}
	if h != nil {
		s.AllowIPBlock = pickB(h.OvAllowIPBlock, s.AllowIPBlock)
		s.AutoBlock = pickB(h.OvAutoBlock, s.AutoBlock)
		s.AllowCleanup = pickB(h.OvAllowCleanup, s.AllowCleanup)
		s.AllowUpgrades = pickB(h.OvAllowUpgrades, s.AllowUpgrades)
	}
	if s.BruteforceThreshold < 1 {
		s.BruteforceThreshold = 10
	}
	if s.BruteforceWindowMin < 1 {
		s.BruteforceWindowMin = 5
	}
	if s.AutoBlockMinutes < 1 {
		s.AutoBlockMinutes = 60
	}
	if s.PortscanThreshold < 1 {
		s.PortscanThreshold = 20
	}
	return s
}

func (a *App) hostSecurity(h *Host) hostSec { return securityFor(h, a.settings.Agent()) }

// resolveHost fills the flat effective booleans of a loaded host.
func (a *App) resolveHost(h *Host) {
	s := a.hostSecurity(h)
	h.AllowIPBlock, h.AutoBlock, h.AllowCleanup, h.AllowUpgrades = s.AllowIPBlock, s.AutoBlock, s.AllowCleanup, s.AllowUpgrades
}

// HostSetting is one setting as the API reports it: the effective value, the
// host's own override (null when inherited) and the global default.
type HostSetting struct {
	Effective any  `json:"effective"`
	Override  any  `json:"override"`
	Global    any  `json:"global"`
	Inherited bool `json:"inherited"`
}

type settingDef struct {
	key  string
	kind string // bool, int or list
	// global reads the default, override the host's override (nil = inherit),
	// effective the resolved value.
	global    func(d AgentDefaults) any
	override  func(h *Host, o *AgentOptions) any
	effective func(s hostSec) any
}

func ovB(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}

func ovI(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

var hostSettingDefs = []settingDef{
	{"allow_ip_block", "bool", func(d AgentDefaults) any { return d.AllowIPBlock }, func(h *Host, _ *AgentOptions) any { return ovB(h.OvAllowIPBlock) }, func(s hostSec) any { return s.AllowIPBlock }},
	{"auto_block", "bool", func(d AgentDefaults) any { return d.AutoBlock }, func(h *Host, _ *AgentOptions) any { return ovB(h.OvAutoBlock) }, func(s hostSec) any { return s.AutoBlock }},
	{"allow_cleanup", "bool", func(d AgentDefaults) any { return d.AllowCleanup }, func(h *Host, _ *AgentOptions) any { return ovB(h.OvAllowCleanup) }, func(s hostSec) any { return s.AllowCleanup }},
	{"allow_upgrades", "bool", func(d AgentDefaults) any { return d.AllowUpgrades }, func(h *Host, _ *AgentOptions) any { return ovB(h.OvAllowUpgrades) }, func(s hostSec) any { return s.AllowUpgrades }},
	{"bruteforce_enabled", "bool", func(d AgentDefaults) any { return d.BruteforceEnabled }, func(_ *Host, o *AgentOptions) any { return ovB(o.BruteforceEnabled) }, func(s hostSec) any { return s.BruteforceEnabled }},
	{"bruteforce_threshold", "int", func(d AgentDefaults) any { return d.BruteforceThreshold }, func(_ *Host, o *AgentOptions) any { return ovI(o.BruteforceThreshold) }, func(s hostSec) any { return s.BruteforceThreshold }},
	{"bruteforce_window_minutes", "int", func(d AgentDefaults) any { return d.BruteforceWindowMin }, func(_ *Host, o *AgentOptions) any { return ovI(o.BruteforceWindowMin) }, func(s hostSec) any { return s.BruteforceWindowMin }},
	{"portscan_enabled", "bool", func(d AgentDefaults) any { return d.PortscanEnabled }, func(_ *Host, o *AgentOptions) any { return ovB(o.PortscanEnabled) }, func(s hostSec) any { return s.PortscanEnabled }},
	{"portscan_threshold_per_min", "int", func(d AgentDefaults) any { return d.PortscanThresholdPerMin }, func(_ *Host, o *AgentOptions) any { return ovI(o.PortscanThresholdPerMin) }, func(s hostSec) any { return s.PortscanThreshold }},
	{"auto_block_threshold", "int", func(d AgentDefaults) any { return d.AutoBlockThreshold }, func(_ *Host, o *AgentOptions) any { return ovI(o.AutoBlockThreshold) }, func(s hostSec) any { return s.AutoBlockThreshold }},
	{"auto_block_minutes", "int", func(d AgentDefaults) any { return d.AutoBlockMinutes }, func(_ *Host, o *AgentOptions) any { return ovI(o.AutoBlockMinutes) }, func(s hostSec) any { return s.AutoBlockMinutes }},
	// A host's never-block entries are added to the global list.
	{"never_block", "list", func(d AgentDefaults) any { return nonNil(d.NeverBlock) }, func(_ *Host, o *AgentOptions) any {
		if o.NeverBlock == nil {
			return nil
		}
		return o.NeverBlock
	}, func(s hostSec) any { return nonNil(s.NeverBlock) }},
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// hostSettingsView builds the per-setting inheritance view for a host.
func (a *App) hostSettingsView(h *Host) map[string]HostSetting {
	d := a.settings.Agent()
	s := securityFor(h, d)
	o := hostOptions(h)
	out := make(map[string]HostSetting, len(hostSettingDefs))
	for _, def := range hostSettingDefs {
		ov := def.override(h, &o)
		out[def.key] = HostSetting{Effective: def.effective(s), Override: ov, Global: def.global(d), Inherited: ov == nil}
	}
	return out
}

// applyHostSettings applies a {key: value|null} map of overrides. It returns
// column updates for the hosts table and the new agent_options value.
func (a *App) applyHostSettings(h *Host, opts *AgentOptions, in map[string]json.RawMessage, set func(col string, v any), changes map[string]any) error {
	for key, raw := range in {
		var def *settingDef
		for i := range hostSettingDefs {
			if hostSettingDefs[i].key == key {
				def = &hostSettingDefs[i]
			}
		}
		if def == nil {
			return badRequest("unknown setting %q", key)
		}
		isNull := strings.TrimSpace(string(raw)) == "null"
		var bv *bool
		var iv *int
		var lv []string
		if !isNull {
			switch def.kind {
			case "bool":
				var b bool
				if err := json.Unmarshal(raw, &b); err != nil {
					return badRequest("%s must be true, false or null", key)
				}
				bv = &b
			case "int":
				var n int
				if err := json.Unmarshal(raw, &n); err != nil {
					return badRequest("%s must be a number or null", key)
				}
				if err := checkSettingRange(key, n); err != nil {
					return err
				}
				iv = &n
			case "list":
				if err := json.Unmarshal(raw, &lv); err != nil {
					return badRequest("%s must be a list or null", key)
				}
				l, err := normalizeAllowlist(lv)
				if err != nil {
					return err
				}
				lv = nonNil(l)
			}
		}
		if isNull {
			changes["setting."+key] = nil
		} else if bv != nil {
			changes["setting."+key] = *bv
		} else if iv != nil {
			changes["setting."+key] = *iv
		} else {
			changes["setting."+key] = lv
		}
		switch key {
		case "allow_ip_block", "auto_block", "allow_cleanup", "allow_upgrades":
			if bv == nil {
				set(key, nil)
			} else {
				set(key, *bv)
			}
		case "bruteforce_enabled":
			opts.BruteforceEnabled = bv
		case "portscan_enabled":
			opts.PortscanEnabled = bv
		case "bruteforce_threshold":
			opts.BruteforceThreshold = iv
		case "bruteforce_window_minutes":
			opts.BruteforceWindowMin = iv
		case "portscan_threshold_per_min":
			opts.PortscanThresholdPerMin = iv
		case "auto_block_threshold":
			opts.AutoBlockThreshold = iv
		case "auto_block_minutes":
			opts.AutoBlockMinutes = iv
		case "never_block":
			if isNull {
				opts.NeverBlock = nil
			} else {
				opts.NeverBlock = lv
			}
		}
	}
	return nil
}

func checkSettingRange(key string, n int) error {
	lo, hi := 1, 100000
	switch key {
	case "bruteforce_window_minutes":
		hi = 1440
	case "auto_block_minutes":
		hi = 60 * 24 * 90
	}
	if n < lo || n > hi {
		return badRequest("%s must be between %d and %d", key, lo, hi)
	}
	return nil
}

// --- never-block list ---

var (
	serverAddrMu    sync.Mutex
	serverAddrCache []string
	serverAddrAt    time.Time
	// serverAddrsFn is replaced in tests.
	serverAddrsFn = discoverServerAddrs
)

// serverAddrs lists the addresses the Sentrel server itself is reachable on:
// every local interface address plus the configured server URL host.
func (a *App) serverAddrs() []string {
	serverAddrMu.Lock()
	defer serverAddrMu.Unlock()
	if serverAddrCache != nil && time.Since(serverAddrAt) < 10*time.Minute {
		return serverAddrCache
	}
	host := ""
	if u, err := url.Parse(a.settings.General().ServerURL); err == nil {
		host = u.Hostname()
	}
	serverAddrCache = serverAddrsFn(host)
	serverAddrAt = time.Now()
	return serverAddrCache
}

func discoverServerAddrs(urlHost string) []string {
	out := []string{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	if urlHost != "" {
		if ip := net.ParseIP(urlHost); ip != nil {
			out = append(out, ip.String())
		} else {
			r := &net.Resolver{}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if ips, err := r.LookupIPAddr(ctx, urlHost); err == nil {
				for _, ip := range ips {
					out = append(out, ip.IP.String())
				}
			}
		}
	}
	return mergeLists(out, nil)
}

// neverBlockList is the full allowlist for a host: global entries, the host's
// own additions and the server's own addresses (always protected).
func (a *App) neverBlockList(h *Host) []string {
	return mergeLists(a.hostSecurity(h).NeverBlock, a.serverAddrs())
}

// neverBlocked reports whether ip must not be blocked on host h.
func (a *App) neverBlocked(h *Host, ip string) bool {
	return ipWhitelisted(ip, a.neverBlockList(h))
}

var errNeverBlock = badRequest("address is on the never-block list")

// bruteforceRule applies the host's effective brute-force threshold as the
// warning level when the rule in force is the provider default.
func (a *App) bruteforceRule(ctx context.Context, hostID int64, eff *EffectiveRule) *EffectiveRule {
	h, err := a.getHost(ctx, hostID)
	if err != nil {
		return eff
	}
	hasOv := hostOptions(h).BruteforceThreshold != nil
	if !hasOv && eff.Origin != "provider" {
		return eff
	}
	t := float64(a.hostSecurity(h).BruteforceThreshold)
	r := eff.Rule
	r.Warn = &t
	if r.Crit != nil && *r.Crit <= t {
		r.Crit = nil
	}
	return &EffectiveRule{Rule: r, Origin: eff.Origin}
}
