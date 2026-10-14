package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "sentrel/gen/sentrelv1"
	"sentrel/server/db"
	"sentrel/server/metrics"
	"sentrel/server/probe"
)

// Server-side check types run by the poller; agent check types are
// evaluated from the host state pushed by the agent.
var serverCheckTypes = map[string]bool{"ping": true, "tcp": true, "http": true, "cert": true, "dns": true, "push": true}
var agentCheckTypes = map[string]bool{"service": true, "process": true, "port": true, "container": true}

// CheckConfig holds type-specific options (stored as JSON).
type CheckConfig struct {
	Count           int               `json:"count,omitempty"`  // ping
	Method          string            `json:"method,omitempty"` // http
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	ExpectStatus    string            `json:"expect_status,omitempty"`
	Keyword         string            `json:"keyword,omitempty"`
	KeywordAbsent   bool              `json:"keyword_absent,omitempty"`
	KeywordRegex    bool              `json:"keyword_regex,omitempty"`
	FollowRedirects *bool             `json:"follow_redirects,omitempty"`
	IgnoreTLS       bool              `json:"ignore_tls,omitempty"`
	BasicUser       string            `json:"basic_user,omitempty"`
	BasicPassword   string            `json:"basic_password,omitempty"`
	MaxLatencyMs    float64           `json:"max_latency_ms,omitempty"`
	CheckCert       bool              `json:"check_cert,omitempty"`  // http: also alert on cert expiry
	ServerName      string            `json:"server_name,omitempty"` // cert SNI
	AllowInvalid    bool              `json:"allow_invalid,omitempty"`
	WarnDays        float64           `json:"warn_days,omitempty"`   // cert, default 30
	NoticeDays      float64           `json:"notice_days,omitempty"` // cert, default 14 (re-notify)
	CritDays        float64           `json:"crit_days,omitempty"`   // cert, default 7
	RecordType      string            `json:"record_type,omitempty"` // dns
	Resolver        string            `json:"resolver,omitempty"`
	Expect          string            `json:"expect,omitempty"`
	State           string            `json:"state,omitempty"`           // service: expected state (running/active)
	Regex           bool              `json:"regex,omitempty"`           // process
	Protocol        string            `json:"protocol,omitempty"`        // port: tcp/udp
	RequireHealthy  bool              `json:"require_healthy,omitempty"` // container
	GraceSeconds    int               `json:"grace_seconds,omitempty"`   // push: extra wait after the interval (default 300)
}

// CheckRunner schedules server-side checks.
type CheckRunner struct {
	app     *App
	mu      sync.Mutex
	next    map[int64]time.Time
	running map[int64]bool
	checks  []Check
	loaded  time.Time
	sem     chan struct{}

	// Push checks are judged by when their last beat arrived, not polled.
	pushIDs    []int64
	pushLoaded time.Time
	pushNext   map[int64]time.Time
	pushLimit  *rateLimiter // beats per token
	pushMiss   *rateLimiter // unknown-token requests per client
}

func newCheckRunner(a *App) *CheckRunner {
	return &CheckRunner{app: a, next: map[int64]time.Time{}, running: map[int64]bool{}, sem: make(chan struct{}, 32),
		pushNext: map[int64]time.Time{}, pushLimit: newRateLimiter(pushBeatsPerMinute, time.Minute), pushMiss: newRateLimiter(pushMissesPerMinute, time.Minute)}
}

// Invalidate reloads checks on the next tick and runs a changed check soon.
func (r *CheckRunner) Invalidate(id int64) {
	r.mu.Lock()
	r.loaded = time.Time{}
	delete(r.next, id)
	r.mu.Unlock()
}

func (r *CheckRunner) loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *CheckRunner) tick(ctx context.Context) {
	r.mu.Lock()
	if time.Since(r.loaded) > 30*time.Second {
		var cs []Check
		if err := r.app.db.SelectContext(ctx, &cs, `SELECT * FROM checks WHERE enabled=1 AND type IN ('ping','tcp','http','cert','dns')`); err == nil {
			r.checks = cs
			r.loaded = time.Now()
		}
	}
	now := time.Now()
	var due []Check
	for _, c := range r.checks {
		if r.running[c.ID] {
			continue
		}
		n, ok := r.next[c.ID]
		if !ok {
			// Spread first runs over a few seconds.
			n = now.Add(time.Duration(c.ID%5) * time.Second)
			r.next[c.ID] = n
		}
		if !now.Before(n) {
			iv := c.IntervalSeconds
			if iv < 10 {
				iv = 10
			}
			r.next[c.ID] = now.Add(time.Duration(iv) * time.Second)
			r.running[c.ID] = true
			due = append(due, c)
		}
	}
	r.mu.Unlock()
	r.tickPush(ctx)
	for _, c := range due {
		c := c
		go func() {
			r.sem <- struct{}{}
			defer func() { <-r.sem }()
			res := r.execute(ctx, &c)
			r.record(ctx, &c, res)
			r.mu.Lock()
			delete(r.running, c.ID)
			r.mu.Unlock()
		}()
	}
}

