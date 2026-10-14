package app

import (
	"strings"
	"testing"
)

func i64(v int64) *int64 { return &v }

func TestDiskHealthConditions(t *testing.T) {
	s := &DiskHealth{
		Devices: []DiskHealthDevice{
			{Device: "/dev/sda", Model: "Disk A", Health: "failed"},
			{Device: "/dev/sdb", Health: "passed", Reallocated: i64(0), Pending: i64(3)},
			{Device: "/dev/nvme0n1", Health: "passed", PercentUsed: i64(91)},
			{Device: "/dev/sdc", Health: "passed", Reallocated: i64(0), PercentUsed: i64(89)},
		},
		Raid:  []DiskHealthRaid{{Name: "md0", Level: "raid1", Members: 2, Working: 1, Degraded: true}, {Name: "md1", Level: "raid1", Members: 2, Working: 2}},
		Pools: []DiskHealthPool{{Name: "tank", Health: "DEGRADED"}, {Name: "ok", Health: "ONLINE"}},
	}
	prev := &DiskHealth{Devices: []DiskHealthDevice{{Device: "/dev/sdb", Pending: i64(1)}}}
	carrySectorBaselines(prev, s, 1000)
	got := diskHealthConditions(7, "h1", 0, s, prev)
	want := map[string]string{
		"diskhealth:7:smart:/dev/sda":    "critical",
		"diskhealth:7:smart:/dev/sdb":    "ok",
		"diskhealth:7:sectors:/dev/sdb":  "warning",
		"diskhealth:7:sectors:/dev/sda":  "ok",
		"diskhealth:7:wear:/dev/nvme0n1": "warning",
		"diskhealth:7:wear:/dev/sdc":     "ok",
		"diskhealth:7:md:md0":            "critical",
		"diskhealth:7:md:md1":            "ok",
		"diskhealth:7:zfs:tank":          "critical",
		"diskhealth:7:zfs:ok":            "ok",
	}
	for k, sev := range want {
		if got[k].Severity != sev {
			t.Errorf("%s = %q, want %q", k, got[k].Severity, sev)
		}
	}
	if m := got["diskhealth:7:sectors:/dev/sdb"].Message; !strings.Contains(m, "up from 1") {
		t.Errorf("message should mention the increase: %q", m)
	}
}

func TestSectorBaselineWarnsOnlyOnIncrease(t *testing.T) {
	sev := func(prev, cur *DiskHealth, now int64) string {
		carrySectorBaselines(prev, cur, now)
		return diskHealthConditions(1, "h", 0, cur, prev)["diskhealth:1:sectors:/dev/sda"].Severity
	}
	snap := func(n int64, serial string) *DiskHealth {
		return &DiskHealth{Devices: []DiskHealthDevice{{Device: "/dev/sda", Serial: serial, Health: "passed", Reallocated: i64(n)}}}
	}
	// First sight of a drive with 8 long-stable sectors: no warning.
	s1 := snap(8, "A1")
	if got := sev(&DiskHealth{}, s1, 0); got != "ok" {
		t.Fatalf("first sight = %s", got)
	}
	// Unchanged: still fine.
	s2 := snap(8, "A1")
	if got := sev(s1, s2, 100); got != "ok" {
		t.Fatalf("stable = %s", got)
	}
	// Rises: warns, and keeps warning while the count holds.
	s3 := snap(10, "A1")
	if got := sev(s2, s3, 200); got != "warning" {
		t.Fatalf("rise = %s", got)
	}
	s4 := snap(10, "A1")
	if got := sev(s3, s4, 300); got != "warning" {
		t.Fatalf("held = %s", got)
	}
	// A quiet week later the new count becomes the baseline.
	s5 := snap(10, "A1")
	if got := sev(s4, s5, 200+sectorQuietPeriod+1); got != "ok" {
		t.Fatalf("after quiet period = %s", got)
	}
	// A replaced drive (new serial) starts fresh.
	s6 := snap(30, "B2")
	if got := sev(s5, s6, 400+sectorQuietPeriod); got != "ok" {
		t.Fatalf("replaced = %s", got)
	}
}

func TestSanitizeDiskHealthBounds(t *testing.T) {
	d := &DiskHealth{}
	for i := 0; i < 200; i++ {
		d.Devices = append(d.Devices, DiskHealthDevice{Device: strings.Repeat("x", 500), Health: "bogus"})
	}
	sanitizeDiskHealth(d)
	if len(d.Devices) != 64 || len(d.Devices[0].Device) > 45 || d.Devices[0].Health != "unknown" || d.Raid == nil || d.Pools == nil {
		t.Fatalf("not bounded: %d %d %q", len(d.Devices), len(d.Devices[0].Device), d.Devices[0].Health)
	}
}
