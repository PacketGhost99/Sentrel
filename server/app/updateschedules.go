package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"sentrel/server/db"
)

// Update schedules queue upgrade jobs on a recurring basis. They never reboot.

const maxScheduleSkipped = 50

// maintenanceCooldown is the minimum gap between upgrade jobs that one
// maintenance-window schedule queues for the same host.
const maintenanceCooldown = 12 * time.Hour

type updateScheduleReq struct {
	TenantID     int64  `json:"tenant_id"`
	Name         string `json:"name"`
	Scope        string `json:"scope"`
	ScopeID      int64  `json:"scope_id"`
	Frequency    string `json:"frequency"`
	Weekday      int    `json:"weekday"`
	MonthDay     int    `json:"month_day"`
	TimeOfDay    string `json:"time_of_day"`
	SecurityOnly *bool  `json:"security_only"`
	Enabled      *bool  `json:"enabled"`
}

type updateSkip struct {
	HostID int64  `json:"host_id"`
	Host   string `json:"host"`
	Reason string `json:"reason"`
}

type updateRunResult struct {
	Queued  int          `json:"queued"`
	Skipped []updateSkip `json:"skipped"`
}

// normalizeUpdateSchedule validates the non-scope fields and fills defaults.
func normalizeUpdateSchedule(req *updateScheduleReq) error {
	req.Name = strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(req.Name) > 80 {
		return badRequest("name must be at most 80 characters")
	}
	switch req.Frequency {
	case "daily", "weekly", "monthly", "maintenance":
	default:
		return badRequest("frequency must be daily, weekly, monthly or maintenance")
	}
	if req.Weekday < 0 || req.Weekday > 6 {
		return badRequest("weekday must be 0-6")
	}
	if req.Frequency == "monthly" {
		if req.MonthDay < 1 || req.MonthDay > 28 {
			return badRequest("month_day must be 1-28")
		}
	} else if req.MonthDay < 1 || req.MonthDay > 28 {
		req.MonthDay = 1
	}
	if req.Frequency == "maintenance" {
		if req.TimeOfDay == "" {
			req.TimeOfDay = "03:00"
		}
	} else if _, _, ok := parseHHMM(req.TimeOfDay); !ok {
		return badRequest("time must be HH:MM")
	}
	return nil
}

func (a *App) validateUpdateSchedule(c *Ctx, req *updateScheduleReq) (int64, error) {
	tenant, err := a.validateScheduleScope(c, req.TenantID, &req.Scope, &req.ScopeID)
	if err != nil {
		return 0, err
	}
	return tenant, normalizeUpdateSchedule(req)
}

func (a *App) apiListUpdateSchedules(c *Ctx) (any, error) {
	tw, args := c.tenantWhere("tenant_id")
	out := []UpdateSchedule{}
	err := a.db.SelectContext(c.ctx(), &out, `SELECT * FROM update_schedules WHERE `+tw+` ORDER BY id`, args...)
	return out, err
}

