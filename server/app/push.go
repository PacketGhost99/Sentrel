package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"sentrel/server/db"
	"sentrel/server/probe"
)

// Push (heartbeat) checks: the monitored job calls a secret URL, and the
// check goes down when no call arrives within interval + grace.

const (
	pushDefaultInterval = 3600
	pushDefaultGrace    = 300
	pushTokenLen        = 32 // base64url chars = 192 bits
	pushBeatsPerMinute  = 60 // per token
	pushMissesPerMinute = 30 // unknown tokens per client address
	pushEvalEvery       = 30 * time.Second
)

// newPushToken returns a random URL token (192 bits) and its lookup hash.
func newPushToken() (token, hash string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("push token: " + err.Error())
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashPushToken(token)
}

func hashPushToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func validPushTokenShape(t string) bool {
	if len(t) != pushTokenLen {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// pushWindow returns the interval and grace of a push check in seconds.
func pushWindow(c *Check) (interval, grace int64) {
	interval, grace = int64(c.IntervalSeconds), int64(checkConfig(c).GraceSeconds)
	if interval <= 0 {
		interval = pushDefaultInterval
	}
	if grace <= 0 {
		grace = pushDefaultGrace
	}
	return
}

// pushState judges a push check at a point in time. evaluated is false while
// the first beat is still due.
func pushState(c *Check, now int64) (res probe.Result, evaluated bool) {
	interval, grace := pushWindow(c)
	base := c.LastBeatAt
	if base == 0 {
		base = c.CreatedAt
	}
	if c.LastBeatAt == 0 && now <= base+interval+grace {
		return probe.Result{}, false
	}
	if now > base+interval+grace {
		msg := "No heartbeat received since the check was created"
		if c.LastBeatAt != 0 {
			msg = "No heartbeat received for " + humanDuration(time.Duration(now-c.LastBeatAt)*time.Second)
		}
		return probe.Result{Message: msg}, true
	}
	if !c.LastBeatUp {
		msg := "Reported down"
		if c.LastBeatMsg != "" {
			msg += ": " + c.LastBeatMsg
		}
		return probe.Result{Message: msg}, true
	}
	msg := c.LastBeatMsg
	if msg == "" {
		msg = "Heartbeat received"
	}
	return probe.Result{Up: true, Message: msg}, true
}

func (r *CheckRunner) freshCheck(ctx context.Context, id int64) (*Check, error) {
	var k Check
	if err := r.app.db.GetContext(ctx, &k, `SELECT * FROM checks WHERE id=?`, id); err != nil {
		return nil, err
	}
	return &k, nil
}

// tickPush judges every enabled push check about every pushEvalEvery.
func (r *CheckRunner) tickPush(ctx context.Context) {
	now := time.Now()
	r.mu.Lock()
	reload := now.Sub(r.pushLoaded) > 15*time.Second
	r.mu.Unlock()
	if reload {
		var ids []int64
		if err := r.app.db.SelectContext(ctx, &ids, `SELECT id FROM checks WHERE type='push' AND enabled=1`); err == nil {
			r.mu.Lock()
			r.pushIDs, r.pushLoaded = ids, now
			r.mu.Unlock()
		}
	}
	var due []int64
	r.mu.Lock()
	for _, id := range r.pushIDs {
		if n, ok := r.pushNext[id]; !ok || !now.Before(n) {
			r.pushNext[id] = now.Add(pushEvalEvery)
			due = append(due, id)
		}
	}
	r.mu.Unlock()
	for _, id := range due {
		// Read the row fresh: a beat may have arrived since the list loaded.
		k, err := r.freshCheck(ctx, id)
		if err != nil || !k.Enabled || k.Type != "push" {
			continue
		}
		if res, ok := pushState(k, db.Now()); ok {
			r.recordWith(ctx, k, res, int(pushEvalEvery/time.Second))
		}
	}
}

func (r *CheckRunner) runPushNow(ctx context.Context, c *Check) probe.Result {
	k, err := r.freshCheck(ctx, c.ID)
	if err != nil {
		return probe.Result{Message: "check not found"}
	}
	res, ok := pushState(k, db.Now())
	if !ok {
		return probe.Result{Message: "Waiting for the first heartbeat"}
	}
	r.recordWith(ctx, k, res, 0)
	return res
}

// beat records one heartbeat for a push check.
func (r *CheckRunner) beat(ctx context.Context, id int64, up bool, msg string) error {
	now := db.Now()
	if _, err := r.app.db.ExecContext(ctx, `UPDATE checks SET last_beat_at=?, last_beat_up=?, last_beat_msg=? WHERE id=?`, now, up, msg, id); err != nil {
		return err
	}
	k, err := r.freshCheck(ctx, id)
	if err != nil {
		return err
	}
	res, _ := pushState(k, now)
	r.recordWith(ctx, k, res, 0)
	return nil
}

// cleanBeatMessage trims and strips control characters from ?msg=.
func cleanBeatMessage(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return truncateStr(strings.TrimSpace(s), 500)
}

// handlePush is the public heartbeat endpoint. It is unauthenticated by
// design: the long random token is the credential. Unknown, malformed and
// disabled tokens all get the same 404, so nothing reveals which exist.
func (a *App) handlePush(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != GET && r.Method != POST {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cr := a.checks
	ip := a.clientIP(r)
	if cr.pushMiss.Count(ip) >= cr.pushMiss.limit {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	miss := func() {
		cr.pushMiss.Allow(ip)
		http.Error(w, "not found", http.StatusNotFound)
	}
	token := chi.URLParam(r, "token")
	if !validPushTokenShape(token) {
		miss()
		return
	}
	hash := hashPushToken(token)
	var k Check
	if err := a.db.GetContext(r.Context(), &k, `SELECT * FROM checks WHERE push_token_hash=? AND type='push' AND enabled=1`, hash); err != nil ||
		subtle.ConstantTimeCompare([]byte(k.PushToken), []byte(token)) != 1 {
		miss()
		return
	}
	if !cr.pushLimit.Allow(hash) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	up := true
	switch s := strings.ToLower(r.URL.Query().Get("status")); s {
	case "", "up", "ok":
	case "down", "fail":
		up = false
	default:
		http.Error(w, "status must be up or down", http.StatusBadRequest)
		return
	}
	if err := cr.beat(r.Context(), k.ID, up, cleanBeatMessage(r.URL.Query().Get("msg"))); err != nil {
		a.log.Error("push beat", "check", k.ID, "err", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// apiRegenerateCheckToken issues a new push URL; the old one stops working.
func (a *App) apiRegenerateCheckToken(c *Ctx) (any, error) {
	k, err := a.visibleCheck(c, c.ID())
	if err != nil {
		return nil, err
	}
	if k.Type != "push" {
		return nil, badRequest("only push checks have a token")
	}
	tok, hash := newPushToken()
	if _, err := a.db.ExecContext(c.ctx(), `UPDATE checks SET push_token=?, push_token_hash=? WHERE id=?`, tok, hash, k.ID); err != nil {
		return nil, err
	}
	a.checks.pushLimit.Reset(k.PushTokenHash)
	c.auditT(k.TenantID, "check.token_regenerated", "check", itoa(k.ID), map[string]any{"name": k.Name})
	nk, err := a.visibleCheck(c, k.ID)
	if err != nil {
		return nil, err
	}
	a.decorateCheck(c, nk)
	return nk, nil
}
