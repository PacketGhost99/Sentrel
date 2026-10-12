package app

import "testing"

func fp(v float64) *float64 { return &v }

func TestResolvePrecedence(t *testing.T) {
	rs := &ruleSet{rules: []ThresholdRule{
		{ID: 1, TenantID: 0, Scope: "global", Metric: "disk", Warn: fp(80), Crit: fp(90), Enabled: true},
		{ID: 2, TenantID: 5, Scope: "global", Metric: "disk", Warn: fp(70), Crit: fp(85), Enabled: true},
		{ID: 3, TenantID: 5, Scope: "group", ScopeID: 9, Metric: "disk", Warn: fp(60), Enabled: true},
		{ID: 4, TenantID: 5, Scope: "group", ScopeID: 9, Metric: "disk", LabelMatch: "/var", Warn: fp(50), Enabled: true},
		{ID: 5, TenantID: 5, Scope: "host", ScopeID: 42, Metric: "disk", LabelMatch: "/data*", Warn: fp(95), Enabled: true},
		{ID: 6, TenantID: 7, Scope: "global", Metric: "disk", Warn: fp(10), Enabled: true}, // other tenant
	}}
	cases := []struct {
		name   string
		h      hostRef
		label  string
		want   int64
		origin string
	}{
		{"provider default for tenant 0 host", hostRef{ID: 1, TenantID: 0}, "/", 1, "provider"},
		{"tenant default beats provider", hostRef{ID: 2, TenantID: 5}, "/", 2, "tenant"},
		{"group beats tenant", hostRef{ID: 3, TenantID: 5, GroupID: 9}, "/", 3, "group"},
		{"label rule beats wildcard at same level", hostRef{ID: 3, TenantID: 5, GroupID: 9}, "/var", 4, "group"},
		{"host glob label rule beats group", hostRef{ID: 42, TenantID: 5, GroupID: 9}, "/data2", 5, "host"},
		{"host glob does not match other mount", hostRef{ID: 42, TenantID: 5, GroupID: 9}, "/srv", 3, "group"},
		{"other tenant's rules never apply", hostRef{ID: 8, TenantID: 6}, "/", 1, "provider"},
	}
	for _, c := range cases {
		got := rs.resolve(c.h, "disk", c.label)
		if got == nil || got.Rule.ID != c.want || got.Origin != c.origin {
			t.Errorf("%s: got %+v, want rule %d from %s", c.name, got, c.want, c.origin)
		}
	}
}

func TestSeverityFor(t *testing.T) {
	disk, _ := metricByKey("disk")
	fill, _ := metricByKey("disk_fill_hours")
	r := ThresholdRule{Warn: fp(80), Crit: fp(90)}
	for _, c := range []struct {
		v    float64
		want string
	}{{50, "ok"}, {80, "warning"}, {89.9, "warning"}, {90, "critical"}} {
		if s, _ := severityFor(disk, r, c.v); s != c.want {
			t.Errorf("disk %v: got %s want %s", c.v, s, c.want)
		}
	}
	// Lower is worse: hours until full.
	lr := ThresholdRule{Warn: fp(72), Crit: fp(24)}
	for _, c := range []struct {
		v    float64
		want string
	}{{200, "ok"}, {72, "warning"}, {30, "warning"}, {24, "critical"}, {1, "critical"}} {
		if s, _ := severityFor(fill, lr, c.v); s != c.want {
			t.Errorf("fill %v: got %s want %s", c.v, s, c.want)
		}
	}
	// Only critical set.
	if s, _ := severityFor(disk, ThresholdRule{Crit: fp(95)}, 90); s != "ok" {
		t.Errorf("crit-only below crit should be ok, got %s", s)
	}
}

func TestLabelMatches(t *testing.T) {
	if !labelMatches("", "/anything") || !labelMatches("/var", "/var") || !labelMatches("/mnt/*", "/mnt/disk1") {
		t.Fatal("expected matches")
	}
	if labelMatches("/var", "/var/lib") || labelMatches("/mnt/*", "/srv") {
		t.Fatal("unexpected match")
	}
}
