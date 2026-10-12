package app

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"sentrel/server/db"
)

// CleanupTask describes one disk cleanup task an agent can run.
type CleanupTask struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

var linuxCleanupTasks = []CleanupTask{
	{"pkg_cache", "Package cache", "Downloaded package files (apt clean, dnf clean all, ...)."},
	{"journal", "Systemd journal", "Vacuum the journal to 200 MB and 14 days."},
	{"old_kernels", "Unused packages and old kernels", "Remove packages no longer needed (apt autoremove --purge, dnf autoremove)."},
	{"tmp", "Temporary files", "Files in /tmp and /var/tmp not modified for 10 days."},
	{"docker", "Docker unused data", "Stopped containers, dangling images and build cache. Volumes are never removed."},
	{"logs", "Rotated logs", "Rotated or compressed logs in /var/log (*.gz, *.1, *.old) older than 7 days."},
}

var windowsCleanupTasks = []CleanupTask{
	{"tmp", "Temporary files", "Files older than 7 days in the service account temp folder and C:\\Windows\\Temp."},
	{"windows_update", "Windows Update downloads", "Downloaded update files in C:\\Windows\\SoftwareDistribution\\Download."},
	{"recycle", "Recycle Bin", "Empty the recycle bin on all fixed drives."},
	{"component_store", "Component store", "DISM /StartComponentCleanup (superseded system components)."},
}

func cleanupCatalog(goos string) []CleanupTask {
	if goos == "windows" {
		return windowsCleanupTasks
	}
	return linuxCleanupTasks
}