// RunNow executes a check immediately and returns the result. It shares the
// scheduler's limits: a check that is already running (scheduled or manual) is
// refused with a conflict, and the run waits for a slot in the same semaphore.
func (r *CheckRunner) RunNow(ctx context.Context, c *Check) (probe.Result, error) {
	r.mu.Lock()
	if r.running[c.ID] {
		r.mu.Unlock()
		return probe.Result{}, conflict("this check is already running")
	}
	r.running[c.ID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, c.ID)
		r.mu.Unlock()
	}()
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return probe.Result{}, ctx.Err()
	}
	var res probe.Result
	switch {
	case c.Type == "push":
		return r.runPushNow(ctx, c), nil
	case agentCheckTypes[c.Type]:
		res = r.evaluateAgentCheck(ctx, c)
	default:
		res = r.execute(ctx, c)
	}
	r.record(ctx, c, res)
	return res, nil
}

// hostMoved drops cached copies of a host's checks after it changes tenant,
// so no scheduled run uses the previous owner.
func (r *CheckRunner) hostMoved(hostID, tenant int64) {
	r.mu.Lock()
	for i := range r.checks {
		if r.checks[i].HostID != nil && *r.checks[i].HostID == hostID {
			r.checks[i].TenantID = tenant
		}
	}
	r.loaded = time.Time{}
	r.mu.Unlock()
}

// refreshOwner re-reads who owns a check just before its result is stored, so
// a run that started before a host transfer cannot publish to the old tenant.
// It reports false when the check no longer exists.
func (r *CheckRunner) refreshOwner(ctx context.Context, c *Check) bool {
	var cur struct {
		TenantID int64  `db:"tenant_id"`
		HostID   *int64 `db:"host_id"`
	}
	if err := r.app.db.GetContext(ctx, &cur, `SELECT tenant_id, host_id FROM checks WHERE id=?`, c.ID); err != nil {
		return !errors.Is(err, sql.ErrNoRows)
	}
	c.TenantID, c.HostID = cur.TenantID, cur.HostID
	return true
}

func checkConfig(c *Check) CheckConfig {
	var cfg CheckConfig
	if len(c.Config) > 0 {
		_ = json.Unmarshal(c.Config, &cfg)
	}
	return cfg
}

func (r *CheckRunner) execute(ctx context.Context, c *Check) probe.Result {
	cfg := checkConfig(c)
	to := time.Duration(c.TimeoutSeconds) * time.Second
	if to <= 0 {
		to = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, to+5*time.Second)
	defer cancel()
	g := r.app.guardFor(c.TenantID)
	switch c.Type {
	case "ping":
		return probe.Ping(cctx, g, c.Target, cfg.Count, to)
	case "tcp":
		return probe.TCP(cctx, g, c.Target, to)
	case "http":
		return probe.HTTP(cctx, g, probe.HTTPConfig{URL: c.Target, Method: cfg.Method, Headers: cfg.Headers, Body: cfg.Body, ExpectStatus: cfg.ExpectStatus,
			Keyword: cfg.Keyword, KeywordAbsent: cfg.KeywordAbsent, KeywordRegex: cfg.KeywordRegex, FollowRedirects: cfg.FollowRedirects,
			IgnoreTLS: cfg.IgnoreTLS, BasicUser: cfg.BasicUser, BasicPassword: cfg.BasicPassword, MaxLatencyMs: cfg.MaxLatencyMs}, to)
	case "cert":
		return probe.Cert(cctx, g, c.Target, cfg.ServerName, to, cfg.AllowInvalid)
	case "dns":
		return probe.DNS(cctx, g, c.Target, probe.DNSConfig{RecordType: cfg.RecordType, Server: cfg.Resolver, Expect: cfg.Expect}, to)
	}
	return probe.Result{Message: "unknown check type " + c.Type}
}

// certSeverity maps days-to-expiry onto severity and escalation stage.
func certSeverity(days float64, cfg CheckConfig) (string, int) {
	warn, notice, crit := cfg.WarnDays, cfg.NoticeDays, cfg.CritDays
	if warn == 0 {
		warn = 30
	}
	if notice == 0 {
		notice = 14
	}
	if crit == 0 {
		crit = 7
	}
	switch {
	case days <= crit:
		return "critical", 3
	case days <= notice:
		return "warning", 2
	case days <= warn:
		return "warning", 1
	}
	return "ok", 0
}

