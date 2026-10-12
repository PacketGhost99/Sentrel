package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	pb "heartbeat/gen/heartbeatv1"
	"heartbeat/internal/sigs"
	"heartbeat/server/db"
)

// JobManager creates, schedules, signs and dispatches jobs.
type JobManager struct {
	app        *App
	wake       chan struct{}
	lastAutoUp time.Time
}

func newJobManager(a *App) *JobManager { return &JobManager{app: a, wake: make(chan struct{}, 1)} }

func (m *JobManager) kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// JobRequest is a request to run a job on a host.
type JobRequest struct {
	HostID   int64             `json:"host_id"`
	Type     string            `json:"type"`
	Params   map[string]string `json:"params"`
	DryRun   bool              `json:"dry_run"`
	Schedule string            `json:"schedule"` // now, at, maintenance
	RunAt    int64             `json:"run_at"`
	Confirm  bool              `json:"confirm"`
	Timeout  int               `json:"timeout_seconds"`
}

var (
	pkgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_:~-]{0,127}$`)
	jobTypes  = map[string]bool{"upgrade": true, "refresh_inventory": true, "block_ip": true, "unblock_ip": true, "self_update": true, "script": true, "collect_now": true,
		"disk_usage": true, "disk_cleanup": true}
)

// ErrConfirmation is returned when a destructive job lacks confirmation.
var ErrConfirmation = errors.New("this action changes the host: confirm it explicitly (confirm=true); consider a dry run first")

// Create validates and stores a job. p is the requesting principal.
func (m *JobManager) Create(ctx context.Context, p *Principal, req JobRequest) (*Job, error) {
	a := m.app
	if !jobTypes[req.Type] {
		return nil, badRequest("unknown job type %q", req.Type)
	}
	h, err := a.getHost(ctx, req.HostID)
	if err != nil || !p.CanTenant(h.TenantID) {
		return nil, errNotFound
	}
	if h.Kind != "agent" || h.Revoked {
		return nil, badRequest("jobs can only run on hosts with an active agent")
	}
	if req.Params == nil {
		req.Params = map[string]string{}
	}
	timeout := req.Timeout
	switch req.Type {
	case "upgrade":
		if !h.AllowUpgrades && !req.DryRun {
			return nil, forbidden("remote upgrades are not enabled for this host (host settings → allow upgrades)")
		}
		if !req.DryRun && !req.Confirm {
			return nil, ErrConfirmation
		}
		if pk := strings.TrimSpace(req.Params["packages"]); pk != "" {
			for _, n := range strings.FieldsFunc(pk, func(r rune) bool { return r == ',' || r == ' ' }) {
				if !pkgNameRe.MatchString(n) {
					return nil, badRequest("invalid package name %q", n)
				}
			}
		}
		if timeout == 0 {
			timeout = 3600
		}
	case "block_ip", "unblock_ip":
		if !h.AllowIPBlock {
			return nil, forbidden("IP blocking is not enabled for this host (host settings → allow IP blocking)")
		}
		if net.ParseIP(req.Params["ip"]) == nil {
			return nil, badRequest("a valid ip is required")
		}
		if req.Type == "block_ip" && !req.Confirm {
			return nil, ErrConfirmation
		}
	case "script":
		if !p.Has(PermScripts) {
			return nil, forbidden("only administrators can run scripts")
		}
		if !h.AllowScripts {
			return nil, forbidden("remote scripts are not enabled for this host (host settings → allow scripts)")
		}
		if strings.TrimSpace(req.Params["script"]) == "" {
			return nil, badRequest("script is required")
		}
		if len(req.Params["script"]) > 64*1024 {
			return nil, badRequest("script too large (64 KiB max)")
		}
		if !req.Confirm {
			return nil, ErrConfirmation
		}
		if timeout == 0 {
			timeout = 600
		}
	case "self_update":
		if !req.Confirm {
			return nil, ErrConfirmation
		}
	case "disk_usage":
		if err := normalizeDiskUsageParams(hostInfo(h).Os, req.Params); err != nil {
			return nil, err
		}
		if timeout == 0 {
			timeout = 300
		}
	case "disk_cleanup":
		if !h.AllowCleanup {
			return nil, forbidden("disk cleanup is not enabled for this host (host settings → allow cleanup)")
		}
		tasks, err := validateCleanupTasks(hostInfo(h).Os, req.Params["tasks"])
		if err != nil {
			return nil, err
		}
		req.Params["tasks"] = strings.Join(tasks, ",")
		if !req.DryRun && !req.Confirm {
			return nil, ErrConfirmation
		}
		if timeout == 0 {
			timeout = 1800
		}
	}
	if timeout <= 0 {
		timeout = 900
	}
	if timeout > 4*3600 {
		timeout = 4 * 3600
	}
	now := db.Now()
	j := &Job{ID: uuid.NewString(), TenantID: h.TenantID, HostID: h.ID, Type: req.Type, DryRun: req.DryRun, Schedule: "now",
		RunAfter: now, TimeoutSeconds: timeout, RequestedBy: p.Name, CreatedAt: now, Status: "queued"}
	if req.Confirm {
		j.ConfirmedBy = p.Name
	}
	switch req.Schedule {
	case "", "now":
		j.ExpiresAt = now + 24*3600
	case "at":
		if req.RunAt <= now {
			return nil, badRequest("run_at must be in the future")
		}
		j.Schedule, j.Status, j.RunAfter, j.ExpiresAt = "at", "scheduled", req.RunAt, req.RunAt+24*3600
	case "maintenance":
		j.Schedule, j.Status, j.ExpiresAt = "maintenance", "scheduled", now+14*24*3600
	default:
		return nil, badRequest("schedule must be now, at or maintenance")
	}
	pj, _ := json.Marshal(req.Params)
	j.Params = JSON(pj)
	_, err = a.db.ExecContext(ctx, `INSERT INTO jobs (id, tenant_id, host_id, type, params, dry_run, status, schedule, run_after, expires_at, timeout_seconds,
		requested_by, confirmed_by, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.TenantID, j.HostID, j.Type, string(pj), j.DryRun, j.Status, j.Schedule, j.RunAfter, j.ExpiresAt, j.TimeoutSeconds, j.RequestedBy, j.ConfirmedBy, j.CreatedAt)
	if err != nil {
		return nil, err
	}
	details := map[string]any{"type": j.Type, "dry_run": j.DryRun, "schedule": j.Schedule, "host": h.Name}
	for k, v := range req.Params {
		if k == "script" {
			v = truncateStr(v, 500)
		}
		details[k] = v
	}
	a.audit(ctx, auditEntry{TenantID: h.TenantID, UserID: p.UserID, Actor: p.Name, Action: "job.created", TargetType: "job", TargetID: j.ID, Details: details, IP: p.IP})
	a.events.publish(Event{Type: "job.updated", TenantID: j.TenantID, HostID: j.HostID, ID: j.ID})
	m.kick()
	return j, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Cancel cancels a job that has not been sent yet.
