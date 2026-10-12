package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeSized(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiskUsageWalker(t *testing.T) {
	root := t.TempDir()
	writeSized(t, filepath.Join(root, "a", "b", "c", "deep.bin"), 1<<20)
	writeSized(t, filepath.Join(root, "a", "b", "mid.bin"), 256<<10)
	writeSized(t, filepath.Join(root, "d", "small.bin"), 64<<10)
	writeSized(t, filepath.Join(root, "top.bin"), 2<<20)
	for i := 0; i < 30; i++ { // more files than the largest-files limit
		writeSized(t, filepath.Join(root, "many", "f"+string(rune('a'+i%26))+string(rune('a'+i/26))), 4096)
	}

	res, err := DiskUsage(context.Background(), root, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DUEntry{}
	for _, e := range res.Entries {
		by[e.Path] = e
		if e.Depth < 1 || e.Depth > 2 {
			t.Errorf("entry beyond depth: %+v", e)
		}
	}
	a, b := by[filepath.Join(root, "a")], by[filepath.Join(root, "a", "b")]
	if a.Depth != 1 || b.Depth != 2 {
		t.Fatalf("depths: %+v %+v", a, b)
	}
	if _, ok := by[filepath.Join(root, "a", "b", "c")]; ok {
		t.Error("depth-3 directory reported with depth limit 2")
	}
	// c is aggregated into b and a.
	if b.Bytes < (1<<20)+(256<<10) || a.Bytes < b.Bytes {
		t.Errorf("aggregation: a=%d b=%d", a.Bytes, b.Bytes)
	}
	for i := 1; i < len(res.Entries); i++ {
		if res.Entries[i].Bytes > res.Entries[i-1].Bytes {
			t.Fatal("entries not sorted largest first")
		}
	}
	if len(res.LargestFiles) != duTopFiles {
		t.Errorf("largest files: %d, want %d", len(res.LargestFiles), duTopFiles)
	}
	if res.LargestFiles[0].Path != filepath.Join(root, "top.bin") || res.LargestFiles[1].Path != filepath.Join(root, "a", "b", "c", "deep.bin") {
		t.Errorf("largest files order: %+v", res.LargestFiles[:2])
	}
	if res.ScannedBytes < (2<<20)+(1<<20) || res.Files != 34 || res.Truncated {
		t.Errorf("totals: %+v", res)
	}

	// top limits the directory list to the biggest.
	res, _ = DiskUsage(context.Background(), root, 3, 2)
	if len(res.Entries) != 2 || res.Entries[0].Path != filepath.Join(root, "a") {
		t.Errorf("top=2: %+v", res.Entries)
	}
}

func TestDiskUsageTimeoutAndErrors(t *testing.T) {
	root := t.TempDir()
	writeSized(t, filepath.Join(root, "x", "f"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := DiskUsage(ctx, root, 3, 10)
	if err != nil || !res.Truncated {
		t.Fatalf("cancelled walk: %+v %v", res, err)
	}
	if _, err := DiskUsage(context.Background(), filepath.Join(root, "missing"), 3, 10); err == nil {
		t.Error("missing path accepted")
	}
	if _, err := DiskUsage(context.Background(), filepath.Join(root, "x", "f"), 3, 10); err == nil {
		t.Error("file accepted as a root")
	}
}

func TestParseDiskUsageArgs(t *testing.T) {
	root := t.TempDir()
	if _, d, n, s, err := parseDiskUsageArgs([]string{root, "3", "30", "120"}); err != nil || d != 3 || n != 30 || s != 120 {
		t.Fatalf("valid: %v", err)
	}
	for _, bad := range [][]string{
		{root, "0", "30", "120"}, {root, "3", "0", "120"}, {root, "3", "30", "1"}, {root, "x", "30", "120"},
		{"relative/path", "3", "30", "120"}, {"", "3", "30", "120"}, {root}, {root + "\x00", "3", "30", "120"},
	} {
		if _, _, _, _, err := parseDiskUsageArgs(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestValidateCleanupTasksAllowlist(t *testing.T) {
	first := cleanupTaskIDs[0]
	got, err := validateCleanupTasks(first + "," + first)
	if err != nil || len(got) != 1 || got[0] != first {
		t.Fatalf("valid task: %v %v", got, err)
	}
	for _, bad := range []string{"", " ", "nope", first + ";id", first + ",$(id)", "../" + first, "--dry-run", first + ",rm -rf /"} {
		if _, err := validateCleanupTasks(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPrivDiskCleanupRejectsUnknown(t *testing.T) {
	var out bytes.Buffer
	if err := privDiskCleanup(context.Background(), &out, []string{"--dry-run", "format_disk"}); err == nil {
		t.Fatal("unknown task accepted")
	}
	if err := privDiskCleanup(context.Background(), &out, []string{"--bogus", cleanupTaskIDs[0]}); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if strings.Contains(out.String(), "freed_bytes") {
		t.Errorf("nothing should run: %s", out.String())
	}
}

func TestSweep(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-30 * 24 * time.Hour)
	oldFile := filepath.Join(root, "sub", "old.gz")
	newFile := filepath.Join(root, "sub", "new.gz")
	keep := filepath.Join(root, "sub", "old.txt")
	writeSized(t, oldFile, 1000)
	writeSized(t, newFile, 1000)
	writeSized(t, keep, 1000)
	for _, p := range []string{oldFile, keep} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	match := func(n string) bool { return strings.HasSuffix(n, ".gz") }
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	n, files, _ := sweep(context.Background(), root, sweepOpts{Cutoff: cutoff, DryRun: true, Match: match})
	if files != 1 || n < 1000 {
		t.Fatalf("dry run: %d files %d bytes", files, n)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatal("dry run deleted a file")
	}
	n, files, _ = sweep(context.Background(), root, sweepOpts{Cutoff: cutoff, Match: match})
	if files != 1 || n < 1000 {
		t.Fatalf("sweep: %d files %d bytes", files, n)
	}
	if _, err := os.Stat(oldFile); err == nil {
		t.Error("old file survived")
	}
	for _, p := range []string{newFile, keep} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
}

func TestParseHumanSize(t *testing.T) {
	cases := map[string]int64{"1.5GB": 1500000000, "340kB": 340000, "12 MiB": 12 << 20, "0B": 0, "": 0, "garbage": 0}
	for in, want := range cases {
		if got := parseHumanSize(in); got != want {
			t.Errorf("%q: got %d want %d", in, got, want)
		}
	}
}

func TestIsMountPoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		if !isMountPoint(`c:\`, []string{"C:"}) || isMountPoint(`C:\Windows`, []string{"C:"}) {
			t.Error("windows mount matching")
		}
		return
	}
	mounts := []string{"/", "/boot", "/mnt/data"}
	for _, ok := range []string{"/", "/boot", "/mnt/data/", "/mnt//data"} {
		if !isMountPoint(ok, mounts) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"/etc", "/root", "/var/lib/sentrel-agent", "/mnt", "/boot/../etc"} {
		if isMountPoint(bad, mounts) {
			t.Errorf("%q accepted", bad)
		}
	}
}