// validateCleanupTasks checks a comma separated task list against the
// catalog for the host OS and returns it de-duplicated.
func validateCleanupTasks(goos, csv string) ([]string, error) {
	allowed := map[string]bool{}
	for _, t := range cleanupCatalog(goos) {
		allowed[t.ID] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(csv, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !allowed[t] {
			return nil, badRequest("unknown cleanup task %q for this host", t)
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, badRequest("choose at least one cleanup task")
	}
	return out, nil
}

var winDriveRe = regexp.MustCompile(`^[A-Za-z]:([\\/].*)?$`)

// normalizeMount returns the canonical mount/path string for a host OS.
func normalizeMount(goos, p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.ContainsRune(p, 0) || len(p) > 1024 {
		return "", badRequest("invalid path")
	}
	if goos == "windows" {
		if !winDriveRe.MatchString(p) {
			return "", badRequest("path must start with a drive letter such as C:")
		}
		p = strings.ToUpper(p[:1]) + p[1:]
		p = strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
		if len(p) == 1 {
			p += ":"
		}
		return p, nil
	}
	if !strings.HasPrefix(p, "/") {
		return "", badRequest("path must be absolute")
	}
	return path.Clean(p), nil
}

func defaultMount(goos string) string {
	if goos == "windows" {
		return "C:"
	}
	return "/"
}

// normalizeDiskUsageParams validates and fills disk_usage job parameters.
func normalizeDiskUsageParams(goos string, params map[string]string) error {
	p := params["path"]
	if p == "" {
		p = params["mount"]
	}
	if p == "" {
		p = defaultMount(goos)
	}
	p, err := normalizeMount(goos, p)
	if err != nil {
		return err
	}
	delete(params, "mount")
	params["path"] = p
	depth, top := 3, 30
	if v := params["depth"]; v != "" {
		if depth, err = strconv.Atoi(v); err != nil || depth < 1 || depth > 6 {
			return badRequest("depth must be 1-6")
		}
	}
	if v := params["top"]; v != "" {
		if top, err = strconv.Atoi(v); err != nil || top < 1 || top > 200 {
			return badRequest("top must be 1-200")
		}
	}
	params["depth"], params["top"] = strconv.Itoa(depth), strconv.Itoa(top)
	return nil
}

// queueDiskUsage creates a disk_usage job and a running report row for it.
func (a *App) queueDiskUsage(ctx context.Context, p *Principal, h *Host, mount string) (*Job, error) {
	params := map[string]string{"path": mount}
	if err := normalizeDiskUsageParams(hostInfo(h).Os, params); err != nil {
		return nil, err
	}
	j, err := a.jobs.Create(ctx, p, JobRequest{HostID: h.ID, Type: "disk_usage", Params: params})
	if err != nil {
		return nil, err
	}
	_, err = a.db.ExecContext(ctx, `INSERT INTO disk_reports (host_id, mount, job_id, status, created_at) VALUES (?,?,?,?,?)`,
		h.ID, params["path"], j.ID, "running", db.Now())
	return j, err
}

// storeDiskResult records the outcome of a finished disk_usage job.
func (a *App) storeDiskResult(ctx context.Context, j *Job, params map[string]string, status, errMsg string, result []byte) {
	st, data := "done", "{}"
	switch {
	case status != "succeeded":
		st = "failed"
	case !json.Valid(result):
		st, errMsg = "failed", "the agent returned no usable result"
	default:
		data = string(result)
	}
	res, err := a.db.ExecContext(ctx, `UPDATE disk_reports SET status=?, error=?, data=? WHERE job_id=?`, st, truncateStr(errMsg, 2000), data, j.ID)
	if err != nil {
		a.log.Warn("disk report", "err", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = a.db.ExecContext(ctx, `INSERT INTO disk_reports (host_id, mount, job_id, status, error, data, created_at) VALUES (?,?,?,?,?,?,?)`,
			j.HostID, params["path"], j.ID, st, truncateStr(errMsg, 2000), data, db.Now())
		if err != nil {
			a.log.Warn("disk report", "err", err)
		}
	}
	a.events.publish(Event{Type: "disk.report", TenantID: j.TenantID, HostID: j.HostID, ID: j.ID})
}

type diskEntry struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Depth int    `json:"depth"`
}

type diskFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// DiskUsageReport is the GET /hosts/{id}/disk-usage response.
type DiskUsageReport struct {
	Mount        string      `json:"mount"`
	Status       string      `json:"status"` // none, running, done, failed
	JobID        string      `json:"job_id"`
	CreatedAt    int64       `json:"created_at"`
	TotalBytes   int64       `json:"total_bytes"`
	UsedBytes    int64       `json:"used_bytes"`
	Entries      []diskEntry `json:"entries"`
	LargestFiles []diskFile  `json:"largest_files"`
	Error        string      `json:"error"`
}

func (a *App) apiGetDiskUsage(c *Ctx) (any, error) {
	h, err := c.visibleHost(c.ID())
	if err != nil {
		return nil, err
	}
	goos := hostInfo(h).Os
	mount := c.Q("mount")
	if mount == "" {
		mount = defaultMount(goos)
	}
	if mount, err = normalizeMount(goos, mount); err != nil {
		return nil, err
	}
	out := DiskUsageReport{Mount: mount, Status: "none", Entries: []diskEntry{}, LargestFiles: []diskFile{}}
	var row struct {
		JobID     string `db:"job_id"`
		Status    string `db:"status"`
		Error     string `db:"error"`
		Data      string `db:"data"`
		CreatedAt int64  `db:"created_at"`
		JobStatus string `db:"job_status"`
		JobError  string `db:"job_error"`
	}
	err = a.db.GetContext(c.ctx(), &row, `SELECT r.job_id, r.status, r.error, r.data, r.created_at, COALESCE(j.status,'') AS job_status, COALESCE(j.error,'') AS job_error
		FROM disk_reports r LEFT JOIN jobs j ON j.id=r.job_id WHERE r.host_id=? AND r.mount=? ORDER BY r.created_at DESC, r.id DESC LIMIT 1`, h.ID, mount)
	if err != nil {
		return out, nil // no report yet
	}
	out.JobID, out.Status, out.CreatedAt, out.Error = row.JobID, row.Status, row.CreatedAt, row.Error
	if row.Status == "running" {
		switch row.JobStatus {
		case "failed", "expired", "cancelled":
			out.Status = "failed"
			out.Error = row.JobError
			if out.Error == "" {
				out.Error = "job " + row.JobStatus
			}
		}
	}
	var data struct {
		TotalBytes   int64       `json:"total_bytes"`
		UsedBytes    int64       `json:"used_bytes"`
		Entries      []diskEntry `json:"entries"`
		LargestFiles []diskFile  `json:"largest_files"`
	}
	if json.Unmarshal([]byte(row.Data), &data) == nil {
		out.TotalBytes, out.UsedBytes = data.TotalBytes, data.UsedBytes
		if data.Entries != nil {
			out.Entries = data.Entries
		}
		if data.LargestFiles != nil {
			out.LargestFiles = data.LargestFiles
		}
	}
	return out, nil
}

type diskUsageReq struct {
	Mount string `json:"mount"`
}

func (a *App) apiPostDiskUsage(c *Ctx) (any, error) {
	h, err := c.visibleHost(c.ID())
	if err != nil {
		return nil, err
	}
	var req diskUsageReq
	if c.R.ContentLength != 0 {
		if err := c.Decode(&req); err != nil {
			return nil, err
		}
	}
	mount := req.Mount
	if mount == "" {
		mount = defaultMount(hostInfo(h).Os)
	}
	j, err := a.queueDiskUsage(c.ctx(), c.P, h, mount)
	if err != nil {
		return nil, err
	}
	return map[string]string{"job_id": j.ID}, nil
}

func (a *App) apiDiskCleanupTasks(c *Ctx) (any, error) {
	h, err := c.visibleHost(c.ID())
	if err != nil {
		return nil, err
	}
	return cleanupCatalog(hostInfo(h).Os), nil
}

var autoDiskMu sync.Mutex

// maybeAutoDiskAnalysis queues a disk_usage job when a disk related alert
// fires on an agent host. At most one analysis per host and mount every 6 hours.
func (a *App) maybeAutoDiskAnalysis(al Alert) {
	switch al.Metric {
	case "disk", "inodes", "disk_fill_hours":
	default:
		return
	}
	if al.HostID == nil || al.Labels == "" || al.Source != "threshold" {
		return
	}
	hostID, mount := *al.HostID, al.Labels
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h, err := a.getHost(ctx, hostID)
		if err != nil || h.Kind != "agent" || h.Revoked || !a.hub.Connected(hostID) {
			return
		}
		m, err := normalizeMount(hostInfo(h).Os, mount)
		if err != nil {
			return
		}
		autoDiskMu.Lock()
		defer autoDiskMu.Unlock()
		var n int
		if err := a.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM disk_reports WHERE host_id=? AND mount=? AND created_at > ?`, hostID, m, db.Now()-6*3600); err != nil || n > 0 {
			return
		}
		j, err := a.queueDiskUsage(ctx, systemPrincipal("system:disk-analysis", h.TenantID), h, m)
		if err != nil {
			a.log.Warn("auto disk analysis", "host_id", hostID, "err", err)
			return
		}
		a.log.Info("auto disk analysis queued", "host_id", hostID, "mount", m, "job", j.ID)
	}()
}

// pruneDiskReports keeps the newest 5 reports per host and mount.
func (a *App) pruneDiskReports(ctx context.Context) {
	_, err := a.db.ExecContext(ctx, `DELETE FROM disk_reports WHERE id IN (
		SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY host_id, mount ORDER BY created_at DESC, id DESC) AS rn FROM disk_reports) WHERE rn > 5)`)
	if err != nil {
		a.log.Warn("prune disk reports", "err", err)
	}
}
