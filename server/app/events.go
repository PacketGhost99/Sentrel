package app

import (
	"context"
	"sync"
	"time"

	"sentrel/server/db"
)

// Event is pushed to browsers over server-sent events so views refresh
// without polling.
type Event struct {
	Type     string `json:"type"` // host.connected, host.updated, alert.changed, check.changed, job.updated, ...
	TenantID int64  `json:"tenant_id"`
	HostID   int64  `json:"host_id,omitempty"`
	ID       string `json:"id,omitempty"`
}

type eventBus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func newEventBus() *eventBus { return &eventBus{subs: map[chan Event]struct{}{}} }

func (b *eventBus) subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *eventBus) unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (b *eventBus) publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // slow consumer: drop; the UI also refreshes periodically
		}
	}
}

// latestCache keeps headline values per host for list views and the
// /metrics endpoint, avoiding a TSDB query per row.
type latestCache struct {
	mu   sync.RWMutex
	m    map[int64]*HostSummary
	seen map[int64]time.Time
}

func newLatestCache() *latestCache {
	return &latestCache{m: map[int64]*HostSummary{}, seen: map[int64]time.Time{}}
}

func (c *latestCache) touch(hostID int64) {
	c.mu.Lock()
	c.seen[hostID] = time.Now()
	c.mu.Unlock()
}

func (c *latestCache) lastSeen(hostID int64) time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.seen[hostID]
}

func (c *latestCache) update(hostID int64, f func(s *HostSummary)) {
	c.mu.Lock()
	s, ok := c.m[hostID]
	if !ok {
		s = &HostSummary{}
		c.m[hostID] = s
	}
	f(s)
	c.seen[hostID] = time.Now()
	c.mu.Unlock()
}

func (c *latestCache) get(hostID int64) *HostSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if s, ok := c.m[hostID]; ok {
		cp := *s
		return &cp
	}
	return nil
}

func (c *latestCache) forget(hostID int64) {
	c.mu.Lock()
	delete(c.m, hostID)
	delete(c.seen, hostID)
	c.mu.Unlock()
}

// audit log ----------------------------------------------------------------

type auditEntry struct {
	TenantID   int64
	UserID     *int64
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	Details    any
	IP         string
}

func (a *App) audit(ctx context.Context, e auditEntry) {
	details := "{}"
	if e.Details != nil {
		details = mustJSON(e.Details)
	}
	_, err := a.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO audit_log (tenant_id, user_id, actor, action, target_type, target_id, details, ip, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, e.TenantID, e.UserID, e.Actor, e.Action, e.TargetType, e.TargetID, details, e.IP, db.Now())
	if err != nil {
		a.log.Error("audit write failed", "err", err, "action", e.Action)
	}
}
