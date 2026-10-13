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
