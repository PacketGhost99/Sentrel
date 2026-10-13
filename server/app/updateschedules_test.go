package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestScheduleDue(t *testing.T) {
	utc := time.UTC
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	at := func(loc *time.Location, y int, m time.Month, d, h, mi int) time.Time {
		return time.Date(y, m, d, h, mi, 0, 0, loc)
	}
	// 2026-03-01 is a Sunday.
	tests := []struct {
		name    string
		now     time.Time
		loc     *time.Location
		freq    string
		weekday int
		monthD  int
		hhmm    string
		last    int64
		want    bool
	}{
		{"daily before time", at(utc, 2026, 3, 1, 2, 59), utc, "daily", 0, 1, "03:00", 0, false},
		{"daily at time", at(utc, 2026, 3, 1, 3, 0), utc, "daily", 0, 1, "03:00", 0, true},
		{"daily after time", at(utc, 2026, 3, 1, 15, 0), utc, "daily", 0, 1, "03:00", 0, true},
		{"daily already ran", at(utc, 2026, 3, 1, 15, 0), utc, "daily", 0, 1, "03:00", at(utc, 2026, 3, 1, 3, 1).Unix(), false},
		{"daily ran yesterday", at(utc, 2026, 3, 1, 3, 5), utc, "daily", 0, 1, "03:00", at(utc, 2026, 2, 28, 3, 1).Unix(), true},
		{"weekly matching day", at(utc, 2026, 3, 1, 4, 0), utc, "weekly", 0, 1, "03:00", 0, true},
		{"weekly other day", at(utc, 2026, 3, 2, 4, 0), utc, "weekly", 0, 1, "03:00", 0, false},
		{"weekly already ran", at(utc, 2026, 3, 1, 4, 0), utc, "weekly", 0, 1, "03:00", at(utc, 2026, 3, 1, 3, 0).Unix(), false},
		{"monthly matching day", at(utc, 2026, 3, 1, 3, 30), utc, "monthly", 0, 1, "03:00", 0, true},
		{"monthly other day", at(utc, 2026, 3, 2, 3, 30), utc, "monthly", 0, 1, "03:00", 0, false},
		{"monthly before time", at(utc, 2026, 3, 1, 2, 0), utc, "monthly", 0, 1, "03:00", 0, false},
		{"monthly already ran", at(utc, 2026, 3, 1, 9, 0), utc, "monthly", 0, 1, "03:00", at(utc, 2026, 3, 1, 3, 2).Unix(), false},
		// 07:30 UTC is 02:30 in New York (EST): before 03:00 local, due once 08:00 UTC passes.
		{"timezone before local time", at(utc, 2026, 1, 10, 7, 30), ny, "daily", 0, 1, "03:00", 0, false},
		{"timezone after local time", at(utc, 2026, 1, 10, 8, 30), ny, "daily", 0, 1, "03:00", 0, true},
		// 02:00 UTC on Sunday 1 March is still Saturday evening in New York.
		{"timezone local weekday", at(utc, 2026, 3, 1, 2, 0), ny, "weekly", 0, 1, "01:00", 0, false},
		{"maintenance has no clock", at(utc, 2026, 3, 1, 4, 0), utc, "maintenance", 0, 1, "03:00", 0, false},
		{"bad time", at(utc, 2026, 3, 1, 4, 0), utc, "daily", 0, 1, "xx", 0, false},
	}
	for _, tc := range tests {
		if got := scheduleDue(tc.now, tc.loc, tc.freq, tc.weekday, tc.monthD, tc.hhmm, tc.last); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeUpdateSchedule(t *testing.T) {
	bad := []updateScheduleReq{
		{Frequency: "hourly", TimeOfDay: "03:00"},
		{Frequency: "daily", TimeOfDay: "25:00"},
		{Frequency: "daily", TimeOfDay: ""},
		{Frequency: "weekly", Weekday: 7, TimeOfDay: "03:00"},
		{Frequency: "weekly", Weekday: -1, TimeOfDay: "03:00"},
		{Frequency: "monthly", MonthDay: 0, TimeOfDay: "03:00"},
		{Frequency: "monthly", MonthDay: 29, TimeOfDay: "03:00"},
		{Frequency: "daily", TimeOfDay: "03:00", Name: strings.Repeat("a", 81)},
	}
	for i, r := range bad {
		if err := normalizeUpdateSchedule(&r); err == nil {
			t.Errorf("case %d should be rejected: %+v", i, r)
		}
	}
	good := []updateScheduleReq{
		{Frequency: "daily", TimeOfDay: "03:00"},
		{Frequency: "weekly", Weekday: 6, TimeOfDay: "23:59", Name: "  Weekly patching "},
		{Frequency: "monthly", MonthDay: 28, TimeOfDay: "00:00"},
		{Frequency: "maintenance"},
		{Frequency: "daily", TimeOfDay: "03:00", Name: strings.Repeat("a", 80)},
	}
	for i, r := range good {
		if err := normalizeUpdateSchedule(&r); err != nil {
			t.Errorf("good case %d: %v", i, err)
		}
	}
	r := updateScheduleReq{Frequency: "weekly", TimeOfDay: "03:00", Name: "  x "}
	_ = normalizeUpdateSchedule(&r)
	if r.Name != "x" || r.MonthDay != 1 {
		t.Errorf("normalised: %+v", r)
	}
}

func TestUpdateScheduleAPIValidation(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	for _, body := range []map[string]any{
		{"scope": "all", "frequency": "monthly", "month_day": 31, "time_of_day": "03:00"},
		{"scope": "all", "frequency": "weekly", "weekday": 9, "time_of_day": "03:00"},
		{"scope": "nowhere", "frequency": "daily", "time_of_day": "03:00"},
		{"scope": "host", "scope_id": 9999, "frequency": "daily", "time_of_day": "03:00"},
	} {
		if code, _, raw := admin.do("POST", "/update-schedules", body); code != 400 {
			t.Errorf("%v: got %d %s", body, code, raw)
		}
	}
	code, m, raw := admin.do("POST", "/update-schedules", map[string]any{"scope": "all", "frequency": "maintenance", "name": "Windows"})
	if code != 201 || m["security_only"] != true {
		t.Fatalf("create: %d %s", code, raw)
	}
}

func TestUpdateScheduleRunner(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	ctx := context.Background()
	okID, _ := e.enrol(admin)
	offID, _ := e.enrol(admin)
	idleID, _ := e.enrol(admin)
	for id, allow := range map[int64]int{okID: 1, offID: 0, idleID: 1} {
		if _, err := e.a.db.Exec(`UPDATE hosts SET allow_upgrades=? WHERE id=?`, allow, id); err != nil {
			t.Fatal(err)
		}
		sec := 3
		if id == idleID {
			sec = 0
		}
		if _, err := e.a.db.Exec(`INSERT OR REPLACE INTO inventory_status (tenant_id, host_id, upgradable_count, security_count, updated_at) VALUES (0,?,?,?,1)`, id, 5, sec); err != nil {
			t.Fatal(err)
		}
	}

	// Daily at 00:00 is always due once the day has started.
	code, m, raw := admin.do("POST", "/update-schedules", map[string]any{"scope": "all", "frequency": "daily", "time_of_day": "00:00"})
	if code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	sid := int64(m["id"].(float64))
	e.a.runUpdateSchedules(ctx)
	var s UpdateSchedule
	if err := e.a.db.Get(&s, `SELECT * FROM update_schedules WHERE id=?`, sid); err != nil {
		t.Fatal(err)
	}
	var res updateRunResult
	if err := json.Unmarshal(s.LastResult, &res); err != nil {
		t.Fatal(err)
	}
	if s.LastRunAt == 0 || res.Queued != 1 || len(res.Skipped) != 2 {
		t.Fatalf("result: last_run=%d %+v", s.LastRunAt, res)
	}
	var n int
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM jobs WHERE host_id=? AND type='upgrade' AND requested_by=?`, okID, updateSchedulePrincipalName(sid))
	if n != 1 {
		t.Fatalf("jobs for allowed host: %d", n)
	}
	// Not run twice on the same day.
	e.a.runUpdateSchedules(ctx)
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM jobs WHERE type='upgrade'`)
	if n != 1 {
		t.Fatalf("second run created jobs: %d", n)
	}

	// Maintenance mode queues once per host until the job finishes.
	if _, err := e.a.db.Exec(`UPDATE update_schedules SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.db.Exec(`DELETE FROM jobs`); err != nil {
		t.Fatal(err)
	}
	code, m, raw = admin.do("POST", "/update-schedules", map[string]any{"scope": "all", "frequency": "maintenance"})
	if code != 201 {
		t.Fatalf("create maint: %d %s", code, raw)
	}
	mid := int64(m["id"].(float64))
	for i := 0; i < 3; i++ {
		e.a.runUpdateSchedules(ctx)
	}
	var st string
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM jobs WHERE type='upgrade' AND requested_by=?`, updateSchedulePrincipalName(mid))
	_ = e.a.db.Get(&st, `SELECT status FROM jobs WHERE type='upgrade' AND requested_by=?`, updateSchedulePrincipalName(mid))
	if n != 1 || st != "scheduled" {
		t.Fatalf("maintenance jobs: %d status %q", n, st)
	}
	// A finished job still blocks the host until the cooldown has passed, so a
	// failing upgrade is not retried every minute while the window is open.
	if _, err := e.a.db.Exec(`UPDATE jobs SET status='failed' WHERE type='upgrade'`); err != nil {
		t.Fatal(err)
	}
	e.a.runUpdateSchedules(ctx)
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM jobs WHERE type='upgrade'`)
	if n != 1 {
		t.Fatalf("within cooldown: %d", n)
	}
	if _, err := e.a.db.Exec(`UPDATE jobs SET created_at=? WHERE type='upgrade'`, time.Now().Add(-maintenanceCooldown-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	e.a.runUpdateSchedules(ctx)
	_ = e.a.db.Get(&n, `SELECT COUNT(*) FROM jobs WHERE type='upgrade'`)
	if n != 2 {
		t.Fatalf("after cooldown: %d", n)
	}
}