func (r *CheckRunner) record(ctx context.Context, c *Check, res probe.Result) {
	iv := c.IntervalSeconds
	if iv <= 0 {
		iv = 60
	}
	r.recordWith(ctx, c, res, iv)
}

// recordWith stores a result; uptime is the number of seconds to credit to
// the up or down total (0 for none).
func (r *CheckRunner) recordWith(ctx context.Context, c *Check, res probe.Result, uptime int) {
	a := r.app
	if !r.refreshOwner(ctx, c) {
		return
	}
	now := db.Now()
	cfg := checkConfig(c)
	status := "up"
	if !res.Up {
		status = "down"
	}
	certSev, certStage := "ok", 0
	if d, ok := res.Values["cert_days"]; ok && (c.Type == "cert" || cfg.CheckCert) {
		certSev, certStage = certSeverity(d, cfg)
		if res.Up && certSev != "ok" {
			status = "warning"
		}
	}
	cf := 0
	if !res.Up {
		cf = c.ConsecutiveFailures + 1
	}
	prev := c.Status
	changeAt := c.LastChangeAt
	if prev != status {
		changeAt = now
	}
	_, err := a.db.ExecContext(ctx, `UPDATE checks SET status=?, consecutive_failures=?, last_message=?, last_latency_ms=?, last_checked_at=?, last_change_at=? WHERE id=?`,
		status, cf, truncateStr(res.Message, 1000), res.LatencyMs, now, changeAt, c.ID)
	if err != nil {
		a.log.Error("check update", "err", err)
	}
	c.Status, c.ConsecutiveFailures, c.LastMessage, c.LastLatencyMs, c.LastCheckedAt, c.LastChangeAt = status, cf, res.Message, res.LatencyMs, now, changeAt
	if c.HostID != nil {
		// Agentless targets take their status from their checks.
		_, _ = a.db.ExecContext(ctx, `UPDATE hosts SET status = CASE WHEN EXISTS (SELECT 1 FROM checks k WHERE k.host_id=hosts.id AND k.enabled=1
			AND k.status='down' AND k.consecutive_failures>=k.failure_threshold) THEN 'down' ELSE 'up' END, last_seen_at=? WHERE id=? AND kind='target'`, now, *c.HostID)
	}

	// Time series.
	var hostID int64
	if c.HostID != nil {
		hostID = *c.HostID
	}
	up := 0.0
	if res.Up {
		up = 1
	}
	lbl := map[string]string{"check_id": itoa(c.ID), "check_type": c.Type}
	samples := []metrics.Sample{{Name: "check_up", Labels: lbl, Value: up}}
	if res.Up && res.LatencyMs > 0 {
		samples = append(samples, metrics.Sample{Name: "check_latency_ms", Labels: lbl, Value: res.LatencyMs})
	}
	for k, v := range res.Values {
		samples = append(samples, metrics.Sample{Name: "check_" + k, Labels: lbl, Value: v})
	}
	_ = a.ts.Append(hostID, c.TenantID, samples)

	// Alerts.
	cond := condition{Key: "check:" + itoa(c.ID), TenantID: c.TenantID, HostID: c.HostID, CheckID: &c.ID, Source: "check", Metric: c.Type, Severity: "ok"}
	if !res.Up {
		cond.Severity = c.Severity
		if cond.Severity == "" {
			cond.Severity = "critical"
		}
		cond.Title = c.Name + " is down"
		cond.Message = res.Message
		cond.ForCount = c.FailureThreshold
	}
	a.alerts.apply(ctx, cond)
	if c.Type == "cert" || cfg.CheckCert {
		cc := condition{Key: "check:" + itoa(c.ID) + ":cert", TenantID: c.TenantID, HostID: c.HostID, CheckID: &c.ID, Source: "cert", Metric: "cert_days", Severity: certSev, Stage: certStage, ForCount: 1}
		if d, ok := res.Values["cert_days"]; ok && certSev != "ok" {
			cc.Title = "Certificate for " + c.Name + " expires soon"
			cc.Message = fmt.Sprintf("Certificate expires in %.0f days", d)
			v := d
			cc.Value = &v
		}
		if _, ok := res.Values["cert_days"]; ok || res.Up {
			a.alerts.apply(ctx, cc)
		}
	}
	if prev != status {
		a.events.publish(Event{Type: "check.changed", TenantID: c.TenantID, HostID: hostID, ID: itoa(c.ID)})
	}
	// Uptime accounting.
	if uptime > 0 {
		a.addUptime(ctx, c.TenantID, "check", c.ID, res.Up, uptime)
	}
}

