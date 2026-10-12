//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink planted at the marker path must never be written through or
// chowned by the root-run install step.
func TestWriteMarkerRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(target)
	if err := os.Symlink(target, markerPath(dir)); err != nil {
		t.Fatal(err)
	}
	err := writeMarkerOwned(dir, &updateMarker{From: "a", To: "b"}, os.Getuid(), os.Getgid())
	if err == nil {
		t.Fatal("write through symlink was not refused")
	}
	b, _ := os.ReadFile(target)
	after, _ := os.Stat(target)
	if string(b) != "precious" || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("symlink target was modified")
	}
	// A dangling symlink must not be created through either.
	os.Remove(markerPath(dir))
	missing := filepath.Join(t.TempDir(), "new")
	_ = os.Symlink(missing, markerPath(dir))
	if err := writeMarkerOwned(dir, &updateMarker{}, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("dangling symlink not refused")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("dangling target was created")
	}
}

func TestWriteMarkerNormal(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ { // second pass replaces an existing regular file
		if err := writeMarkerOwned(dir, &updateMarker{From: "a", To: "b", Attempts: i}, os.Getuid(), os.Getgid()); err != nil {
			t.Fatal(err)
		}
	}
	m, err := readMarker(dir)
	if err != nil || m.Attempts != 1 {
		t.Fatalf("marker %+v %v", m, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("leftover temp files: %v", ents)
	}
}
