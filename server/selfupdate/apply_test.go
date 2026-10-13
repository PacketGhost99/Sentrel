package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sentrel/internal/release"
)

type fixture struct {
	t        *testing.T
	data     string
	bin      string
	pub      ed25519.PublicKey
	priv     ed25519.PrivateKey
	restarts int
	healthOK func(version string) error
	cfg      ApplyConfig
}

const arch = "amd64"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, data: t.TempDir()}
	f.pub, f.priv, _ = ed25519.GenerateKey(rand.Reader)
	if err := os.MkdirAll(filepath.Join(f.data, DirName, StagedDir), 0o750); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	f.bin = filepath.Join(binDir, "sentrel-server")
	if err := os.WriteFile(f.bin, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.healthOK = func(string) error { return nil }
	f.cfg = ApplyConfig{
		DataDir: f.data, BinPath: f.bin, CurrentVersion: "0.9.1-dev", GOARCH: arch, Key: f.pub,
		OwnerUID: -1, TmpDir: t.TempDir(), HealthTimeout: 2 * time.Second,
		RunVersion: func(_ context.Context, path string) (string, error) {
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			// The fake binary's content encodes its version: "BIN <version>".
			return "sentrel-server " + strings.TrimPrefix(string(b), "BIN ") + " (test)", nil
		},
		Setcap:  func(context.Context, string) error { return nil },
		Restart: func(context.Context) error { f.restarts++; return nil },
		Health:  func(_ context.Context, v string) error { return f.healthOK(v) },
	}
	return f
}

// stage writes a correctly signed release for version into staged/ and
// triggers it with request.json.
func (f *fixture) stage(version string) {
	f.t.Helper()
	staged := filepath.Join(f.data, DirName, StagedDir)
	asset := release.ServerAsset(arch)
	bin := []byte("BIN " + version)
	sums := []byte(release.SHA256Hex(bin) + "  " + asset + "\n")
	sig := []byte(release.Sign(f.priv, sums) + "\n")
	for name, b := range map[string][]byte{asset: bin, SumsName: sums, SigName: sig} {
		if err := os.WriteFile(filepath.Join(staged, name), b, 0o640); err != nil {
			f.t.Fatal(err)
		}
	}
	f.request(version)
}

