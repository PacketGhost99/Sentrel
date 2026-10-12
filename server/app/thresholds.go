package app

import (
	"context"
	"path"
	"sort"
	"sync"
)

// MetricDef describes a metric that thresholds can be set on.
type MetricDef struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Sample       string `json:"sample"`    // sample name as pushed by agent / poller
	LabelKey     string `json:"label_key"` // label that distinguishes instances (mount, iface...)
	Unit         string `json:"unit"`
	LowerIsWorse bool   `json:"lower_is_worse"`
	Description  string `json:"description"`
}

// metricCatalog lists every thresholdable metric.
var metricCatalog = []MetricDef{
	{Key: "cpu", Name: "CPU usage", Sample: "cpu_usage_percent", Unit: "%", Description: "Total CPU utilisation"},
	{Key: "memory", Name: "Memory usage", Sample: "mem_used_percent", Unit: "%", Description: "Used memory excluding cache/buffers"},
	{Key: "swap", Name: "Swap usage", Sample: "swap_used_percent", Unit: "%"},
	{Key: "disk", Name: "Disk usage", Sample: "disk_used_percent", LabelKey: "mount", Unit: "%", Description: "Per mount point; use label match for a specific mount (globs allowed)"},
	{Key: "inodes", Name: "Inode usage", Sample: "disk_inodes_used_percent", LabelKey: "mount", Unit: "%"},
	{Key: "disk_fill_hours", Name: "Disk full in", Sample: "disk_fill_eta_hours", LabelKey: "mount", Unit: "h", LowerIsWorse: true, Description: "Predicted hours until the mount is full (linear fit over 24h)"},
	{Key: "load", Name: "Load per core", Sample: "load1_per_core", Unit: "", Description: "1-minute load average divided by CPU cores"},
	{Key: "temperature", Name: "Temperature", Sample: "temperature_celsius", LabelKey: "sensor", Unit: "°C"},
	{Key: "net_errors", Name: "Network errors", Sample: "net_errors_per_sec", LabelKey: "iface", Unit: "/s"},
	{Key: "if_util", Name: "Interface utilisation", Sample: "if_util_percent", LabelKey: "ifname", Unit: "%", Description: "SNMP interface utilisation (max of in/out)"},
	{Key: "security_updates", Name: "Security updates pending", Sample: "packages_security_updates", Unit: ""},
	{Key: "reboot_required", Name: "Reboot required", Sample: "reboot_required", Unit: "", Description: "1 when the OS reports a pending reboot"},
	{Key: "old_kernels", Name: "Old kernels installed", Sample: "old_kernels_installed", Unit: "", Description: "Installed kernels that are neither running nor the newest (removable)"},
	{Key: "kernel_reboot_pending", Name: "Newer kernel waiting for reboot", Sample: "kernel_reboot_pending", Unit: "", Description: "1 when the newest installed kernel is not the running one"},
	{Key: "bruteforce", Name: "Failed logins (per IP, window)", Sample: "auth_failures_window_max", Unit: "", Description: "Most failed logins from one IP within the brute-force window"},
	{Key: "container_restarts", Name: "Container restarts", Sample: "container_restarts_delta", LabelKey: "container", Unit: "", Description: "Container restarts since the previous collection"},
	{Key: "host_down", Name: "Host offline after", Sample: "", Unit: "s", Description: "Seconds without contact before a host is considered down (critical only)"},
	{Key: "host_storage_mb", Name: "Metric storage per host", Sample: "", Unit: "MB", Description: "Stored time-series data for one host"},
}

func metricByKey(k string) (MetricDef, bool) {
	for _, m := range metricCatalog {
		if m.Key == k {
			return m, true
		}
	}
	return MetricDef{}, false
}

var metricBySample = func() map[string]MetricDef {
	m := map[string]MetricDef{}
	for _, d := range metricCatalog {
		if d.Sample != "" {
			m[d.Sample] = d
		}
	}
	return m
}()

type defaultRule struct {
	Metric   string
	Warn     *float64
	Crit     *float64
	ForCount int
	Enabled  bool
}

func f(v float64) *float64 { return &v }

func defaultThresholds() []defaultRule {
	return []defaultRule{
		{"cpu", f(85), f(95), 4, true},
		{"memory", f(90), f(97), 3, true},
		{"swap", f(60), f(85), 4, true},
		{"disk", f(80), f(90), 2, true},
		{"inodes", f(80), f(90), 2, true},
		{"disk_fill_hours", f(72), f(24), 2, true},
		{"load", f(2), f(4), 4, true},
		{"temperature", f(80), f(90), 3, true},
		{"net_errors", f(10), f(100), 3, true},
		{"if_util", f(80), f(95), 3, true},
		{"security_updates", f(1), nil, 1, false},
		{"reboot_required", f(1), nil, 1, false},
		{"old_kernels", f(1), nil, 1, false},
		{"kernel_reboot_pending", f(1), nil, 1, false},
		{"bruteforce", f(10), f(50), 1, true},
		{"container_restarts", f(1), f(5), 1, true},
		{"host_down", nil, f(120), 1, true},
		{"host_storage_mb", f(512), nil, 1, true},
	}
}

