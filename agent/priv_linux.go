//go:build linux

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	pb "sentrel/gen/sentrelv1"
)

const installedBinary = "/usr/local/bin/sentrel-agent"

// newPrivRunner returns a runner that invokes this binary's `priv` verbs
// through sudo (or directly when already root).
func newPrivRunner() (privRunner, privStreamer) {
	stream := func(ctx context.Context, timeout time.Duration, w io.Writer, verb string, args ...string) error {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		full := append([]string{"priv", verb}, args...)
		var cmd *exec.Cmd
		if os.Geteuid() == 0 {
			cmd = exec.CommandContext(cctx, self, full...)
		} else {
			cmd = exec.CommandContext(cctx, "sudo", append([]string{"-n", self}, full...)...)
		}
		cmd.Stdout = w
		cmd.Stderr = w
		cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
		err = cmd.Run()
		if cctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s", timeout)
		}
		return err
	}
	run := func(ctx context.Context, timeout time.Duration, verb string, args ...string) ([]byte, error) {
		var b bytes.Buffer
		err := stream(ctx, timeout, &b, verb, args...)
		return b.Bytes(), err
	}
	return run, stream
}

// PrivMain implements `sentrel-agent priv <verb> ...`. It runs as root and
// trusts nothing from its caller beyond validated arguments.
func PrivMain(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("priv must run as root (via sudo)")
	}
	if len(args) == 0 {
		return errors.New("usage: sentrel-agent priv <verb> [args]")
	}
	verb, rest := args[0], args[1:]
	ctx := context.Background()
	w := os.Stdout
	switch verb {
	case "pkg-refresh":
		return pkgRefresh(ctx, w)
	case "pkg-upgrade":
		o, err := parseUpgradeArgs(rest)
		if err != nil {
			return err
		}
		return pkgUpgrade(ctx, w, o)
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
		return blockIP(ctx, w, ip, min)
	case "unblock-ip":
		if len(rest) != 1 {
			return errors.New("usage: unblock-ip IP")
		}
		ip, err := validIP(rest[0])
		if err != nil {
			return err
		}
		return unblockIP(ctx, w, ip)
	case "list-blocks":
		blocks, err := listBlocks(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(w).Encode(blocks)
	case "fail2ban-status":
		f := fail2banStatus(ctx)
		b, _ := protojson.Marshal(f)
		_, err := w.Write(b)
		return err
	case "remove-kernel":
		return privRemoveKernel(ctx, w, rest)
	case "disk-usage":
		return privDiskUsage(ctx, w, rest)
	case "disk-cleanup":
		return privDiskCleanup(ctx, w, rest)
	case "install-update":
		if len(rest) != 4 {
			return errors.New("usage: install-update PATH SHA256 VERSION SIGNATURE")
		}
		return installUpdate(rest[0], rest[1], rest[2], rest[3])
	case "rollback-update":
		return rollbackUpdate()
	}
	return fmt.Errorf("unknown priv verb %q", verb)
}

// runLogged runs a command streaming output, inside its own systemd scope
// when possible so it is not limited by (or killed with) the agent's cgroup.
func runLogged(ctx context.Context, w io.Writer, env []string, name string, args ...string) error {
	full := append([]string{name}, args...)
	if p, err := exec.LookPath("systemd-run"); err == nil && isSystemd() {
		unit := fmt.Sprintf("sentrel-job-%d", time.Now().UnixNano()%1e9)
		full = append([]string{p, "--scope", "--quiet", "--collect", "--unit", unit}, full...)
	}
	fmt.Fprintf(w, "$ %s\n", strings.Join(append([]string{name}, args...), " "))
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	cmd.Env = append(append(os.Environ(), "LANG=C", "LC_ALL=C"), env...)
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd.Run()
}

func isSystemd() bool {
	b, err := os.ReadFile("/proc/1/comm")
	return err == nil && strings.TrimSpace(string(b)) == "systemd"
}

func pkgRefresh(ctx context.Context, w io.Writer) error {
	switch packageManager() {
	case "apt":
		return runLogged(ctx, w, nil, "apt-get", "update", "-q")
	case "dnf":
		return runLogged(ctx, w, nil, "dnf", "-q", "makecache")
	case "yum":
		return runLogged(ctx, w, nil, "yum", "-q", "makecache")
	case "zypper":
		return runLogged(ctx, w, nil, "zypper", "--non-interactive", "refresh")
	case "pacman":
		return runLogged(ctx, w, nil, "pacman", "-Sy", "--noconfirm")
	case "apk":
		return runLogged(ctx, w, nil, "apk", "update")
	}
	return errors.New("no supported package manager")
}

