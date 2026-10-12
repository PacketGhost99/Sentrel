//go:build linux

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// cleanupTaskIDs is the fixed allowlist of Linux cleanup tasks.
var cleanupTaskIDs = []string{"pkg_cache", "journal", "old_kernels", "tmp", "docker", "logs"}

const (
	journalKeepBytes = 200 << 20
	tmpMaxAge        = 10 * 24 * time.Hour
	logMaxAge        = 7 * 24 * time.Hour
)

var rotatedLogRe = regexp.MustCompile(`\.(gz|xz|bz2|zst|old|[0-9]+)$`)

func isRotatedLog(name string) bool { return rotatedLogRe.MatchString(name) }

func runCleanupTask(ctx context.Context, w io.Writer, task string, dry bool) (int64, error) {
	switch task {
	case "pkg_cache":
		return cleanPkgCache(ctx, w, dry)
	case "journal":
		return cleanJournal(ctx, w, dry)
	case "old_kernels":
		return cleanAutoremove(ctx, w, dry)
	case "tmp":
		var total int64
		for _, d := range []string{"/tmp", "/var/tmp"} {
			n, files, errs := sweep(ctx, d, sweepOpts{Cutoff: time.Now().Add(-tmpMaxAge), DryRun: dry, RemoveDirs: true,
				SkipDir: func(p string) bool { return strings.HasPrefix(filepath.Base(p), "systemd-private-") }})
			fmt.Fprintf(w, "%s: %d files, %s (%d errors)\n", d, files, humanBytes(n), errs)
			total += n
		}
		return total, nil
	case "logs":
		n, files, errs := sweep(ctx, "/var/log", sweepOpts{Cutoff: time.Now().Add(-logMaxAge), DryRun: dry, Match: isRotatedLog,
			SkipDir: func(p string) bool { return p == "/var/log/journal" }})
		fmt.Fprintf(w, "/var/log: %d files, %s (%d errors)\n", files, humanBytes(n), errs)
		return n, nil
	case "docker":
		return cleanDocker(ctx, w, dry)
	}
	return 0, fmt.Errorf("unknown task %q", task)
}

func dirBytes(ctx context.Context, dirs ...string) int64 {
	var t int64
	for _, d := range dirs {
		n, _, _ := sweep(ctx, d, sweepOpts{DryRun: true})
		t += n
	}
	return t
}

func cleanPkgCache(ctx context.Context, w io.Writer, dry bool) (int64, error) {
	mgr := packageManager()
	var dirs []string
	var cmd []string
	switch mgr {
	case "apt":
		dirs, cmd = []string{"/var/cache/apt/archives"}, []string{"apt-get", "clean"}
	case "dnf":
		dirs, cmd = []string{"/var/cache/dnf", "/var/cache/libdnf5"}, []string{"dnf", "clean", "all"}
	case "yum":
		dirs, cmd = []string{"/var/cache/yum"}, []string{"yum", "clean", "all"}
	case "zypper":
		dirs, cmd = []string{"/var/cache/zypp/packages"}, []string{"zypper", "clean", "--all"}
	case "pacman":
		dirs, cmd = []string{"/var/cache/pacman/pkg"}, []string{"pacman", "-Sc", "--noconfirm"}
	case "apk":
		dirs, cmd = []string{"/var/cache/apk"}, []string{"apk", "cache", "clean"}
	default:
		return 0, fmt.Errorf("%w: no supported package manager", errSkipTask)
	}
	before := dirBytes(ctx, dirs...)
	if dry {
		fmt.Fprintf(w, "package cache (%s): %s\n", mgr, humanBytes(before))
		return before, nil
	}
	if err := runLogged(ctx, w, nil, cmd[0], cmd[1:]...); err != nil {
		return 0, err
	}
	return before - dirBytes(ctx, dirs...), nil
}