func (a *App) addUptime(ctx context.Context, tenant int64, typ string, id int64, up bool, seconds int) {
	day := time.Now().UTC().Format("2006-01-02")
	col := "up_seconds"
	if !up {
		col = "down_seconds"
	}
	_, _ = a.db.ExecContext(ctx, `INSERT INTO uptime_daily (tenant_id, item_type, item_id, day, `+col+`) VALUES (?,?,?,?,?)
		ON CONFLICT(item_type, item_id, day) DO UPDATE SET `+col+`=`+col+`+excluded.`+col, tenant, typ, id, day, seconds)
}

// --- agent-evaluated checks ---

func (r *CheckRunner) evaluateAgentChecks(ctx context.Context, hostID int64, st *pb.HostState) {
	var cs []Check
	if err := r.app.db.SelectContext(ctx, &cs, `SELECT * FROM checks WHERE host_id=? AND enabled=1 AND type IN ('service','process','port','container')`, hostID); err != nil {
		return
	}
	for i := range cs {
		c := &cs[i]
		res := evaluateAgentCheck(c, st)
		r.record(ctx, c, res)
	}
}

func (r *CheckRunner) evaluateAgentCheck(ctx context.Context, c *Check) probe.Result {
	if c.HostID == nil {
		return probe.Result{Message: "agent checks need a host"}
	}
	h, err := r.app.getHost(ctx, *c.HostID)
	if err != nil {
		return probe.Result{Message: "host not found"}
	}
	return evaluateAgentCheck(c, hostState(h))
}

func normaliseService(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".service")
}

func evaluateAgentCheck(c *Check, st *pb.HostState) probe.Result {
	cfg := checkConfig(c)
	switch c.Type {
	case "service":
		want := normaliseService(c.Target)
		for _, s := range st.Services {
			if normaliseService(s.Name) == want || strings.EqualFold(s.DisplayName, c.Target) {
				expected := strings.ToLower(cfg.State)
				state := strings.ToLower(s.State)
				ok := false
				switch expected {
				case "", "running", "active":
					ok = state == "active" || state == "running"
				default:
					ok = state == expected || strings.ToLower(s.SubState) == expected
				}
				msg := fmt.Sprintf("%s is %s", s.Name, s.State)
				if s.SubState != "" {
					msg += " (" + s.SubState + ")"
				}
				return probe.Result{Up: ok, Message: msg}
			}
		}
		return probe.Result{Message: fmt.Sprintf("service %s not found", c.Target)}
	case "process":
		var re *regexp.Regexp
		if cfg.Regex {
			var err error
			if re, err = regexp.Compile(c.Target); err != nil {
				return probe.Result{Message: "invalid regex"}
			}
		}
		for _, n := range st.ProcessNames {
			if (re != nil && re.MatchString(n)) || (re == nil && strings.EqualFold(n, c.Target)) {
				return probe.Result{Up: true, Message: "process " + n + " is running"}
			}
		}
		return probe.Result{Message: fmt.Sprintf("no process matching %q", c.Target)}
	case "port":
		port, err := strconv.Atoi(strings.TrimSpace(c.Target))
		if err != nil {
			return probe.Result{Message: "invalid port"}
		}
		proto := strings.ToLower(cfg.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		for _, p := range st.Ports {
			if int(p.Port) == port && strings.HasPrefix(strings.ToLower(p.Protocol), proto) {
				msg := fmt.Sprintf("%s/%d listening on %s", proto, port, p.Address)
				if p.Process != "" {
					msg += " (" + p.Process + ")"
				}
				return probe.Result{Up: true, Message: msg}
			}
		}
		return probe.Result{Message: fmt.Sprintf("nothing listening on %s/%d", proto, port)}
	case "container":
		for _, ct := range st.Containers {
			if strings.EqualFold(strings.TrimPrefix(ct.Name, "/"), strings.TrimPrefix(c.Target, "/")) || strings.HasPrefix(ct.Id, c.Target) {
				ok := ct.State == "running"
				if ok && cfg.RequireHealthy && ct.Health != "" && ct.Health != "healthy" {
					ok = false
				}
				msg := fmt.Sprintf("container %s is %s", ct.Name, ct.Status)
				if ct.Health != "" {
					msg += ", " + ct.Health
				}
				return probe.Result{Up: ok, Message: msg}
			}
		}
		if !st.DockerAvailable {
			return probe.Result{Message: "Docker is not available to the agent"}
		}
		return probe.Result{Message: fmt.Sprintf("container %s not found", c.Target)}
	}
	return probe.Result{Message: "unknown agent check type"}
}