func pkgUpgrade(ctx context.Context, w io.Writer, o UpgradeOptions) error {
	mgr := packageManager()
	switch mgr {
	case "apt":
		if err := runLogged(ctx, w, nil, "apt-get", "update", "-q"); err != nil {
			fmt.Fprintln(w, "warning: apt-get update failed:", err)
		}
		env := []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "APT_LISTCHANGES_FRONTEND=none"}
		base := []string{"-q", "-y", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}
		if o.DryRun {
			base = append(base, "-s")
		}
		if len(o.Packages) > 0 {
			return runLogged(ctx, w, env, "apt-get", append(append(base, "install", "--only-upgrade"), o.Packages...)...)
		}
		return runLogged(ctx, w, env, "apt-get", append(base, "upgrade", "--with-new-pkgs")...)
	case "dnf", "yum":
		a := []string{"-y", "upgrade"}
		if o.DryRun {
			a = []string{"upgrade", "--assumeno"}
		}
		if o.SecurityOnly {
			a = append(a, "--security")
		}
		a = append(a, o.Packages...)
		err := runLogged(ctx, w, nil, mgr, a...)
		if o.DryRun {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				return nil // --assumeno exits 1 when it would have made changes
			}
		}
		return err
	case "zypper":
		var a []string
		if o.SecurityOnly && len(o.Packages) == 0 {
			a = []string{"--non-interactive", "patch", "--category", "security"}
		} else {
			a = append([]string{"--non-interactive", "update"}, o.Packages...)
		}
		if o.DryRun {
			a = append(a, "--dry-run")
		}
		return runLogged(ctx, w, nil, "zypper", a...)
	case "pacman":
		if o.DryRun {
			return runLogged(ctx, w, nil, "pacman", "-Qu")
		}
		if len(o.Packages) > 0 {
			return runLogged(ctx, w, nil, "pacman", append([]string{"-S", "--noconfirm", "--needed"}, o.Packages...)...)
		}
		return runLogged(ctx, w, nil, "pacman", "-Syu", "--noconfirm")
	case "apk":
		a := []string{"upgrade"}
		if o.DryRun {
			a = append(a, "--simulate")
		}
		return runLogged(ctx, w, nil, "apk", append(a, o.Packages...)...)
	}
	return errors.New("no supported package manager")
}

// --- firewall ---

// BlockEntry describes an active block.
type BlockEntry struct {
	IP      string `json:"ip"`
	Expires int64  `json:"expires"`
	Backend string `json:"backend"`
}

const nftSetup = `table inet sentrel {
	set blocklist4 { type ipv4_addr; flags timeout; }
	set blocklist6 { type ipv6_addr; flags timeout; }
	chain input {
		type filter hook input priority -10; policy accept;
		ip saddr @blocklist4 drop
		ip6 saddr @blocklist6 drop
	}
}
`

func hasNft() bool { _, err := exec.LookPath("nft"); return err == nil }

func ensureNft(ctx context.Context) error {
	if exec.CommandContext(ctx, "nft", "list", "table", "inet", "sentrel").Run() == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(nftSetup)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft setup: %v: %s", err, out)
	}
	return nil
}

func setFor(ip string) (string, string) {
	if strings.Contains(ip, ":") {
		return "blocklist6", "ip6tables"
	}
	return "blocklist4", "iptables"
}

func blockIP(ctx context.Context, w io.Writer, ip string, minutes int) error {
	set, ipt := setFor(ip)
	if hasNft() {
		if err := ensureNft(ctx); err != nil {
			return err
		}
		_ = exec.CommandContext(ctx, "nft", "delete", "element", "inet", "sentrel", set, "{", ip, "}").Run()
		out, err := exec.CommandContext(ctx, "nft", "add", "element", "inet", "sentrel", set, "{", ip, "timeout", strconv.Itoa(minutes)+"m", "}").CombinedOutput()
		if err != nil {
			return fmt.Errorf("nft: %v: %s", err, out)
		}
		fmt.Fprintf(w, "blocked %s for %d minutes (nftables)\n", ip, minutes)
		return nil
	}
	if _, err := exec.LookPath(ipt); err != nil {
		return errors.New("neither nftables nor iptables is available")
	}
	_ = removeIptables(ctx, ipt, ip)
	exp := time.Now().Add(time.Duration(minutes) * time.Minute).Unix()
	out, err := exec.CommandContext(ctx, ipt, "-I", "INPUT", "-s", ip, "-j", "DROP", "-m", "comment", "--comment", fmt.Sprintf("sentrel-block:%d", exp)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", ipt, err, out)
	}
	fmt.Fprintf(w, "blocked %s for %d minutes (%s)\n", ip, minutes, ipt)
	return nil
}