// ruleSet is an in-memory copy of threshold_rules.
type ruleSet struct {
	mu    sync.RWMutex
	rules []ThresholdRule
}

func (rs *ruleSet) load(ctx context.Context, a *App) error {
	var rules []ThresholdRule
	if err := a.db.SelectContext(ctx, &rules, `SELECT * FROM threshold_rules`); err != nil {
		return err
	}
	rs.mu.Lock()
	rs.rules = rules
	rs.mu.Unlock()
	return nil
}

// hostRef is the minimal host info needed to resolve rules.
type hostRef struct {
	ID       int64
	TenantID int64
	GroupID  int64
	Name     string
	Kind     string
}

// EffectiveRule is a resolved rule plus where it came from.
type EffectiveRule struct {
	Rule   ThresholdRule `json:"rule"`
	Origin string        `json:"origin"` // provider, tenant, group, host
}

func labelMatches(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	if pattern == value {
		return true
	}
	ok, _ := path.Match(pattern, value)
	return ok
}

// resolve returns the most specific rule for host/metric/label value.
func (rs *ruleSet) resolve(h hostRef, metric, labelValue string) *EffectiveRule {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	type cand struct {
		r     ThresholdRule
		level int // lower is more specific
	}
	var best *cand
	for _, r := range rs.rules {
		if r.Metric != metric {
			continue
		}
		base := -1
		switch {
		case r.Scope == "host" && r.ScopeID == h.ID:
			base = 0
		case r.Scope == "group" && h.GroupID != 0 && r.ScopeID == h.GroupID:
			base = 2
		case r.Scope == "global" && r.TenantID == h.TenantID && h.TenantID != 0:
			base = 4
		case r.Scope == "global" && r.TenantID == 0:
			base = 6
		}
		if base < 0 {
			continue
		}
		if r.LabelMatch != "" {
			if labelValue == "" || !labelMatches(r.LabelMatch, labelValue) {
				continue
			}
		} else {
			base++ // wildcard rule is less specific than a label-specific one
		}
		if best == nil || base < best.level {
			best = &cand{r, base}
		}
	}
	if best == nil {
		return nil
	}
	origin := map[int]string{0: "host", 1: "host", 2: "group", 3: "group", 4: "tenant", 5: "tenant", 6: "provider", 7: "provider"}[best.level]
	return &EffectiveRule{Rule: best.r, Origin: origin}
}

// MergedRule is one row of the merged thresholds view for a host.
type MergedRule struct {
	Metric    MetricDef       `json:"metric"`
	Effective *EffectiveRule  `json:"effective"`
	Overrides []EffectiveRule `json:"label_overrides"`
	Chain     []EffectiveRule `json:"chain"` // every rule that applies, most specific first
}

// merged builds the merged view for a host.
func (rs *ruleSet) merged(h hostRef) []MergedRule {
	var out []MergedRule
	for _, m := range metricCatalog {
		mr := MergedRule{Metric: m, Effective: rs.resolve(h, m.Key, "")}
		rs.mu.RLock()
		for _, r := range rs.rules {
			if r.Metric != m.Key {
				continue
			}
			origin := ""
			switch {
			case r.Scope == "host" && r.ScopeID == h.ID:
				origin = "host"
			case r.Scope == "group" && h.GroupID != 0 && r.ScopeID == h.GroupID:
				origin = "group"
			case r.Scope == "global" && r.TenantID == h.TenantID && h.TenantID != 0:
				origin = "tenant"
			case r.Scope == "global" && r.TenantID == 0:
				origin = "provider"
			}
			if origin == "" {
				continue
			}
			er := EffectiveRule{Rule: r, Origin: origin}
			mr.Chain = append(mr.Chain, er)
			if r.LabelMatch != "" {
				mr.Overrides = append(mr.Overrides, er)
			}
		}
		rs.mu.RUnlock()
		rank := map[string]int{"host": 0, "group": 1, "tenant": 2, "provider": 3}
		sort.SliceStable(mr.Chain, func(i, j int) bool { return rank[mr.Chain[i].Origin] < rank[mr.Chain[j].Origin] })
		out = append(out, mr)
	}
	return out
}

// severityFor compares a value against a rule.
func severityFor(def MetricDef, r ThresholdRule, v float64) (sev string, thr *float64) {
	worse := func(t float64) bool {
		if def.LowerIsWorse {
			return v <= t
		}
		return v >= t
	}
	if r.Crit != nil && worse(*r.Crit) {
		return "critical", r.Crit
	}
	if r.Warn != nil && worse(*r.Warn) {
		return "warning", r.Warn
	}
	return "ok", nil
}
