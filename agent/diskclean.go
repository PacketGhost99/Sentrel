package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

// errSkipTask marks a task that does not apply to this host.
var errSkipTask = errors.New("skipped")

// validateCleanupTasks checks a comma separated list against the fixed
// allowlist for this OS (cleanupTaskIDs) and returns it de-duplicated.
func validateCleanupTasks(csv string) ([]string, error) {
	allowed := map[string]bool{}
	for _, id := range cleanupTaskIDs {
		allowed[id] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(csv, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !allowed[t] {
			return nil, fmt.Errorf("unknown cleanup task %q", t)
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no cleanup tasks given")
	}
	return out, nil
}

// privDiskCleanup implements `disk-cleanup [--dry-run] TASKS`. Each task prints
// a line; the last line is freed_bytes=N.
func privDiskCleanup(ctx context.Context, w io.Writer, args []string) error {
	dry := false
	var csv string
	for _, a := range args {
		switch {
		case a == "--dry-run":
			dry = true
		case csv == "" && !strings.HasPrefix(a, "-"):
			csv = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	tasks, err := validateCleanupTasks(csv)
	if err != nil {
		return err
	}
	if dry {
		fmt.Fprintln(w, "DRY RUN: nothing will be deleted")
	}
	var total int64
	var failed []string
	for _, t := range tasks {
		if ctx.Err() != nil {
			failed = append(failed, t)
			fmt.Fprintf(w, "[%s] not run: %v\n", t, ctx.Err())
			continue
		}
		n, err := runCleanupTask(ctx, w, t, dry)
		switch {
		case errors.Is(err, errSkipTask):
			fmt.Fprintf(w, "[%s] %v\n", t, err)
			continue
		case err != nil:
			failed = append(failed, t)
			fmt.Fprintf(w, "[%s] error: %v\n", t, err)
		}
		if n < 0 {
			n = 0
		}
		total += n
		verb := "freed"
		if dry {
			verb = "would free about"
		}
		fmt.Fprintf(w, "[%s] %s %s (%d bytes)\n", t, verb, humanBytes(n), n)
	}
	fmt.Fprintf(w, "freed_bytes=%d\n", total)
	if len(failed) > 0 {
		return fmt.Errorf("task(s) failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// jobDiskCleanup runs the cleanup through the privileged helper.
func (a *Agent) jobDiskCleanup(ctx context.Context, timeout time.Duration, dry bool, params map[string]string, w *jobWriter) error {
	tasks, err := validateCleanupTasks(params["tasks"])
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	args := []string{strings.Join(tasks, ",")}
	if dry {
		args = append([]string{"--dry-run"}, args...)
	}
	return a.privOut(ctx, timeout, w, "disk-cleanup", args...)
}

// sweepOpts controls sweep.
type sweepOpts struct {
	Cutoff     time.Time              // only entries modified before this; zero = everything
	DryRun     bool                   // measure only
	RemoveDirs bool                   // remove emptied subdirectories
	Match      func(name string) bool // optional file name filter
	SkipDir    func(path string) bool // optional directory filter
}

// sweep removes (or measures) regular files below root. It never follows
// symlinks and stays on root's filesystem. All access goes through os.Root,
// so a directory swapped for a symlink mid-walk (e.g. by another user in
// /tmp) cannot redirect deletions outside root. Failures are counted.
func sweep(ctx context.Context, root string, o sweepOpts) (bytes int64, files int, errs int) {
	fi, err := os.Lstat(root)
	if err != nil || !fi.IsDir() {
		return 0, 0, 0
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return 0, 0, 1
	}
	defer r.Close()
	dev, _, _, _ := statInfo(fi)
	var rec func(rel string)
	rec = func(rel string) {
		d, err := r.Open(rel)
		if err != nil {
			errs++
			return
		}
		ents, err := d.ReadDir(-1)
		d.Close()
		if err != nil {
			errs++
			return
		}
		for _, e := range ents {
			if ctx.Err() != nil {
				return
			}
			relp := filepath.Join(rel, e.Name())
			full := filepath.Join(root, relp)
			info, err := r.Lstat(relp)
			if err != nil {
				errs++
				continue
			}
			switch {
			case info.IsDir():
				if d, _, _, _ := statInfo(info); d != dev {
					continue
				}
				if o.SkipDir != nil && o.SkipDir(full) {
					continue
				}
				rec(relp)
				if o.RemoveDirs && !o.DryRun && (o.Cutoff.IsZero() || info.ModTime().Before(o.Cutoff)) {
					_ = r.Remove(relp) // only succeeds when empty
				}
			case info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0:
				if !o.Cutoff.IsZero() && !info.ModTime().Before(o.Cutoff) {
					continue
				}
				if o.Match != nil && !o.Match(e.Name()) {
					continue
				}
				_, size, _, _ := statInfo(info)
				if o.DryRun {
					bytes += size
					files++
					continue
				}
				if err := r.Remove(relp); err != nil {
					errs++
					continue
				}
				bytes += size
				files++
			}
		}
	}
	rec(".")
	return bytes, files, errs
}

// freeBytes returns the free space of the filesystem holding path.
func freeBytes(path string) int64 {
	u, err := disk.Usage(path)
	if err != nil {
		return 0
	}
	return int64(u.Free)
}

// parseHumanSize parses sizes such as "1.2GB", "340kB" or "12 MiB".
func parseHumanSize(s string) int64 {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	mult := float64(1)
	switch strings.ToLower(strings.TrimSpace(s[i:])) {
	case "kb":
		mult = 1e3
	case "mb":
		mult = 1e6
	case "gb":
		mult = 1e9
	case "tb":
		mult = 1e12
	case "kib":
		mult = 1 << 10
	case "mib":
		mult = 1 << 20
	case "gib":
		mult = 1 << 30
	case "tib":
		mult = 1 << 40
	}
	return int64(v * mult)
}