func unblockIP(ctx context.Context, w io.Writer, ip string) error {
	set, ipt := setFor(ip)
	if hasNft() {
		out, err := exec.CommandContext(ctx, "nft", "delete", "element", "inet", "sentrel", set, "{", ip, "}").CombinedOutput()
		if err != nil && !strings.Contains(string(out), "No such file") && !strings.Contains(string(out), "does not exist") {
			return fmt.Errorf("nft: %v: %s", err, out)
		}
		fmt.Fprintf(w, "unblocked %s\n", ip)
		return nil
	}
	if err := removeIptables(ctx, ipt, ip); err != nil {
		return err
	}
	fmt.Fprintf(w, "unblocked %s\n", ip)
	return nil
}

var iptRule = regexp.MustCompile(`^-A INPUT -s (\S+?)(?:/\d+)? .*sentrel-block:(\d+)`)

func iptablesRules(ctx context.Context, ipt string) ([]string, error) {
	out, err := exec.CommandContext(ctx, ipt, "-S", "INPUT").Output()
	if err != nil {
		return nil, err
	}
	var rules []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "sentrel-block:") {
			rules = append(rules, l)
		}
	}
	return rules, nil
}

func removeIptables(ctx context.Context, ipt, ip string) error {
	rules, err := iptablesRules(ctx, ipt)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if m := iptRule.FindStringSubmatch(r); m != nil && m[1] == ip {
			spec := strings.Fields(strings.Replace(r, "-A INPUT", "-D INPUT", 1))
			if out, err := exec.CommandContext(ctx, ipt, spec...).CombinedOutput(); err != nil {
				return fmt.Errorf("%s: %v: %s", ipt, err, out)
			}
		}
	}
	return nil
}

func listBlocks(ctx context.Context) ([]BlockEntry, error) {
	var out []BlockEntry
	if hasNft() {
		for _, set := range []string{"blocklist4", "blocklist6"} {
			b, err := exec.CommandContext(ctx, "nft", "-j", "list", "set", "inet", "sentrel", set).Output()
			if err != nil {
				continue
			}
			var doc struct {
				Nftables []map[string]json.RawMessage `json:"nftables"`
			}
			if json.Unmarshal(b, &doc) != nil {
				continue
			}
			for _, item := range doc.Nftables {
				raw, ok := item["set"]
				if !ok {
					continue
				}
				var s struct {
					Elem []json.RawMessage `json:"elem"`
				}
				_ = json.Unmarshal(raw, &s)
				for _, e := range s.Elem {
					var plain string
					if json.Unmarshal(e, &plain) == nil {
						out = append(out, BlockEntry{IP: plain, Backend: "nftables"})
						continue
					}
					var obj struct {
						Elem struct {
							Val     string `json:"val"`
							Expires int64  `json:"expires"`
						} `json:"elem"`
					}
					if json.Unmarshal(e, &obj) == nil && obj.Elem.Val != "" {
						out = append(out, BlockEntry{IP: obj.Elem.Val, Expires: time.Now().Unix() + obj.Elem.Expires, Backend: "nftables"})
					}
				}
			}
		}
		return out, nil
	}
	now := time.Now().Unix()
	for _, ipt := range []string{"iptables", "ip6tables"} {
		if _, err := exec.LookPath(ipt); err != nil {
			continue
		}
		rules, _ := iptablesRules(ctx, ipt)
		for _, r := range rules {
			m := iptRule.FindStringSubmatch(r)
			if m == nil {
				continue
			}
			exp, _ := strconv.ParseInt(m[2], 10, 64)
			if exp > 0 && exp < now {
				_ = removeIptables(ctx, ipt, m[1]) // expired
				continue
			}
			out = append(out, BlockEntry{IP: m[1], Expires: exp, Backend: ipt})
		}
	}
	return out, nil
}

// --- fail2ban ---

