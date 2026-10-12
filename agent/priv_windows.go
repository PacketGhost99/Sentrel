//go:build windows

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	pb "heartbeat/gen/heartbeatv1"
)

// On Windows the agent runs as LocalSystem, so privileged verbs run
// in-process with the same validation as the Linux helper.
func newPrivRunner() (privRunner, privStreamer) {
	stream := func(ctx context.Context, timeout time.Duration, w io.Writer, verb string, args ...string) error {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return privExec(cctx, w, verb, args)
	}
	run := func(ctx context.Context, timeout time.Duration, verb string, args ...string) ([]byte, error) {
		var b bytes.Buffer
		err := stream(ctx, timeout, &b, verb, args...)
		return b.Bytes(), err
	}
	return run, stream
}

// PrivMain allows running verbs manually from an elevated prompt.
func PrivMain(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: heartbeat-agent priv <verb> [args]")
	}
	if !isAdmin() {
		return errors.New("priv must run elevated")
	}
	return privExec(context.Background(), os.Stdout, args[0], args[1:])
}

func privExec(ctx context.Context, w io.Writer, verb string, rest []string) error {
	switch verb {
	case "pkg-refresh":
		return nil // Windows Update searches online each time
	case "pkg-upgrade":
		o, err := parseUpgradeArgs(rest)
		if err != nil {
			return err
		}
		_, err = installUpdates(ctx, 3*time.Hour, o, func(s string) { fmt.Fprintln(w, s) })
		return err
	case "block-ip":
		if len(rest) != 2 {
			return errors.New("usage: block-ip IP MINUTES")
		}
		ip, err := validIP(rest[0])
		if err != nil {
			return err
		}
		min, err := validMinutes(rest[1])
		if err != nil {
			return err
		}
		return winBlock(ctx, w, ip, min)
	case "unblock-ip":
		if len(rest) != 1 {
			return errors.New("usage: unblock-ip IP")
		}
		ip, err := validIP(rest[0])
		if err != nil {
			return err
		}
		return winUnblock(ctx, w, ip)
	case "list-blocks":
		b, err := winListBlocks(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(b)
	case "fail2ban-status":
		b, _ := protojson.Marshal(&pb.Fail2Ban{})
		_, err := w.Write(b)
		return err
	case "disk-usage":
		return privDiskUsage(ctx, w, rest)
	case "disk-cleanup":
		return privDiskCleanup(ctx, w, rest)
	case "install-update":
		if len(rest) != 4 {
			return errors.New("usage: install-update PATH SHA256 VERSION SIGNATURE")
		}
		return winInstallUpdate(rest[0], rest[1], rest[2], rest[3])
	case "rollback-update":
		return winRollback()
	}
	return fmt.Errorf("unknown priv verb %q", verb)
}

// BlockEntry describes an active block.
type BlockEntry struct {
	IP      string `json:"ip"`
	Expires int64  `json:"expires"`
	Backend string `json:"backend"`
}

var blocksMu sync.Mutex

func blocksFile() string { return filepath.Join(DefaultStateDir(), "blocks.json") }

func loadBlocks() map[string]int64 {
	m := map[string]int64{}
	b, err := os.ReadFile(blocksFile())
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func saveBlocks(m map[string]int64) {
	b, _ := json.Marshal(m)
	_ = os.WriteFile(blocksFile(), b, 0o600)
}

func ruleName(ip string) string { return "Heartbeat block " + ip }

func winBlock(ctx context.Context, w io.Writer, ip string, minutes int) error {
	blocksMu.Lock()
	defer blocksMu.Unlock()
	_, _ = runOut(ctx, 30*time.Second, "netsh", "advfirewall", "firewall", "delete", "rule", "name="+ruleName(ip))
	out, err := runOut(ctx, 30*time.Second, "netsh", "advfirewall", "firewall", "add", "rule", "name="+ruleName(ip), "dir=in", "action=block",
		"remoteip="+ip, "description=Added by Heartbeat agent; expires "+time.Now().Add(time.Duration(minutes)*time.Minute).UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("netsh: %v: %s", err, out)
	}
	m := loadBlocks()
	m[ip] = time.Now().Add(time.Duration(minutes) * time.Minute).Unix()
	saveBlocks(m)
	fmt.Fprintf(w, "blocked %s for %d minutes (Windows Firewall)\n", ip, minutes)
	return nil
}

func winUnblock(ctx context.Context, w io.Writer, ip string) error {
	blocksMu.Lock()
	defer blocksMu.Unlock()
	_, _ = runOut(ctx, 30*time.Second, "netsh", "advfirewall", "firewall", "delete", "rule", "name="+ruleName(ip))
	m := loadBlocks()
	delete(m, ip)
	saveBlocks(m)
	fmt.Fprintf(w, "unblocked %s\n", ip)
	return nil
}

func winListBlocks(ctx context.Context) ([]BlockEntry, error) {
	blocksMu.Lock()
	m := loadBlocks()
	blocksMu.Unlock()
	now := time.Now().Unix()
	var out []BlockEntry
	for ip, exp := range m {
		if exp < now {
			_ = winUnblock(ctx, io.Discard, ip)
			continue
		}
		out = append(out, BlockEntry{IP: ip, Expires: exp, Backend: "windows-firewall"})
	}
	return out, nil
}

func installedPath() string {
	p, _ := os.Executable()
	return p
}

// winInstallUpdate swaps the running executable: Windows allows renaming a
// running binary, so the current one becomes .old and the new one takes its
// place; the service then restarts.
func winInstallUpdate(path, sha, version, sig string) error {
	cfgDir := filepath.Dir(DefaultConfigPath())
	pub, err := readJobKey(filepath.Join(cfgDir, "job.pub"))
	if err != nil {
		return err
	}
	// The update must sit in the protected staging directory (only SYSTEM and
	// Administrators can write there). Copy it to a file of our own, verify
	// that copy, and install from it, so nothing can change between the
	// check and the swap.
	state := DefaultStateDir()
	abs, err := filepath.Abs(path)
	if err != nil || !isStagingDir(state, filepath.Dir(abs)) {
		return fmt.Errorf("update file must be inside %s", filepath.Join(state, "update-stage"))
	}
	if fi, err := os.Lstat(abs); err != nil || !fi.Mode().IsRegular() {
		return errors.New("update file must be a regular file")
	}
	verified := filepath.Join(filepath.Dir(abs), "verified.exe")
	if err := copyFile(abs, verified, 0o700); err != nil {
		return err
	}
	defer os.Remove(verified)
	if err := verifyUpdateFile(verified, sha, version, sig, pub, runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	exe := installedPath()
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("rename current binary: %w", err)
	}
	if err := copyFile(verified, exe, 0o755); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	// Drop any explicit ACL: the binary inherits Program Files' ACL.
	if err := resetInheritedDACL(exe); err != nil {
		_ = os.Remove(exe)
		_ = os.Rename(old, exe)
		return err
	}
	m := &updateMarker{From: AgentVersion(), To: version, At: time.Now().Unix()}
	return writeMarker(state, m)
}

func winRollback() error {
	exe := installedPath()
	old := exe + ".old"
	if _, err := os.Stat(old); err != nil {
		return errors.New("no previous binary to roll back to")
	}
	bad := exe + ".bad-" + strconv.FormatInt(time.Now().Unix(), 10)
	if err := os.Rename(exe, bad); err != nil {
		return err
	}
	if err := os.Rename(old, exe); err != nil {
		_ = os.Rename(bad, exe)
		return err
	}
	_ = os.Remove(markerPath(DefaultStateDir()))
	return nil
}
