package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "diskhealth", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSmartDeviceAllowlist(t *testing.T) {
	for _, ok := range []string{"/dev/sda", "/dev/sdab", "/dev/nvme0n1", "/dev/nvme12n3", "/dev/hda", "/dev/vdb"} {
		if !validSmartDevice(ok) {
			t.Errorf("%q should be allowed", ok)
		}
	}
	for _, bad := range []string{"", "/dev/sda; rm", "/dev/sda ", " /dev/sda", "/dev/../etc/passwd", "../dev/sda", "/dev/sda/../sdb", "/dev/sda1", "/dev/nvme0", "/dev/nvme0n1p1",
		"/dev/sdA", "/dev/sd", "-a", "--scan", "sda", "/dev/sda\n", "/dev/sda\x00", "/dev/disk/by-id/x", "/dev/loop0", "/dev/sdabcd", "/dev/$(id)", "/dev/sda`id`"} {
		if validSmartDevice(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestParseSmartScanDropsUnsafe(t *testing.T) {
	got, err := parseSmartScan(fixture(t, "scan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0] != (scanTarget{"/dev/sda", "sat"}) || got[1] != (scanTarget{"/dev/nvme0n1", "nvme"}) || got[2] != (scanTarget{"/dev/sdb", ""}) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseSmartctlSata(t *testing.T) {
	d, err := parseSmartctl("/dev/sda", fixture(t, "sata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Health != "passed" || d.Model != "Samsung SSD 860 EVO 500GB" || d.Serial != "…456A" {
		t.Fatalf("%+v", d)
	}
	if *d.TemperatureC != 34 || *d.PowerOnHours != 18250 || *d.Reallocated != 8 || *d.Pending != 2 || *d.Uncorrectable != 0 {
		t.Fatalf("%+v", d)
	}
	if d.PercentUsed != nil {
		t.Fatal("sata has no wear value")
	}
}

func TestParseSmartctlNVMe(t *testing.T) {
	d, err := parseSmartctl("/dev/nvme0n1", fixture(t, "nvme.json"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Health != "failed" || *d.PercentUsed != 93 || *d.MediaErrors != 3 || *d.TemperatureC != 41 || *d.PowerOnHours != 4100 || d.Serial != "…ABC1" {
		t.Fatalf("%+v", d)
	}
}

func TestParseSmartctlRejectsNonJSON(t *testing.T) {
	if _, err := parseSmartctl("/dev/sda", []byte("smartctl 6.6\nunrecognized option -j")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := parseSmartctl("/dev/sda", []byte(`{"foo":1}`)); err == nil {
		t.Fatal("expected error for missing json_format_version")
	}
}

func TestParseMdstat(t *testing.T) {
	h := parseMdstat(fixture(t, "mdstat_healthy"))
	if len(h) != 1 || h[0].Name != "md0" || h[0].Level != "raid1" || h[0].Members != 2 || h[0].Working != 2 || h[0].Degraded || h[0].Failed || h[0].SyncAction != "" {
		t.Fatalf("healthy: %+v", h)
	}
	d := parseMdstat(fixture(t, "mdstat_degraded"))
	if len(d) != 2 {
		t.Fatalf("degraded: %+v", d)
	}
	if !d[0].Degraded || d[0].Failed || d[0].FailedDevs != 1 || d[0].Level != "raid5" || d[0].Members != 4 || d[0].Working != 3 {
		t.Fatalf("md0: %+v", d[0])
	}
	if !d[1].Degraded || d[1].Failed || d[1].Working != 1 {
		t.Fatalf("md1: %+v", d[1])
	}
	r := parseMdstat(fixture(t, "mdstat_resync"))
	if len(r) != 1 || !r[0].Degraded || r[0].SyncAction != "recovery" || r[0].SyncPct != 8.5 || r[0].Finish != "94.3min" || r[0].Name != "md127" {
		t.Fatalf("resync: %+v", r)
	}
	f := parseMdstat([]byte("md0 : inactive sdb1[1](S)\n      1000 blocks super 1.2\n"))
	if len(f) != 1 || !f[0].Failed {
		t.Fatalf("inactive: %+v", f)
	}
	f = parseMdstat([]byte("md0 : active raid0 sdb[1] sda[0](F)\n      1000 blocks 512k chunks\n"))
	if len(f) != 1 || !f[0].Failed || !f[0].Degraded {
		t.Fatalf("raid0 failed: %+v", f)
	}
	if got := parseMdstat([]byte("Personalities :\nunused devices: <none>\n")); len(got) != 0 {
		t.Fatalf("empty: %+v", got)
	}
}

func TestParseZpoolList(t *testing.T) {
	p := parseZpoolList([]byte("tank\tONLINE\t10.9T\t3.21T\nbackup\tDEGRADED\t7.27T\t1.1T\n\nbroken line\n"))
	if len(p) != 2 || p[0] != (ZPool{"tank", "ONLINE", "10.9T", "3.21T"}) || p[1].Health != "DEGRADED" {
		t.Fatalf("%+v", p)
	}
}

func TestMaskSerial(t *testing.T) {
	if maskSerial("  S3Z2NB0K123456A ") != "…456A" || maskSerial("ab") != "…ab" || maskSerial("") != "" {
		t.Fatal("maskSerial")
	}
}