func (a *App) apiCreateUpdateSchedule(c *Ctx) (any, error) {
	var req updateScheduleReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	tenant, err := a.validateUpdateSchedule(c, &req)
	if err != nil {
		return nil, err
	}
	res, err := a.db.ExecContext(c.ctx(), `INSERT INTO update_schedules (tenant_id, name, scope, scope_id, frequency, weekday, month_day, time_of_day, security_only, enabled, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		tenant, req.Name, req.Scope, req.ScopeID, req.Frequency, req.Weekday, req.MonthDay, req.TimeOfDay, boolOr(req.SecurityOnly, true), boolOr(req.Enabled, true), db.Now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.auditT(tenant, "update.schedule_created", "update_schedule", itoa(id), req)
	var s UpdateSchedule
	err = a.db.GetContext(c.ctx(), &s, `SELECT * FROM update_schedules WHERE id=?`, id)
	return created{s}, err
}

func (a *App) apiUpdateUpdateSchedule(c *Ctx) (any, error) {
	var s UpdateSchedule
	if err := a.db.GetContext(c.ctx(), &s, `SELECT * FROM update_schedules WHERE id=?`, c.ID()); err != nil || !c.P.CanTenant(s.TenantID) {
		return nil, errNotFound
	}
	var req updateScheduleReq
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	req.TenantID = s.TenantID
	if _, err := a.validateUpdateSchedule(c, &req); err != nil {
		return nil, err
	}
	_, err := a.db.ExecContext(c.ctx(), `UPDATE update_schedules SET name=?, scope=?, scope_id=?, frequency=?, weekday=?, month_day=?, time_of_day=?, security_only=?, enabled=? WHERE id=?`,
		req.Name, req.Scope, req.ScopeID, req.Frequency, req.Weekday, req.MonthDay, req.TimeOfDay, boolOr(req.SecurityOnly, true), boolOr(req.Enabled, true), s.ID)
	c.auditT(s.TenantID, "update.schedule_updated", "update_schedule", itoa(s.ID), req)
	return nil, err
}

func (a *App) apiDeleteUpdateSchedule(c *Ctx) (any, error) {
	var s UpdateSchedule
	if err := a.db.GetContext(c.ctx(), &s, `SELECT * FROM update_schedules WHERE id=?`, c.ID()); err != nil || !c.P.CanTenant(s.TenantID) {
		return nil, errNotFound
	}
	_, err := a.db.ExecContext(c.ctx(), `DELETE FROM update_schedules WHERE id=?`, s.ID)
	c.auditT(s.TenantID, "update.schedule_deleted", "update_schedule", itoa(s.ID), nil)
	return nil, err
}

// scheduleDue reports whether a clock-based schedule should fire now. It fires
// once per matching day, after the time of day, if it has not run since then.
func scheduleDue(now time.Time, loc *time.Location, freq string, weekday, monthDay int, hhmm string, lastRun int64) bool {
	hh, mm, ok := parseHHMM(hhmm)
	if !ok {
		return false
	}
	now = now.In(loc)
	at := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, loc)
	if now.Before(at) || lastRun >= at.Unix() {
		return false
	}
	switch freq {
	case "daily":
		return true
	case "weekly":
		return int(now.Weekday()) == weekday
	case "monthly":
		return now.Day() == monthDay
	}
	return false
}

func updateSchedulePrincipalName(id int64) string {
	return "system:update-schedule:" + itoa(id)
}

// runUpdateSchedules queues upgrade jobs for due schedules. It is called about
// once a minute.
func (a *App) runUpdateSchedules(ctx context.Context) {
	var ss []UpdateSchedule
	if err := a.db.SelectContext(ctx, &ss, `SELECT * FROM update_schedules WHERE enabled=1`); err != nil || len(ss) == 0 {
		return
	}
	loc, err := time.LoadLocation(a.settings.General().Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now()
	for _, s := range ss {
		if ctx.Err() != nil {
			return
		}
		maint := s.Frequency == "maintenance"
		if !maint && !scheduleDue(now, loc, s.Frequency, s.Weekday, s.MonthDay, s.TimeOfDay, s.LastRunAt) {
			continue
		}
		res := a.runUpdateSchedule(ctx, s)
		// A maintenance schedule ticks every minute; only record ticks that
		// queued something so the last result stays meaningful.
		if maint && res.Queued == 0 {
			continue
		}
		rb, _ := json.Marshal(res)
		_, _ = a.db.ExecContext(ctx, `UPDATE update_schedules SET last_run_at=?, last_result=? WHERE id=?`, db.Now(), string(rb), s.ID)
	}
}

// runUpdateSchedule creates the upgrade jobs for one schedule.
func (a *App) runUpdateSchedule(ctx context.Context, s UpdateSchedule) updateRunResult {
	res := updateRunResult{Skipped: []updateSkip{}}
	type hostRow struct {
		ID       int64  `db:"id"`
		Name     string `db:"name"`
		Upgrades int    `db:"upgradable_count"`
		Security int    `db:"security_count"`
	}
	q := `SELECT h.id, h.name, COALESCE(i.upgradable_count,0) AS upgradable_count, COALESCE(i.security_count,0) AS security_count
		FROM hosts h LEFT JOIN inventory_status i ON i.host_id=h.id WHERE h.kind='agent' AND h.revoked=0`
	var args []any
	switch s.Scope {
	case "tenant":
		q += ` AND h.tenant_id=?`
		args = append(args, s.ScopeID)
	case "group":
		q += ` AND h.group_id=?`
		args = append(args, s.ScopeID)
	case "host":
		q += ` AND h.id=?`
		args = append(args, s.ScopeID)
	}
	if s.TenantID != 0 {
		q += ` AND h.tenant_id=?`
		args = append(args, s.TenantID)
	}
	q += ` ORDER BY h.id`
	var hosts []hostRow
	if err := a.db.SelectContext(ctx, &hosts, q, args...); err != nil {
		return res
	}
	p := systemPrincipal(updateSchedulePrincipalName(s.ID), s.TenantID)
	maint := s.Frequency == "maintenance"
	skip := func(h hostRow, reason string) {
		if len(res.Skipped) < maxScheduleSkipped {
			res.Skipped = append(res.Skipped, updateSkip{HostID: h.ID, Host: h.Name, Reason: reason})
		}
	}
	for _, h := range hosts {
		if maint {
			// One job per host and schedule at a time, and at most one per
			// maintenanceCooldown: otherwise a failing upgrade, or packages
			// still pending afterwards, would be retried every minute while
			// the window is open.
			var n int
			_ = a.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM jobs WHERE host_id=? AND type='upgrade' AND requested_by=?
				AND (status IN ('scheduled','queued','sent','running') OR created_at > ?)`,
				h.ID, p.Name, time.Now().Add(-maintenanceCooldown).Unix())
			if n > 0 {
				continue
			}
		}
		if (s.SecurityOnly && h.Security == 0) || h.Upgrades == 0 {
			skip(h, "nothing to install")
			continue
		}
		req := JobRequest{HostID: h.ID, Type: "upgrade", Params: map[string]string{}, Confirm: true}
		if s.SecurityOnly {
			req.Params["security_only"] = "true"
		}
		if maint {
			req.Schedule = "maintenance"
		}
		if _, err := a.jobs.Create(ctx, p, req); err != nil {
			skip(h, err.Error())
			continue
		}
		res.Queued++
	}
	return res
}
