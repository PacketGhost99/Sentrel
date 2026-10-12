package app

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tzdata missing: %v", err)
	}
	return l
}

func TestOneOffWindow(t *testing.T) {
	start := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	w := MaintenanceWindow{Recurrence: "none", StartsAt: start.Unix(), EndsAt: start.Add(2 * time.Hour).Unix()}
	if _, _, ok := w.occurrence(start.Add(-time.Second)); ok {
		t.Error("active before start")
	}
	if _, _, ok := w.occurrence(start.Add(time.Hour)); !ok {
		t.Error("not active inside window")
	}
	if _, _, ok := w.occurrence(start.Add(2 * time.Hour)); ok {
		t.Error("active at end (end is exclusive)")
	}
}

func TestWeeklyWindowAcrossMidnight(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	// Saturdays 23:00 for 3 hours => into Sunday 02:00.
	w := MaintenanceWindow{Recurrence: "weekly", Weekdays: IntList{6}, StartTime: "23:00", DurationMinutes: 180, Timezone: "Europe/London",
		StartsAt: time.Date(2026, 1, 1, 0, 0, 0, 0, loc).Unix()}
	sat := time.Date(2026, 6, 6, 23, 30, 0, 0, loc) // Saturday
	if _, _, ok := w.occurrence(sat); !ok {
		t.Error("expected active Saturday 23:30")
	}
	sun := time.Date(2026, 6, 7, 1, 30, 0, 0, loc)
	if _, _, ok := w.occurrence(sun); !ok {
		t.Error("expected active Sunday 01:30 (spill-over)")
	}
	if _, _, ok := w.occurrence(time.Date(2026, 6, 7, 2, 30, 0, 0, loc)); ok {
		t.Error("expected inactive Sunday 02:30")
	}
	if _, _, ok := w.occurrence(time.Date(2026, 6, 5, 23, 30, 0, 0, loc)); ok {
		t.Error("expected inactive Friday")
	}
	s, e, ok := w.next(time.Date(2026, 6, 8, 12, 0, 0, 0, loc))
	if !ok || s.Weekday() != time.Saturday || e.Sub(s) != 3*time.Hour {
		t.Errorf("next: %v %v %v", s, e, ok)
	}
}

func TestDailyWindowRespectsUntil(t *testing.T) {
	w := MaintenanceWindow{Recurrence: "daily", StartTime: "02:00", DurationMinutes: 60, Timezone: "UTC",
		StartsAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix(), EndsAt: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC).Unix()}
	if _, _, ok := w.occurrence(time.Date(2026, 3, 5, 2, 30, 0, 0, time.UTC)); !ok {
		t.Error("expected active within series")
	}
	if _, _, ok := w.occurrence(time.Date(2026, 3, 12, 2, 30, 0, 0, time.UTC)); ok {
		t.Error("expected inactive after until")
	}
	if _, _, ok := w.occurrence(time.Date(2026, 2, 20, 2, 30, 0, 0, time.UTC)); ok {
		t.Error("expected inactive before first occurrence")
	}
}

func TestWindowScope(t *testing.T) {
	h := hostRef{ID: 3, TenantID: 2, GroupID: 7}
	cases := []struct {
		w    MaintenanceWindow
		want bool
	}{
		{MaintenanceWindow{Scope: "all"}, true},
		{MaintenanceWindow{Scope: "tenant", TenantID: 2}, true},
		{MaintenanceWindow{Scope: "tenant", TenantID: 3}, false},
		{MaintenanceWindow{Scope: "group", ScopeIDs: IntList{7}}, true},
		{MaintenanceWindow{Scope: "group", ScopeIDs: IntList{8}}, false},
		{MaintenanceWindow{Scope: "host", ScopeIDs: IntList{1, 3}}, true},
		{MaintenanceWindow{Scope: "all", TenantID: 9}, false}, // a tenant's window never touches another tenant
	}
	for i, c := range cases {
		if got := c.w.appliesTo(h); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}