func (m *JobManager) Cancel(ctx context.Context, p *Principal, id string) error {
	res, err := m.app.db.ExecContext(ctx, `UPDATE jobs SET status='cancelled', finished_at=? WHERE id=? AND status IN ('queued','scheduled')`, db.Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return badRequest("job is not cancellable (already sent or finished)")
	}
	m.app.audit(ctx, auditEntry{TenantID: p.TenantID, UserID: p.UserID, Actor: p.Name, Action: "job.cancelled", TargetType: "job", TargetID: id, IP: p.IP})
	m.app.events.publish(Event{Type: "job.updated", ID: id})
	return nil
}

func (m *JobManager) loop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.wake:
		}
		m.tick(ctx)
	}
}

func (m *JobManager) tick(ctx context.Context) {
	a := m.app
	now := db.Now()
	// Expire.
	if res, err := a.db.ExecContext(ctx, `UPDATE jobs SET status='expired', finished_at=?, error='not delivered before expiry' WHERE status IN ('queued','scheduled') AND expires_at < ?`, now, now); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			_, _ = a.db.ExecContext(ctx, `UPDATE ip_blocks SET status='failed' WHERE status='pending' AND job_id IN (SELECT id FROM jobs WHERE status='expired')`)
		}
	}
	// Timed-out running jobs.
	_, _ = a.db.ExecContext(ctx, `UPDATE jobs SET status='failed', finished_at=?, error='timed out waiting for the agent to report a result'
		WHERE status IN ('sent','running') AND sent_at > 0 AND sent_at + timeout_seconds + 300 < ?`, now, now)
	// Scheduled at a time.
	_, _ = a.db.ExecContext(ctx, `UPDATE jobs SET status='queued' WHERE status='scheduled' AND schedule='at' AND run_after <= ?`, now)
	// Scheduled in maintenance windows.
	var maint []Job
	_ = a.db.SelectContext(ctx, &maint, `SELECT `+jobCols+` FROM jobs j WHERE j.status='scheduled' AND j.schedule='maintenance'`)
	for _, j := range maint {
		if a.maint.hostAllowsJobs(j.HostID) {
			_, _ = a.db.ExecContext(ctx, `UPDATE jobs SET status='queued', run_after=? WHERE id=? AND status='scheduled'`, now, j.ID)
		}
	}
	// Dispatch queued jobs to connected agents.
	var queued []Job
	if err := a.db.SelectContext(ctx, &queued, `SELECT `+jobCols+` FROM jobs j WHERE j.status='queued' AND j.run_after <= ? ORDER BY j.created_at LIMIT 200`, now); err != nil {
		return
	}
	for _, j := range queued {
		if !a.hub.Connected(j.HostID) {
			continue
		}
		if err := m.dispatch(ctx, &j); err != nil {
			a.log.Warn("job dispatch", "job", j.ID, "err", err)
		}
	}
	// Periodic agent auto-update check.
	if time.Since(m.lastAutoUp) >= 10*time.Minute {
		m.lastAutoUp = time.Now()
		m.autoUpdateAll(ctx)
	}
	// Expired IP blocks.
	_, _ = a.db.ExecContext(ctx, `UPDATE ip_blocks SET status='removed' WHERE status IN ('active','pending') AND expires_at < ?`, now)
}

