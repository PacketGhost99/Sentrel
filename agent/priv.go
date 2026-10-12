package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"sentrel/internal/sigs"
)

// privRunner runs a privileged verb and returns its combined output.
type privRunner func(ctx context.Context, timeout time.Duration, verb string, args ...string) ([]byte, error)

// privStreamer runs a privileged verb streaming output to w.
type privStreamer func(ctx context.Context, timeout time.Duration, w io.Writer, verb string, args ...string) error

var (
	pkgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_:~-]{0,127}$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_~-]{0,63}$`)
)

func validIP(s string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return "", fmt.Errorf("invalid or protected IP %q", s)
	}
	return ip.String(), nil
}

func validMinutes(s string) (int, error) {
	m, err := strconv.Atoi(s)
	if err != nil || m < 1 || m > 60*24*90 {
		return 0, fmt.Errorf("invalid duration %q (1-129600 minutes)", s)
	}
	return m, nil
}

// UpgradeOptions configures a package upgrade.
type UpgradeOptions struct {
	DryRun       bool
	SecurityOnly bool
	Packages     []string
}

func parseUpgradeArgs(args []string) (UpgradeOptions, error) {
	var o UpgradeOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			o.DryRun = true
		case "--security-only":
			o.SecurityOnly = true
		case "--packages":
			if i+1 >= len(args) {
				return o, errors.New("--packages needs a value")
			}
			i++
			for _, p := range strings.Split(args[i], ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if !pkgNameRe.MatchString(p) {
					return o, fmt.Errorf("invalid package name %q", p)
				}
				o.Packages = append(o.Packages, p)
			}
		default:
			return o, fmt.Errorf("unknown upgrade option %q", args[i])
		}
	}
	return o, nil
}

func (o UpgradeOptions) args() []string {
	var a []string
	if o.DryRun {
		a = append(a, "--dry-run")
	}
	if o.SecurityOnly {
		a = append(a, "--security-only")
	}
	if len(o.Packages) > 0 {
		a = append(a, "--packages", strings.Join(o.Packages, ","))
	}
	return a
}

// readJobKey loads the Ed25519 job key from a PEM file.
func readJobKey(path string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil || len(blk.Bytes) != ed25519.PublicKeySize {
		return nil, errors.New("invalid job key file")
	}
	return ed25519.PublicKey(blk.Bytes), nil
}

// verifyUpdateFile checks hash and signature of a downloaded agent binary.
func verifyUpdateFile(path, sha, version, sigB64 string, pub ed25519.PublicKey, goos, goarch string) error {
	if !versionRe.MatchString(version) {
		return fmt.Errorf("invalid version %q", version)
	}
	got, err := sigs.FileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, sha) {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, sha)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return errors.New("bad signature encoding")
	}
	if !sigs.VerifyBinary(pub, goos, goarch, version, strings.ToLower(sha), sig) {
		return errors.New("binary signature verification failed")
	}
	return nil
}

// updateMarker records an in-progress self-update for rollback.
type updateMarker struct {
	From     string `json:"from"`
	To       string `json:"to"`
	At       int64  `json:"at"`
	Attempts int    `json:"attempts"`
	JobID    string `json:"job_id,omitempty"`
}

func markerPath(stateDir string) string { return filepath.Join(stateDir, "update-pending.json") }

func readMarker(stateDir string) (*updateMarker, error) {
	b, err := os.ReadFile(markerPath(stateDir))
	if err != nil {
		return nil, err
	}
	var m updateMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func writeMarker(stateDir string, m *updateMarker) error {
	return writeMarkerOwned(stateDir, m, -1, -1)
}

// writeMarkerOwned writes the marker and, when uid >= 0, hands it to that
// owner. It is used by root-run verbs inside a directory owned by the
// unprivileged agent user, so it must never follow a link planted there: a
// non-regular file at the path is refused, and the content goes to an
// exclusively created temp file (fchown'd by descriptor) that is renamed over
// the marker, so even a racing symlink or hard link is replaced, not written.
func writeMarkerOwned(stateDir string, m *updateMarker, uid, gid int) error {
	b, _ := json.Marshal(m)
	return safeWriteFile(markerPath(stateDir), b, 0o600, uid, gid)
}

// safeWriteFile atomically replaces path without following symlinks. uid < 0
// leaves ownership alone.
func safeWriteFile(path string, data []byte, mode os.FileMode, uid, gid int) error {
	if fi, err := os.Lstat(path); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("refusing to write %s: not a regular file", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var f *os.File
	var tmp string
	for i := 0; ; i++ {
		var rnd [6]byte
		_, _ = rand.Read(rnd[:])
		tmp = fmt.Sprintf("%s.%x.tmp", path, rnd)
		var err error
		// O_EXCL also fails on an existing symlink, dangling or not.
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) || i >= 5 {
			return err
		}
	}
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if uid >= 0 {
		if err := f.Chown(uid, gid); err != nil {
			return fail(err)
		}
	}
	if err := f.Chmod(mode); err != nil && runtime.GOOS != "windows" {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// copyFile copies src to dst with mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return copyReader(in, dst, mode)
}

// copyReader writes r to dst (via a temp file renamed into place) with mode.
func copyReader(in io.Reader, dst string, mode os.FileMode) error {
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp, mode)
	return os.Rename(tmp, dst)
}