func fail2banStatus(ctx context.Context) *pb.Fail2Ban {
	f := &pb.Fail2Ban{}
	if _, err := exec.LookPath("fail2ban-client"); err != nil {
		return f
	}
	out, err := exec.CommandContext(ctx, "fail2ban-client", "status").CombinedOutput()
	if err != nil {
		f.Error = strings.TrimSpace(string(out))
		return f
	}
	f.Available = true
	var jails []string
	for _, l := range strings.Split(string(out), "\n") {
		if i := strings.Index(l, "Jail list:"); i >= 0 {
			for _, j := range strings.Split(l[i+len("Jail list:"):], ",") {
				if j = strings.TrimSpace(j); j != "" {
					jails = append(jails, j)
				}
			}
		}
	}
	for _, j := range jails {
		jo, err := exec.CommandContext(ctx, "fail2ban-client", "status", j).CombinedOutput()
		if err != nil {
			continue
		}
		jail := &pb.Fail2BanJail{Name: j}
		s := bufio.NewScanner(bytes.NewReader(jo))
		for s.Scan() {
			l := s.Text()
			_, v, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			n, _ := strconv.Atoi(v)
			switch {
			case strings.Contains(l, "Currently failed"):
				jail.CurrentlyFailed = uint32(n)
			case strings.Contains(l, "Total failed"):
				jail.TotalFailed = uint32(n)
			case strings.Contains(l, "Currently banned"):
				jail.CurrentlyBanned = uint32(n)
			case strings.Contains(l, "Total banned"):
				jail.TotalBanned = uint32(n)
			case strings.Contains(l, "Banned IP list"):
				for _, ip := range strings.Fields(v) {
					if len(jail.BannedIps) < 200 {
						jail.BannedIps = append(jail.BannedIps, ip)
					}
				}
			}
		}
		f.Jails = append(f.Jails, jail)
	}
	return f
}

// --- self-update (root side) ---

func installUpdate(path, sha, version, sig string) error {
	state := DefaultStateDir()
	abs, err := filepath.Abs(path)
	if err != nil || filepath.Dir(abs) != filepath.Clean(state) {
		return fmt.Errorf("update file must be directly inside %s", state)
	}
	// Open without following a final symlink, then check the descriptor.
	src, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open update file: %w", err)
	}
	defer src.Close()
	if fi, err := src.Stat(); err != nil || !fi.Mode().IsRegular() {
		return errors.New("update file must be a regular file")
	}
	keyPath := filepath.Join(filepath.Dir(DefaultConfigPath()), "job.pub")
	kfi, err := os.Stat(keyPath)
	if err != nil {
		return err
	}
	if st, ok := kfi.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 || kfi.Mode().Perm()&0o022 != 0 {
		return errors.New("job key must be root-owned and not group/world writable")
	}
	pub, err := readJobKey(keyPath)
	if err != nil {
		return err
	}
	// Copy first so the agent user cannot swap the file after verification.
	staged := installedBinary + ".new"
	// Staged 0700 until verified so unverified content is never world-readable.
	if err := copyReader(src, staged, 0o700); err != nil {
		return err
	}
	if err := verifyUpdateFile(staged, sha, version, sig, pub, runtime.GOOS, runtime.GOARCH); err != nil {
		os.Remove(staged)
		return err
	}
	if err := exec.Command(staged, "version").Run(); err != nil {
		os.Remove(staged)
		return fmt.Errorf("new binary does not run: %w", err)
	}
	cur, _ := exec.Command(installedBinary, "version", "--short").Output()
	if err := copyFile(installedBinary, installedBinary+".old", 0o755); err != nil {
		os.Remove(staged)
		return err
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		os.Remove(staged)
		return err
	}
	if err := os.Rename(staged, installedBinary); err != nil {
		return err
	}
	m := &updateMarker{From: strings.TrimSpace(string(cur)), To: version, At: time.Now().Unix()}
	if err := writeUpdateMarker(state, m); err != nil {
		return err
	}
	fmt.Printf("installed agent %s (previous kept as %s.old)\n", version, installedBinary)
	return nil
}

// writeUpdateMarker writes the marker as root and hands it to the invoking
// (sudo) user, refusing symlinks in the agent-owned state directory.
func writeUpdateMarker(state string, m *updateMarker) error {
	uid, gid := -1, -1
	if u, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil {
		uid = u
		if g, err := strconv.Atoi(os.Getenv("SUDO_GID")); err == nil {
			gid = g
		} else {
			gid = u
		}
	}
	return writeMarkerOwned(state, m, uid, gid)
}

func rollbackUpdate() error {
	old := installedBinary + ".old"
	if _, err := os.Stat(old); err != nil {
		return errors.New("no previous binary to roll back to")
	}
	if err := os.Rename(old, installedBinary); err != nil {
		return err
	}
	_ = os.Remove(markerPath(DefaultStateDir()))
	fmt.Println("rolled back to the previous agent binary")
	return nil
}