func (m *JobManager) dispatch(ctx context.Context, j *Job) error {
	a := m.app
	h, err := a.getHost(ctx, j.HostID)
	if err != nil || h.AgentID == nil {
		return errors.New("host gone")
	}
	params := map[string]string{}
	_ = json.Unmarshal(j.Params, &params)
	if j.Type == "self_update" {
		if err := m.prepareSelfUpdate(h, params); err != nil {
			_, _ = a.db.ExecContext(ctx, `UPDATE jobs SET status='failed', error=?, finished_at=? WHERE id=?`, err.Error(), db.Now(), j.ID)
			return err
		}
	}
	spec := &pb.JobSpec{Id: j.ID, AgentId: *h.AgentID, Type: j.Type, Params: params, IssuedUnix: time.Now().Unix(),
		ExpiresUnix: time.Now().Add(15 * time.Minute).Unix(), DryRun: j.DryRun, TimeoutSeconds: uint32(j.TimeoutSeconds)}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(spec)
	if err != nil {
		return err
	}
	msg := &pb.ServerMessage{Msg: &pb.ServerMessage_Job{Job: &pb.SignedJob{Spec: b, Signature: a.ca.SignJob(b)}}}
	res, err := a.db.ExecContext(ctx, `UPDATE jobs SET status='sent', sent_at=? WHERE id=? AND status='queued'`, db.Now(), j.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if !a.hub.Send(j.HostID, msg) {
		_, _ = a.db.ExecContext(ctx, `UPDATE jobs SET status='queued', sent_at=0 WHERE id=?`, j.ID)
		return errors.New("agent send queue unavailable")
	}
	a.events.publish(Event{Type: "job.updated", TenantID: j.TenantID, HostID: j.HostID, ID: j.ID})
	return nil
}

const maxJobOutput = 1 << 20

func (m *JobManager) output(ctx context.Context, hostID int64, o *pb.JobOutput) {
	a := m.app
	chunk := string(o.Data)
	_, err := a.db.ExecContext(ctx, `UPDATE jobs SET output = CASE WHEN length(output) < ? THEN output || ? ELSE output END,
		status = CASE WHEN status='sent' THEN 'running' ELSE status END, started_at = CASE WHEN started_at=0 THEN ? ELSE started_at END
		WHERE id=? AND host_id=?`, maxJobOutput, chunk, db.Now(), o.JobId, hostID)
	if err != nil {
		a.log.Error("job output", "err", err)
	}
	a.events.publish(Event{Type: "job.output", HostID: hostID, ID: o.JobId, TenantID: a.hostTenant(ctx, hostID)})
}

func (m *JobManager) result(ctx context.Context, hostID int64, r *pb.JobResult) {
	a := m.app
	var j Job
	if err := a.db.GetContext(ctx, &j, `SELECT `+jobCols+` FROM jobs j WHERE j.id=? AND j.host_id=?`, r.JobId, hostID); err != nil {
		a.log.Warn("result for unknown job", "job", r.JobId, "host_id", hostID)
		return
	}
	status := r.Status
	switch status {
	case "succeeded", "failed", "rejected", "timeout":
	default:
		status = "failed"
	}
	if status == "timeout" {
		status = "failed"
		if r.Error == "" {
			r.Error = "timed out on the agent"
		}
	}
	started := r.StartedMs / 1000
	if started == 0 {
		started = db.Now()
	}
	_, err := a.db.ExecContext(ctx, `UPDATE jobs SET status=?, exit_code=?, error=?, started_at=CASE WHEN started_at=0 THEN ? ELSE started_at END, finished_at=?, result_json=? WHERE id=?`,
		status, r.ExitCode, r.Error, started, db.Now(), string(r.ResultJson), r.JobId)
	if err != nil {
		a.log.Error("job result", "err", err)
	}
	params := map[string]string{}
	_ = json.Unmarshal(j.Params, &params)
	switch j.Type {
	case "block_ip":
		st := "active"
		if status != "succeeded" {
			st = "failed"
		}
		_, _ = a.db.ExecContext(ctx, `UPDATE ip_blocks SET status=? WHERE job_id=?`, st, j.ID)
	case "unblock_ip":
		if status == "succeeded" {
			_, _ = a.db.ExecContext(ctx, `UPDATE ip_blocks SET status='removed' WHERE host_id=? AND ip=? AND status IN ('active','pending')`, hostID, params["ip"])
		}
	case "disk_usage":
		a.storeDiskResult(ctx, &j, params, status, r.Error, r.ResultJson)
	}
	a.audit(ctx, auditEntry{TenantID: j.TenantID, Actor: "agent:" + itoa(hostID), Action: "job.finished", TargetType: "job", TargetID: j.ID,
		Details: map[string]any{"type": j.Type, "status": status, "exit_code": r.ExitCode, "error": r.Error, "dry_run": j.DryRun}})
	a.events.publish(Event{Type: "job.updated", TenantID: j.TenantID, HostID: hostID, ID: j.ID})
}

// autoUpdateAll checks every connected agent host for an outdated agent.
func (m *JobManager) autoUpdateAll(ctx context.Context) {
	a := m.app
	if !a.settings.Agent().AutoUpdate {
		return
	}
	var ids []int64
	if err := a.db.SelectContext(ctx, &ids, `SELECT id FROM hosts WHERE kind='agent' AND revoked=0 AND connected=1`); err != nil {
		return
	}
	for _, id := range ids {
		if h, err := a.getHost(ctx, id); err == nil {
			m.maybeAutoUpdate(ctx, h)
		}
	}
}

// maybeAutoUpdate queues a self_update job when auto-update is enabled and
// the host's agent differs from the bundled build for its platform.
func (m *JobManager) maybeAutoUpdate(ctx context.Context, h *Host) {
	a := m.app
	if !a.settings.Agent().AutoUpdate || h.Kind != "agent" || h.Revoked {
		return
	}
	switch h.AgentVersion {
	case "", "unknown", "dev":
		return
	}
	hi := hostInfo(h)
	if hi.Os == "" || hi.Arch == "" {
		return
	}
	bin, err := a.agentBinary(hi.Os, hi.Arch)
	if err != nil || bin.Version == "" || bin.Version == "unknown" || bin.Version == "dev" || bin.Version == h.AgentVersion {
		return
	}
	var n int
	if err := a.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM jobs WHERE host_id=? AND type='self_update' AND status IN ('queued','scheduled','sent','running') AND created_at > ?`,
		h.ID, db.Now()-3600); err != nil || n > 0 {
		return
	}
	j, err := m.Create(ctx, systemPrincipal("system:auto-update", h.TenantID), JobRequest{HostID: h.ID, Type: "self_update", Confirm: true, Timeout: 600})
	if err != nil {
		a.log.Warn("auto-update job", "host_id", h.ID, "err", err)
		return
	}
	a.log.Info("auto-update queued", "host_id", h.ID, "name", h.Name, "from", h.AgentVersion, "to", bin.Version, "job", j.ID)
}

// prepareSelfUpdate fills download path, hash, version and signature.
func (m *JobManager) prepareSelfUpdate(h *Host, params map[string]string) error {
	hi := hostInfo(h)
	goos, goarch := hi.Os, hi.Arch
	if goos == "" || goarch == "" {
		return errors.New("host OS/arch unknown")
	}
	bin, err := m.app.agentBinary(goos, goarch)
	if err != nil {
		return err
	}
	if bin.Version == h.AgentVersion && params["force"] != "true" {
		return fmt.Errorf("agent already at version %s", bin.Version)
	}
	sig := m.app.ca.SignJob(sigs.BinaryMessage(goos, goarch, bin.Version, bin.SHA256))
	params["path"] = "/download/agent/" + goos + "/" + goarch
	params["sha256"] = bin.SHA256
	params["version"] = bin.Version
	params["signature"] = base64.StdEncoding.EncodeToString(sig)
	params["size"] = strconv.FormatInt(bin.Size, 10)
	return nil
}

// completeSelfUpdate marks a self-update job succeeded once the agent
// reconnects running the new version.
func (m *JobManager) completeSelfUpdate(ctx context.Context, hostID int64, ver string) {
	_, _ = m.app.db.ExecContext(ctx, `UPDATE jobs SET status='succeeded', finished_at=?, error='' WHERE host_id=? AND type='self_update' AND status IN ('sent','running','succeeded')
		AND json_extract(params,'$.version') IS NOT NULL AND finished_at=0`, db.Now(), hostID)
}

// blockIP records a block and dispatches the job.
func (a *App) blockIP(ctx context.Context, h *Host, ip string, minutes int, reason, actor string) (*Job, error) {
	if minutes <= 0 {
		minutes = 60
	}
	if minutes > 60*24*90 {
		minutes = 60 * 24 * 90
	}
	p := systemPrincipal(actor, h.TenantID)
	j, err := a.jobs.Create(ctx, p, JobRequest{HostID: h.ID, Type: "block_ip", Confirm: true,
		Params: map[string]string{"ip": ip, "minutes": strconv.Itoa(minutes), "reason": reason}, Timeout: 120})
	if err != nil {
		return nil, err
	}
	_, err = a.db.ExecContext(ctx, `INSERT INTO ip_blocks (tenant_id, host_id, ip, reason, expires_at, job_id, status, created_by, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		h.TenantID, h.ID, ip, reason, time.Now().Add(time.Duration(minutes)*time.Minute).Unix(), j.ID, "pending", actor, db.Now())
	return j, err
}

func (a *App) getJob(ctx context.Context, id string) (*Job, error) {
	var j Job
	err := a.db.GetContext(ctx, &j, `SELECT `+jobCols+`, j.output, j.result_json, COALESCE(h.name,'') AS host_name FROM jobs j LEFT JOIN hosts h ON h.id=j.host_id WHERE j.id=?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return &j, err
}
