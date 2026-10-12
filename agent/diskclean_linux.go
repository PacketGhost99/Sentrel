//go:build linux

package agent

import (
	"context"
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
		return cleanOldKernels(ctx, w, dry)
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

// cleanOldKernels removes every installed kernel except the running one and
// the newest, so a fallback always remains. Dry runs report the sizes.
func cleanOldKernels(ctx context.Context, w io.Writer, dry bool) (int64, error) {
	switch mgr := packageManager(); mgr {
	case "apt", "dnf", "yum":
		return removeOldKernels(ctx, w, realKernelDeps(), dry)
	default:
		return 0, fmt.Errorf("%w: not supported with %q", errSkipTask, mgr)
	}
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