func (f *fixture) request(version string) {
	f.t.Helper()
	if err := WriteRequest(f.data, Request{Version: version, Asset: release.ServerAsset(arch), RequestedBy: "tester", RequestedAt: 1}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) binContent() string {
	b, _ := os.ReadFile(f.bin)
	return string(b)
}

func (f *fixture) apply() (*Result, error) { return Apply(context.Background(), f.cfg) }

func TestApplySuccess(t *testing.T) {
	f := newFixture(t)
	f.stage("0.9.1")
	var phases []string
	f.cfg.Logf = func(format string, a ...any) {
		if strings.HasPrefix(format, "phase") {
			phases = append(phases, a[0].(string))
		}
	}
	res, err := f.apply()
	if err != nil || res.Outcome != OutcomeSuccess {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if f.binContent() != "BIN 0.9.1" {
		t.Errorf("binary not swapped: %q", f.binContent())
	}
	if b, _ := os.ReadFile(f.bin + ".old"); string(b) != "OLD-BINARY" {
		t.Errorf(".old = %q", b)
	}
	if f.restarts != 1 {
		t.Errorf("restarts=%d", f.restarts)
	}
	want := []string{PhaseVerifying, PhaseInstalling, PhaseRestarting, PhaseChecking}
	if strings.Join(phases, ",") != strings.Join(want, ",") {
		t.Errorf("phases %v", phases)
	}
	st := ReadState(f.data)
	if st.Request != nil || st.Status != nil || st.Result == nil {
		t.Fatalf("state: %+v", st)
	}
	if st.Result.Outcome != OutcomeSuccess || st.Result.From != "0.9.1-dev" || st.Result.To != "0.9.1" {
		t.Errorf("result: %+v", st.Result)
	}
	if fi, err := os.Stat(filepath.Join(f.data, DirName, ResultFile)); err != nil || (fi.Mode().Perm() != 0o640 && os.PathSeparator != '\\') {
		t.Errorf("result.json mode: %v %v", fi, err)
	}
	// No temp files left next to the binary.
	ents, _ := os.ReadDir(filepath.Dir(f.bin))
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

func TestApplyRollbackWhenUnhealthy(t *testing.T) {
	f := newFixture(t)
	f.stage("0.9.1")
	f.healthOK = func(v string) error {
		if v == "0.9.1" {
			return errors.New("connection refused")
		}
		return nil // the old version comes back healthy
	}
	res, err := f.apply()
	if err == nil || res.Outcome != OutcomeRolledBack || !res.RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if f.binContent() != "OLD-BINARY" {
		t.Errorf("not restored: %q", f.binContent())
	}
	if f.restarts != 2 {
		t.Errorf("restarts=%d (new, then old)", f.restarts)
	}
	if !strings.Contains(res.Message, "did not become healthy") || !strings.Contains(res.Message, "restored") {
		t.Errorf("message: %s", res.Message)
	}
	if st := ReadState(f.data); st.Result == nil || st.Result.Outcome != OutcomeRolledBack {
		t.Errorf("result file: %+v", st.Result)
	}
}

func TestApplyRollbackWhenRestartFails(t *testing.T) {
	f := newFixture(t)
	f.stage("0.9.1")
	n := 0
	f.cfg.Restart = func(context.Context) error {
		n++
		if n == 1 {
			return errors.New("unit failed")
		}
		return nil
	}
	res, err := f.apply()
	if err == nil || !res.RolledBack || f.binContent() != "OLD-BINARY" {
		t.Fatalf("res=%+v err=%v bin=%s", res, err, f.binContent())
	}
}

func TestApplyRefusals(t *testing.T) {
	cases := []struct {
		name    string
		current string
		target  string // staged and requested
		mutate  func(f *fixture)
		wantErr string
	}{
		{"downgrade", "0.9.2", "0.9.1", nil, "not newer"},
		{"equal", "0.9.1", "0.9.1", nil, "not newer"},
		{"prerelease below release", "0.9.1", "0.9.1-rc.1", nil, "not newer"},
		{"dev build cannot update", "dev", "0.9.1", nil, "not a release version"},
		{"bad signature", "0.9.0", "0.9.1", func(f *fixture) {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			sums, _ := os.ReadFile(f.staged(SumsName))
			os.WriteFile(f.staged(SigName), []byte(release.Sign(other, sums)), 0o640)
		}, "verification failed"},
		{"missing signature", "0.9.0", "0.9.1", func(f *fixture) { os.Remove(f.staged(SigName)) }, "SHA256SUMS.sig"},
		{"hash mismatch", "0.9.0", "0.9.1", func(f *fixture) {
			os.WriteFile(f.staged(release.ServerAsset(arch)), []byte("BIN 0.9.1 tampered"), 0o640)
		}, "does not match the signed checksum"},
		{"tampered sums", "0.9.0", "0.9.1", func(f *fixture) {
			b, _ := os.ReadFile(f.staged(SumsName))
			os.WriteFile(f.staged(SumsName), append(b, "# x\n"...), 0o640)
		}, "verification failed"},
		{"wrong arch", "0.9.0", "0.9.1", func(f *fixture) { f.cfg.GOARCH = "arm64" }, "arm64"},
		{"binary lies about version", "0.9.0", "0.9.1", func(f *fixture) {
			// A genuinely signed old release staged as the new one.
			f.signBinary("BIN 0.9.0-rc.1")
		}, "reports version"},
		{"invalid request", "0.9.0", "0.9.1", func(f *fixture) {
			os.WriteFile(filepath.Join(f.data, DirName, RequestFile), []byte("{not json"), 0o640)
		}, "bad update request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.cfg.CurrentVersion = c.current
			f.stage(c.target)
			if c.mutate != nil {
				c.mutate(f)
			}
			res, err := f.apply()
			if err == nil || res == nil || res.Outcome != OutcomeFailed {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if !strings.Contains(res.Message, c.wantErr) {
				t.Errorf("message %q lacks %q", res.Message, c.wantErr)
			}
			if f.binContent() != "OLD-BINARY" || f.restarts != 0 {
				t.Errorf("system was modified: bin=%q restarts=%d", f.binContent(), f.restarts)
			}
			if _, err := os.Stat(f.bin + ".old"); err == nil {
				t.Error(".old created despite refusal")
			}
			if st := ReadState(f.data); st.Result == nil || st.Request != nil {
				t.Errorf("state %+v", st)
			}
		})
	}
}

func (f *fixture) staged(name string) string { return filepath.Join(f.data, DirName, StagedDir, name) }

// signBinary replaces the staged binary and re-signs the sums, as an attacker
// holding an older genuinely signed release would.
func (f *fixture) signBinary(content string) {
	asset := release.ServerAsset(arch)
	sums := []byte(release.SHA256Hex([]byte(content)) + "  " + asset + "\n")
	os.WriteFile(f.staged(asset), []byte(content), 0o640)
	os.WriteFile(f.staged(SumsName), sums, 0o640)
	os.WriteFile(f.staged(SigName), []byte(release.Sign(f.priv, sums)), 0o640)
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func TestApplyRefusesSymlinkedStagedFile(t *testing.T) {
	for _, name := range []string{release.ServerAsset(arch), SumsName, SigName} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.stage("0.9.1")
			// A perfectly valid copy elsewhere in the data dir: even though
			// its content would verify, the symlink itself is refused.
			decoy := filepath.Join(f.data, "decoy")
			b, _ := os.ReadFile(f.staged(name))
			os.WriteFile(decoy, b, 0o640)
			os.Remove(f.staged(name))
			symlinkOrSkip(t, decoy, f.staged(name))
			res, err := f.apply()
			if err == nil || res.Outcome != OutcomeFailed || !strings.Contains(res.Message, "symbolic link") {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if f.binContent() != "OLD-BINARY" {
				t.Error("binary modified")
			}
		})
	}
}

func TestApplyRefusesStagedSymlinkEscapingDataDir(t *testing.T) {
	f := newFixture(t)
	f.stage("0.9.1")
	secret := filepath.Join(t.TempDir(), "shadow")
	os.WriteFile(secret, []byte("root:secret"), 0o600)
	os.Remove(f.staged(SumsName))
	symlinkOrSkip(t, secret, f.staged(SumsName))
	res, err := f.apply()
	if err == nil || res.Outcome != OutcomeFailed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestApplyRefusesSymlinkedStagedDirectory(t *testing.T) {
	f := newFixture(t)
	f.stage("0.9.1")
	outside := t.TempDir()
	for _, n := range []string{release.ServerAsset(arch), SumsName, SigName} {
		b, _ := os.ReadFile(f.staged(n))
		os.WriteFile(filepath.Join(outside, n), b, 0o640)
	}
	os.RemoveAll(filepath.Join(f.data, DirName, StagedDir))
	symlinkOrSkip(t, outside, filepath.Join(f.data, DirName, StagedDir))
	res, err := f.apply()
	if err == nil || res.Outcome != OutcomeFailed || f.binContent() != "OLD-BINARY" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestApplyRefusesSymlinkedUpdateDirectory(t *testing.T) {
	f := newFixture(t)
	f.stage("0.9.1")
	outside := t.TempDir()
	os.Rename(filepath.Join(f.data, DirName), filepath.Join(outside, "update"))
	symlinkOrSkip(t, filepath.Join(outside, "update"), filepath.Join(f.data, DirName))
	if _, err := f.apply(); err == nil {
		t.Fatal("update dir escaping the data directory was accepted")
	}
	if f.binContent() != "OLD-BINARY" {
		t.Error("binary modified")
	}
	if _, err := os.Stat(filepath.Join(outside, "update", ResultFile)); err == nil {
		t.Error("wrote through the symlink")
	}
}

// A planted symlink at result.json must be replaced, never written through.
func TestApplyDoesNotWriteThroughPlantedResultSymlink(t *testing.T) {
	f := newFixture(t)
	f.stage("0.9.1")
	victim := filepath.Join(f.data, "victim")
	os.WriteFile(victim, []byte("precious"), 0o600)
	for _, n := range []string{ResultFile, StatusFile} {
		symlinkOrSkip(t, victim, filepath.Join(f.data, DirName, n))
	}
	if res, err := f.apply(); err != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("victim overwritten: %q", b)
	}
	fi, err := os.Lstat(filepath.Join(f.data, DirName, ResultFile))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("result.json still a symlink: %v %v", fi, err)
	}
}

func TestApplyIgnoresRequestedAssetName(t *testing.T) {
	// The updater derives the asset from its own architecture; a request
	// naming another file must not redirect it.
	f := newFixture(t)
	f.stage("0.9.1")
	WriteRequest(f.data, Request{Version: "0.9.1", Asset: "../../etc/passwd"})
	if res, err := f.apply(); err != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestParseVersionOutputAndListen(t *testing.T) {
	if parseVersionOutput("sentrel-server 0.9.1-dev (abc, 2026)\n") != "0.9.1-dev" || parseVersionOutput("") != "" || parseVersionOutput("x") != "" {
		t.Error("parseVersionOutput")
	}
	u := filepath.Join(t.TempDir(), "u.service")
	os.WriteFile(u, []byte("[Service]\nExecStart=/usr/local/bin/sentrel-server serve --data-dir /d --listen 10.0.0.5:9090\n"), 0o644)
	if ListenFromUnit(u) != "10.0.0.5:9090" {
		t.Errorf("listen %q", ListenFromUnit(u))
	}
	os.WriteFile(u, []byte("ExecStart=/x serve --listen=0.0.0.0:80\n"), 0o644)
	if ListenFromUnit(u) != "0.0.0.0:80" {
		t.Errorf("listen= %q", ListenFromUnit(u))
	}
	if ListenFromUnit(filepath.Join(t.TempDir(), "none")) != "" {
		t.Error("missing unit")
	}
}
