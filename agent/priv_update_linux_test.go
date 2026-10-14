//go:build linux

package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// updateSandbox points the updater at temp dirs owned by the test user.
func updateSandbox(t *testing.T) (binDir string) {
	t.Helper()
	oldBin, oldPriv, oldUID := installedBinary, privDir, privOwnerUID
	binDir = t.TempDir()
	if err := os.Chmod(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinary = filepath.Join(binDir, "sentrel-agent")
	privDir = filepath.Join(t.TempDir(), "priv")
	privOwnerUID = os.Getuid()
	t.Cleanup(func() { installedBinary, privDir, privOwnerUID = oldBin, oldPriv, oldUID })
	return binDir
}

func TestUpdateLockIsExclusive(t *testing.T) {
	updateSandbox(t)
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := lockUpdates()
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			unlock()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("lock not exclusive: %d holders at once", maxInside)
	}
}

func TestPrivDirMustBeRootOnly(t *testing.T) {
	updateSandbox(t)
	if err := os.MkdirAll(privDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(privDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := lockUpdates(); err == nil {
		t.Fatal("lock accepted a world-accessible directory")
	}
	privOwnerUID = os.Getuid() + 1
	_ = os.Chmod(privDir, 0o700)
	if _, err := lockUpdates(); err == nil {
		t.Fatal("lock accepted a directory with the wrong owner")
	}
}

func TestStageCopyUniqueExclusiveAndPrivate(t *testing.T) {
	dir := t.TempDir()
	// A planted symlink at a guessable name must never be followed.
	victim := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(victim, []byte("keep"), 0o644)
	_ = os.Symlink(victim, filepath.Join(dir, stagePrefix+"new"))
	_ = os.Symlink(victim, filepath.Join(dir, "sentrel-agent.new"))

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		name, fi, err := stageCopy(bytes.NewReader([]byte("payload")), dir)
		if err != nil {
			t.Fatal(err)
		}
		if seen[name] {
			t.Fatal("staging name reused")
		}
		seen[name] = true
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("staged file is accessible to others: %o", fi.Mode().Perm())
		}
		if err := stillStaged(name, fi); err != nil {
			t.Fatal(err)
		}
		// The installer makes the file executable before swapping it in; that
		// chmod changes the change time but must not look like a replacement.
		if err := os.Chmod(name, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := afterChmod(name, fi); err != nil {
			t.Fatalf("own chmod rejected: %v", err)
		}
		// Swapping the file for another one is detected.
		_ = os.Remove(name)
		_ = os.WriteFile(name, []byte("evil"), 0o700)
		if err := stillStaged(name, fi); err == nil {
			t.Fatal("replaced staged file not detected")
		}
		if _, err := afterChmod(name, fi); err == nil {
			t.Fatal("replaced staged file not detected after chmod")
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("staging wrote through a symlink")
	}
}

func TestStageCopyRejectsOversize(t *testing.T) {
	if testing.Short() {
		t.Skip("large copy")
	}
	// A reader that yields more than the limit.
	r := &endless{}
	if _, _, err := stageCopy(r, t.TempDir()); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected size error, got %v", err)
	}
}

type endless struct{}

func (endless) Read(p []byte) (int, error) { return len(p), nil }

func TestRollbackRequiresPendingMarker(t *testing.T) {
	binDir := updateSandbox(t)
	if err := os.WriteFile(installedBinary, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedBinary+".old", []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = binDir

	// No marker: refused, binary untouched.
	if err := rollbackUpdate(); err == nil {
		t.Fatal("rollback allowed without a pending update")
	}
	if b, _ := os.ReadFile(installedBinary); string(b) != "new" {
		t.Fatal("binary changed by a refused rollback")
	}

	// Stale marker: refused and the marker is dropped.
	stale := &updateMarker{From: "a", To: "b", At: time.Now().Add(-recoveryWindow - time.Minute).Unix()}
	if err := ensurePrivDir(); err != nil {
		t.Fatal(err)
	}
	if err := writeRootMarker(stale); err != nil {
		t.Fatal(err)
	}
	if err := rollbackUpdate(); err == nil {
		t.Fatal("rollback allowed outside the recovery window")
	}
	if b, _ := os.ReadFile(installedBinary); string(b) != "new" {
		t.Fatal("binary changed by a refused rollback")
	}
	if _, err := os.Stat(rootMarkerPath()); err == nil {
		t.Fatal("stale marker kept")
	}

	// Fresh marker: legitimate recovery works and clears the marker.
	if err := writeRootMarker(&updateMarker{From: "a", To: "b", At: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := rollbackUpdate(); err != nil {
		t.Fatalf("legitimate rollback failed: %v", err)
	}
	if b, _ := os.ReadFile(installedBinary); string(b) != "old" {
		t.Fatal("previous binary not restored")
	}
	if _, err := os.Stat(rootMarkerPath()); err == nil {
		t.Fatal("marker kept after rollback")
	}
}

func TestConfirmUpdateClosesRollbackWindow(t *testing.T) {
	updateSandbox(t)
	_ = os.WriteFile(installedBinary, []byte("new"), 0o755)
	_ = os.WriteFile(installedBinary+".old", []byte("old"), 0o755)
	if err := ensurePrivDir(); err != nil {
		t.Fatal(err)
	}
	if err := writeRootMarker(&updateMarker{From: "a", To: "b", At: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := confirmUpdate(); err != nil {
		t.Fatal(err)
	}
	if err := rollbackUpdate(); err == nil {
		t.Fatal("rollback allowed after the update was confirmed")
	}
	if b, _ := os.ReadFile(installedBinary); string(b) != "new" {
		t.Fatal("binary changed")
	}
}
