package agent

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

// DUEntry is an aggregated directory size.
type DUEntry struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Depth int    `json:"depth"`
}

// DUFile is a single large file.
type DUFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// DUResult is the disk_usage job result.
type DUResult struct {
	Path         string    `json:"path"`
	MaxDepth     int       `json:"max_depth"`
	TotalBytes   int64     `json:"total_bytes"` // filesystem size
	UsedBytes    int64     `json:"used_bytes"`  // filesystem used
	ScannedBytes int64     `json:"scanned_bytes"`
	Files        int64     `json:"files"`
	Dirs         int64     `json:"dirs"`
	Errors       int64     `json:"errors"`
	Truncated    bool      `json:"truncated"` // stopped at the time limit
	Entries      []DUEntry `json:"entries"`
	LargestFiles []DUFile  `json:"largest_files"`
}

const duTopFiles = 20

type entryHeap []DUEntry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return h[i].Bytes < h[j].Bytes }
func (h entryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x any)        { *h = append(*h, x.(DUEntry)) }
func (h *entryHeap) Pop() any          { o := *h; x := o[len(o)-1]; *h = o[:len(o)-1]; return x }

type fileHeap []DUFile

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Bytes < h[j].Bytes }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(DUFile)) }
func (h *fileHeap) Pop() any          { o := *h; x := o[len(o)-1]; *h = o[:len(o)-1]; return x }

type duWalker struct {
	ctx      context.Context
	rootDev  uint64
	maxDepth int
	top      int
	entries  entryHeap
	files    fileHeap
	links    map[[2]uint64]struct{}
	res      DUResult
}

// DiskUsage walks root without crossing filesystems and returns the biggest
// directories (aggregated up to maxDepth levels) and the biggest files. Only
// bounded heaps are kept in memory. Unreadable entries are counted, not fatal;
// when ctx ends the partial result is returned with Truncated set.
func DiskUsage(ctx context.Context, root string, maxDepth, top int) (*DUResult, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	dev, _, _, _ := statInfo(fi)
	w := &duWalker{ctx: ctx, rootDev: dev, maxDepth: maxDepth, top: top, links: map[[2]uint64]struct{}{}}
	w.res.Path, w.res.MaxDepth = root, maxDepth
	total := w.walk(root, 0)
	w.res.ScannedBytes = total
	if u, err := disk.UsageWithContext(context.Background(), root); err == nil {
		w.res.TotalBytes, w.res.UsedBytes = int64(u.Total), int64(u.Used)
	}
	sort.Slice(w.entries, func(i, j int) bool { return w.entries[i].Bytes > w.entries[j].Bytes })
	sort.Slice(w.files, func(i, j int) bool { return w.files[i].Bytes > w.files[j].Bytes })
	w.res.Entries = append([]DUEntry{}, w.entries...)
	w.res.LargestFiles = append([]DUFile{}, w.files...)
	return &w.res, nil
}

func (w *duWalker) walk(dir string, depth int) int64 {
	w.res.Dirs++
	f, err := os.Open(dir)
	if err != nil {
		w.res.Errors++
		return 0
	}
	defer f.Close()
	var total int64
	for {
		ents, err := f.ReadDir(512)
		for _, e := range ents {
			if w.ctx.Err() != nil {
				w.res.Truncated = true
				return total
			}
			full := filepath.Join(dir, e.Name())
			switch t := e.Type(); {
			case t&os.ModeSymlink != 0:
				continue
			case t.IsDir():
				if skipDiskDir(full) {
					continue
				}
				info, err := e.Info()
				if err != nil {
					w.res.Errors++
					continue
				}
				if dev, _, _, _ := statInfo(info); dev != w.rootDev {
					continue // another filesystem (du -x)
				}
				total += w.walk(full, depth+1)
			case t.IsRegular():
				info, err := e.Info()
				if err != nil {
					w.res.Errors++
					continue
				}
				dev, size, ino, nlink := statInfo(info)
				if nlink > 1 {
					k := [2]uint64{dev, ino}
					if _, seen := w.links[k]; seen {
						continue
					}
					w.links[k] = struct{}{}
				}
				w.res.Files++
				total += size
				w.addFile(full, size)
			}
		}
		if err != nil { // io.EOF or a read error
			if !errors.Is(err, io.EOF) {
				w.res.Errors++
			}
			break
		}
	}
	if depth >= 1 && depth <= w.maxDepth {
		w.addEntry(DUEntry{Path: dir, Bytes: total, Depth: depth})
	}
	return total
}

func (w *duWalker) addFile(p string, size int64) {
	if len(w.files) < duTopFiles {
		heap.Push(&w.files, DUFile{Path: p, Bytes: size})
	} else if size > w.files[0].Bytes {
		w.files[0] = DUFile{Path: p, Bytes: size}
		heap.Fix(&w.files, 0)
	}
}

func (w *duWalker) addEntry(e DUEntry) {
	if len(w.entries) < w.top {
		heap.Push(&w.entries, e)
	} else if e.Bytes > w.entries[0].Bytes {
		w.entries[0] = e
		heap.Fix(&w.entries, 0)
	}
}

// skipDiskDir lists virtual trees that are never worth walking.
func skipDiskDir(p string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	switch p {
	case "/proc", "/sys", "/dev":
		return true
	}
	return false
}

