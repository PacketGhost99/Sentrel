//go:build windows

package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

// cleanupTaskIDs is the fixed allowlist of Windows cleanup tasks.
var cleanupTaskIDs = []string{"tmp", "windows_update", "recycle", "component_store"}

const winTmpMaxAge = 7 * 24 * time.Hour

func systemRoot() string {
	if r := os.Getenv("SystemRoot"); r != "" {
		return r
	}
	return `C:\Windows`
}

func runCleanupTask(ctx context.Context, w io.Writer, task string, dry bool) (int64, error) {
	switch task {
	case "tmp":
		seen := map[string]bool{}
		var total int64
		for _, d := range []string{os.TempDir(), filepath.Join(systemRoot(), "Temp")} {
			k := strings.ToLower(filepath.Clean(d))
			if seen[k] {
				continue
			}
			seen[k] = true
			n, files, errs := sweep(ctx, d, sweepOpts{Cutoff: time.Now().Add(-winTmpMaxAge), DryRun: dry, RemoveDirs: true})
			fmt.Fprintf(w, "%s: %d files, %s (%d locked or unreadable)\n", d, files, humanBytes(n), errs)
			total += n
		}
		return total, nil
	case "windows_update":
		d := filepath.Join(systemRoot(), "SoftwareDistribution", "Download")
		n, files, errs := sweep(ctx, d, sweepOpts{DryRun: dry, RemoveDirs: true})
		fmt.Fprintf(w, "%s: %d files, %s (%d locked or unreadable)\n", d, files, humanBytes(n), errs)
		return n, nil
	case "recycle":
		parts, err := disk.PartitionsWithContext(ctx, false)
		if err != nil {
			return 0, err
		}
		var total int64
		for _, p := range parts {
			d := strings.TrimRight(p.Mountpoint, `\`) + `\$Recycle.Bin`
			if _, err := os.Stat(d); err != nil {
				continue
			}
			n, files, errs := sweep(ctx, d, sweepOpts{DryRun: dry, RemoveDirs: true})
			fmt.Fprintf(w, "%s: %d files, %s (%d locked or unreadable)\n", d, files, humanBytes(n), errs)
			total += n
		}
		return total, nil
	case "component_store":
		dism := filepath.Join(systemRoot(), "System32", "Dism.exe")
		if dry {
			out, err := runOut(ctx, 10*time.Minute, dism, "/Online", "/Cleanup-Image", "/AnalyzeComponentStore")
			w.Write(out)
			fmt.Fprintln(w, "size of the reclaimable component store is not estimated; see the analysis above")
			return 0, err
		}
		drive := os.Getenv("SystemDrive")
		if drive == "" {
			drive = "C:"
		}
		before := freeBytes(drive + `\`)
		out, err := runOut(ctx, 2*time.Hour, dism, "/Online", "/Cleanup-Image", "/StartComponentCleanup")
		w.Write(out)
		if err != nil {
			return 0, err
		}
		return freeBytes(drive+`\`) - before, nil
	}
	return 0, fmt.Errorf("unknown task %q", task)
}