func cleanJournal(ctx context.Context, w io.Writer, dry bool) (int64, error) {
	dirs := []string{"/var/log/journal", "/run/log/journal"}
	if _, err := exec.LookPath("journalctl"); err != nil {
		return 0, fmt.Errorf("%w: journalctl not installed", errSkipTask)
	}
	before := dirBytes(ctx, dirs...)
	if dry {
		est := before - journalKeepBytes
		if est < 0 {
			est = 0
		}
		fmt.Fprintf(w, "journal uses %s; vacuuming keeps at most 200M and 14 days\n", humanBytes(before))
		return est, nil
	}
	if err := runLogged(ctx, w, nil, "journalctl", "--vacuum-size=200M", "--vacuum-time=14d"); err != nil {
		return 0, err
	}
	return before - dirBytes(ctx, dirs...), nil
}

var (
	aptRemvRe = regexp.MustCompile(`(?m)^Remv (\S+)`)
	aptEnv    = []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "APT_LISTCHANGES_FRONTEND=none"}
)

func cleanAutoremove(ctx context.Context, w io.Writer, dry bool) (int64, error) {
	mgr := packageManager()
	switch mgr {
	case "apt":
		if dry {
			var buf bytes.Buffer
			cmd := exec.CommandContext(ctx, "apt-get", "-s", "autoremove", "--purge")
			cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
			cmd.Stdout = &buf
			if err := cmd.Run(); err != nil {
				return 0, err
			}
			var kib int64
			var names []string
			for _, m := range aptRemvRe.FindAllStringSubmatch(buf.String(), -1) {
				if !pkgNameRe.MatchString(m[1]) {
					continue
				}
				names = append(names, m[1])
				out, _ := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${Installed-Size}", m[1]).Output()
				var n int64
				fmt.Sscan(strings.TrimSpace(string(out)), &n)
				kib += n
			}
			fmt.Fprintf(w, "%d package(s) would be removed: %s\n", len(names), strings.Join(names, " "))
			return kib * 1024, nil
		}
		before := freeBytes("/")
		if err := runLogged(ctx, w, aptEnv, "apt-get", "-q", "-y", "autoremove", "--purge"); err != nil {
			return 0, err
		}
		return freeBytes("/") - before, nil
	case "dnf", "yum":
		if dry {
			fmt.Fprintf(w, "previewing with %s autoremove --assumeno\n", mgr)
			err := runLogged(ctx, w, nil, mgr, "autoremove", "--assumeno")
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				err = nil // --assumeno exits 1 when it would have made changes
			}
			return 0, err
		}
		before := freeBytes("/")
		if err := runLogged(ctx, w, nil, mgr, "-y", "autoremove"); err != nil {
			return 0, err
		}
		return freeBytes("/") - before, nil
	}
	return 0, fmt.Errorf("%w: not supported with %q", errSkipTask, mgr)
}

func cleanDocker(ctx context.Context, w io.Writer, dry bool) (int64, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return 0, fmt.Errorf("%w: docker not installed", errSkipTask)
	}
	if dry {
		// Dangling images, plus stopped containers and build cache from docker system df.
		var est int64
		out, _ := exec.CommandContext(ctx, "docker", "images", "--filter", "dangling=true", "--format", "{{.Size}}").Output()
		for _, l := range strings.Split(string(out), "\n") {
			est += parseHumanSize(l)
		}
		df, err := exec.CommandContext(ctx, "docker", "system", "df", "--format", "{{.Type}}|{{.Reclaimable}}").Output()
		if err != nil {
			return est, err
		}
		for _, l := range strings.Split(string(df), "\n") {
			typ, rec, ok := strings.Cut(l, "|")
			if ok && (typ == "Containers" || typ == "Build Cache") {
				est += parseHumanSize(strings.Fields(rec + " ")[0])
			}
		}
		fmt.Fprintln(w, "estimate: dangling images, stopped containers and build cache (volumes are never removed)")
		return est, nil
	}
	root := "/var/lib/docker"
	if _, err := os.Stat(root); err != nil {
		root = "/"
	}
	before := freeBytes(root)
	if err := runLogged(ctx, w, nil, "docker", "system", "prune", "-f"); err != nil {
		return 0, err
	}
	return freeBytes(root) - before, nil
}