// parseDiskUsageArgs validates `priv disk-usage PATH DEPTH TOP SECONDS`.
func parseDiskUsageArgs(args []string) (path string, depth, top, secs int, err error) {
	if len(args) != 4 {
		return "", 0, 0, 0, errors.New("usage: disk-usage PATH DEPTH TOP SECONDS")
	}
	if path, err = normalizeDiskPath(args[0]); err != nil {
		return
	}
	if depth, err = strconv.Atoi(args[1]); err != nil || depth < 1 || depth > 8 {
		return "", 0, 0, 0, fmt.Errorf("invalid depth %q (1-8)", args[1])
	}
	if top, err = strconv.Atoi(args[2]); err != nil || top < 1 || top > 500 {
		return "", 0, 0, 0, fmt.Errorf("invalid top %q (1-500)", args[2])
	}
	if secs, err = strconv.Atoi(args[3]); err != nil || secs < 5 || secs > 4*3600 {
		return "", 0, 0, 0, fmt.Errorf("invalid time limit %q (5-14400 seconds)", args[3])
	}
	return path, depth, top, secs, nil
}

// normalizeDiskPath returns a cleaned absolute path or an error.
func normalizeDiskPath(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || len(p) > 4096 {
		return "", errors.New("invalid path")
	}
	if runtime.GOOS == "windows" {
		if len(p) == 2 && p[1] == ':' {
			p += `\`
		}
		if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
			return "", fmt.Errorf("path %q must be absolute (e.g. C:\\)", p)
		}
		return filepath.Clean(p), nil
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path %q must be absolute", p)
	}
	return filepath.Clean(p), nil
}

// isMountPoint reports whether path equals one of the mount points (or "/").
func isMountPoint(path string, mounts []string) bool {
	norm := func(s string) string {
		if runtime.GOOS == "windows" {
			// filepath.Clean("C:") is "C:." on Windows, so compare a bare
			// volume ("C:", "c:\") by its volume name.
			if v := filepath.VolumeName(s); v != "" && strings.Trim(s[len(v):], `\/`) == "" {
				return strings.ToLower(v)
			}
			return strings.ToLower(strings.TrimRight(filepath.Clean(s), `\/`))
		}
		return strings.TrimRight(filepath.Clean(s), `\/`)
	}
	want := norm(path)
	if runtime.GOOS != "windows" && want == "" {
		return true // "/"
	}
	for _, m := range mounts {
		if m != "" && norm(m) == want {
			return true
		}
	}
	return false
}

// requireMountPoint restricts the root-run disk walk to filesystem roots.
func requireMountPoint(ctx context.Context, path string) error {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return fmt.Errorf("list mount points: %w", err)
	}
	mounts := make([]string, 0, len(parts))
	for _, p := range parts {
		mounts = append(mounts, p.Mountpoint)
	}
	if !isMountPoint(path, mounts) {
		return fmt.Errorf("%q is not a mount point; disk usage can only scan a mount point", path)
	}
	return nil
}

// privDiskUsage implements the privileged `disk-usage` verb: JSON on w.
func privDiskUsage(ctx context.Context, w io.Writer, args []string) error {
	path, depth, top, secs, err := parseDiskUsageArgs(args)
	if err != nil {
		return err
	}
	if err := requireMountPoint(ctx, path); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(secs)*time.Second)
	defer cancel()
	res, err := DiskUsage(cctx, path, depth, top)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(res)
}

// jobDiskUsage runs the walk through the privileged helper and returns the
// JSON result for the server.
func (a *Agent) jobDiskUsage(ctx context.Context, timeout time.Duration, params map[string]string, w *jobWriter) ([]byte, error) {
	path := params["path"]
	if path == "" {
		path = params["mount"]
	}
	if path == "" {
		path = "/"
		if runtime.GOOS == "windows" {
			path = `C:\`
		}
	}
	depth, top := "3", "30"
	if v := params["depth"]; v != "" {
		depth = v
	}
	if v := params["top"]; v != "" {
		top = v
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	secs := int(timeout.Seconds()) - 20
	if secs < 10 {
		secs = 10
	}
	args := []string{path, depth, top, strconv.Itoa(secs)}
	if _, _, _, _, err := parseDiskUsageArgs(args); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := a.privOut(ctx, timeout, &buf, "disk-usage", args...); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(truncateString(buf.String(), 500)))
	}
	b := buf.Bytes()
	i, j := bytes.IndexByte(b, '{'), bytes.LastIndexByte(b, '}')
	if i < 0 || j < i {
		return nil, fmt.Errorf("unexpected helper output: %s", truncateString(strings.TrimSpace(buf.String()), 300))
	}
	var res DUResult
	if err := json.Unmarshal(b[i:j+1], &res); err != nil {
		return nil, err
	}
	w.Printf("scanned %s in %d files and %d directories (%d unreadable)%s", humanBytes(res.ScannedBytes), res.Files, res.Dirs, res.Errors,
		map[bool]string{true: "; stopped at the time limit", false: ""}[res.Truncated])
	for _, e := range res.Entries {
		if len(e.Path) > 0 {
			w.Printf("%10s  %s", humanBytes(e.Bytes), e.Path)
		}
	}
	return b[i : j+1], nil
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
