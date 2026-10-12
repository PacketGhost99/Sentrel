package app

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaintenanceCache evaluates maintenance windows. Windows are reloaded every
// 30 seconds and on change.
type MaintenanceCache struct {
	app *App
	mu  sync.RWMutex
	ws  []MaintenanceWindow
}

func newMaintenanceCache(a *App) *MaintenanceCache { return &MaintenanceCache{app: a} }

func (m *MaintenanceCache) refresh(ctx context.Context) {
	var ws []MaintenanceWindow
	if err := m.app.db.SelectContext(ctx, &ws, `SELECT * FROM maintenance_windows WHERE enabled=1`); err != nil {
		m.app.log.Error("load maintenance windows", "err", err)
		return
	}
	m.mu.Lock()
	m.ws = ws
	m.mu.Unlock()
}

func (m *MaintenanceCache) loop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh(ctx)
		}
	}
}

func parseHHMM(s string) (int, int, bool) {
	p := strings.SplitN(s, ":", 2)
	if len(p) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(p[0])
	mi, err2 := strconv.Atoi(p[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || mi < 0 || mi > 59 {
		return 0, 0, false
	}
	return h, mi, true
}

// occurrence returns the [start,end) of the occurrence that contains t, or
// ok=false.
func (w *MaintenanceWindow) occurrence(t time.Time) (time.Time, time.Time, bool) {
	if w.Recurrence == "" || w.Recurrence == "none" {
		s, e := time.Unix(w.StartsAt, 0), time.Unix(w.EndsAt, 0)
		return s, e, !t.Before(s) && t.Before(e)
	}
	if t.Unix() < w.StartsAt || (w.EndsAt > 0 && t.Unix() >= w.EndsAt) {
		return time.Time{}, time.Time{}, false
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		loc = time.UTC
	}
	hh, mm, ok := parseHHMM(w.StartTime)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	dur := time.Duration(w.DurationMinutes) * time.Minute
	lt := t.In(loc)
	// Check occurrences starting on the previous days that may still be running.
	days := int(dur/(24*time.Hour)) + 1
	for back := 0; back <= days; back++ {
		d := lt.AddDate(0, 0, -back)
		if !w.dayMatches(d, loc) {
			continue
		}
		s := time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, loc)
		e := s.Add(dur)
		if !t.Before(s) && t.Before(e) {
			return s, e, true
		}
	}
	return time.Time{}, time.Time{}, false
}

func (w *MaintenanceWindow) dayMatches(d time.Time, loc *time.Location) bool {
	switch w.Recurrence {
	case "daily":
		return true
	case "weekly":
		for _, wd := range w.Weekdays {
			if int(d.Weekday()) == int(wd) {
				return true
			}
		}
		return false
	case "monthly":
		return d.Day() == time.Unix(w.StartsAt, 0).In(loc).Day()
	}
	return false
}

// next returns the next occurrence starting after t (within ~400 days).
func (w *MaintenanceWindow) next(t time.Time) (time.Time, time.Time, bool) {
	if s, e, ok := w.occurrence(t); ok {
		return s, e, true
	}
	if w.Recurrence == "" || w.Recurrence == "none" {
		s, e := time.Unix(w.StartsAt, 0), time.Unix(w.EndsAt, 0)
		return s, e, s.After(t)
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		loc = time.UTC
	}
	hh, mm, ok := parseHHMM(w.StartTime)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	lt := t.In(loc)
	for i := 0; i < 400; i++ {
		d := lt.AddDate(0, 0, i)
		if !w.dayMatches(d, loc) {
			continue
		}
		s := time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, loc)
		if s.Unix() < w.StartsAt || !s.After(t) {
			continue
		}
		if w.EndsAt > 0 && s.Unix() >= w.EndsAt {
			return time.Time{}, time.Time{}, false
		}
		return s, s.Add(time.Duration(w.DurationMinutes) * time.Minute), true
	}
	return time.Time{}, time.Time{}, false
}

// Decorate fills ActiveNow/NextStart/NextEnd for API responses.
func (w *MaintenanceWindow) Decorate(now time.Time) {
	_, _, w.ActiveNow = w.occurrence(now)
	if s, e, ok := w.next(now); ok {
		w.NextStart, w.NextEnd = s.Unix(), e.Unix()
	}
}

func (w *MaintenanceWindow) appliesTo(h hostRef) bool {
	if w.TenantID != 0 && w.TenantID != h.TenantID {
		return false
	}
	switch w.Scope {
	case "all":
		return true
	case "tenant":
		if len(w.ScopeIDs) == 0 {
			return w.TenantID == h.TenantID
		}
		return containsInt(w.ScopeIDs, h.TenantID)
	case "group":
		return h.GroupID != 0 && containsInt(w.ScopeIDs, h.GroupID)
	case "host":
		return containsInt(w.ScopeIDs, h.ID)
	}
	return false
}

func (m *MaintenanceCache) activeFor(hostID int64, pred func(*MaintenanceWindow) bool) bool {
	h, ok := m.app.alerts.host(hostID)
	if !ok {
		return false
	}
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.ws {
		w := &m.ws[i]
		if !pred(w) || !w.appliesTo(h) {
			continue
		}
		if _, _, ok := w.occurrence(now); ok {
			return true
		}
	}
	return false
}

// hostSuppressed reports whether alerts for a host are silenced now.
func (m *MaintenanceCache) hostSuppressed(hostID int64) bool {
	return m.activeFor(hostID, func(w *MaintenanceWindow) bool { return w.SuppressAlerts })
}

// hostInWindow reports whether a host is in any active maintenance window.
func (m *MaintenanceCache) hostInWindow(hostID int64) bool {
	return m.activeFor(hostID, func(w *MaintenanceWindow) bool { return true })
}

// hostAllowsJobs reports whether scheduled jobs may run on a host now.
func (m *MaintenanceCache) hostAllowsJobs(hostID int64) bool {
	return m.activeFor(hostID, func(w *MaintenanceWindow) bool { return w.AllowJobs })
}

// tenantSuppressed is used for alerts not tied to a host.
func (m *MaintenanceCache) tenantSuppressed(tenantID int64) bool {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.ws {
		w := &m.ws[i]
		if !w.SuppressAlerts {
			continue
		}
		if w.Scope == "all" && (w.TenantID == 0 || w.TenantID == tenantID) || w.Scope == "tenant" && (w.TenantID == tenantID || containsInt(w.ScopeIDs, tenantID)) {
			if _, _, ok := w.occurrence(now); ok {
				return true
			}
		}
	}
	return false
}
