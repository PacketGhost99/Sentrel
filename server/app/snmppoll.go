package app

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"heartbeat/server/db"
	"heartbeat/server/metrics"
	"heartbeat/server/snmp"
)

// SNMPPoller polls SNMP hosts on their interval.
type SNMPPoller struct {
	app      *App
	mu       sync.Mutex
	profiles map[string]*snmp.Profile
	profErr  error
	next     map[int64]time.Time
	running  map[int64]bool
	counters map[string]ifCounter // "<host>/<ifIndex>"
	state    *snmp.State            // profile counter metrics
	sem      chan struct{}
}

type ifCounter struct {
	in, out, inErr, outErr uint64
	at                     time.Time
	hc                     bool
}

// SNMPInfo is stored in hosts.snmp_info.
type SNMPInfo struct {
	System     snmp.System      `json:"system"`
	Profile    string           `json:"profile"`
	Interfaces []snmp.Interface `json:"interfaces"`
	Errors     []string         `json:"errors,omitempty"`
	LastPoll   int64            `json:"last_poll"`
	LastError  string           `json:"last_error,omitempty"`
	DurationMs float64          `json:"duration_ms"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
}

func newSNMPPoller(a *App) *SNMPPoller {
	p := &SNMPPoller{app: a, next: map[int64]time.Time{}, running: map[int64]bool{}, counters: map[string]ifCounter{}, state: snmp.NewState(), sem: make(chan struct{}, 16)}
	p.reloadProfiles()
	return p
}

func (p *SNMPPoller) profileDir() string { return filepath.Join(p.app.cfg.DataDir, "snmp-profiles") }

func (p *SNMPPoller) reloadProfiles() error {
	pr, err := snmp.LoadProfiles(p.profileDir())
	p.mu.Lock()
	if pr != nil {
		p.profiles = pr
	}
	p.profErr = err
	p.mu.Unlock()
	if err != nil {
		p.app.log.Error("snmp profiles", "err", err)
	}
	return err
}

// Profiles returns loaded profiles.
func (p *SNMPPoller) Profiles() (map[string]*snmp.Profile, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.profiles, p.profErr
}

// Schedule makes a host poll on the next tick.
func (p *SNMPPoller) Schedule(id int64) {
	p.mu.Lock()
	delete(p.next, id)
	p.mu.Unlock()
}

func (p *SNMPPoller) loop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

type snmpHost struct {
	ID       int64  `db:"id"`
	TenantID int64  `db:"tenant_id"`
	Name     string `db:"name"`
	Address  string `db:"address"`
	Config   JSON   `db:"snmp_config"`
}

func (p *SNMPPoller) tick(ctx context.Context) {
	var hosts []snmpHost
	if err := p.app.db.SelectContext(ctx, &hosts, `SELECT id, tenant_id, name, address, snmp_config FROM hosts WHERE kind='snmp' AND revoked=0`); err != nil {
		return
	}
	now := time.Now()
	for _, h := range hosts {
		var cfg snmp.Config
		_ = json.Unmarshal(h.Config, &cfg)
		iv := cfg.IntervalSec
		if iv < 15 {
			iv = 60
		}
		p.mu.Lock()
		due := !p.running[h.ID] && !now.Before(p.next[h.ID])
		if due {
			p.running[h.ID] = true
			p.next[h.ID] = now.Add(time.Duration(iv) * time.Second)
		}
		p.mu.Unlock()
		if !due {
			continue
		}
		h := h
		go func() {
			p.sem <- struct{}{}
			defer func() { <-p.sem }()
			p.poll(ctx, h, cfg)
			p.mu.Lock()
			delete(p.running, h.ID)
			p.mu.Unlock()
		}()
	}
}

func (p *SNMPPoller) poll(ctx context.Context, h snmpHost, cfg snmp.Config) {
	a := p.app
	profiles, _ := p.Profiles()
	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := snmp.Poll(pctx, h.Address, cfg, profiles, p.state)
	now := time.Now()
	var info SNMPInfo
	var prevInfo JSON
	_ = a.db.GetContext(ctx, &prevInfo, `SELECT snmp_info FROM hosts WHERE id=?`, h.ID)
	_ = json.Unmarshal(prevInfo, &info)
	if err != nil {
		info.LastError = err.Error()
		info.LastPoll = now.Unix()
		_, _ = a.db.ExecContext(ctx, `UPDATE hosts SET snmp_info=?, updated_at=? WHERE id=?`, mustJSON(info), now.Unix(), h.ID)
		a.log.Debug("snmp poll failed", "host", h.Name, "err", err)
		return
	}
	info = SNMPInfo{System: res.System, Profile: res.Profile, Errors: res.Errors, LastPoll: now.Unix(), DurationMs: res.DurationMs, Metrics: map[string]float64{}}
	var samples []metrics.Sample
	samples = append(samples, metrics.Sample{Name: "uptime_seconds", Value: res.System.Uptime}, metrics.Sample{Name: "snmp_poll_ms", Value: res.DurationMs})
	p.mu.Lock()
	for i := range res.Interfaces {
		it := &res.Interfaces[i]
		key := itoa(h.ID) + "/" + strconv.Itoa(it.Index)
		prev, ok := p.counters[key]
		p.counters[key] = ifCounter{in: it.InOctets, out: it.OutOctets, inErr: it.InErrors, outErr: it.OutErrors, at: now, hc: it.HC}
		lbl := map[string]string{"ifname": it.Name}
		oper := 0.0
		if it.OperUp {
			oper = 1
		}
		samples = append(samples, metrics.Sample{Name: "if_oper_up", Labels: lbl, Value: oper})
		if !ok || prev.hc != it.HC {
			continue
		}
		dt := now.Sub(prev.at).Seconds()
		if dt <= 0 {
			continue
		}
		it.InBps = counterRate(prev.in, it.InOctets, it.HC, dt) * 8
		it.OutBps = counterRate(prev.out, it.OutOctets, it.HC, dt) * 8
		errs := counterRate(prev.inErr, it.InErrors, false, dt) + counterRate(prev.outErr, it.OutErrors, false, dt)
		samples = append(samples,
			metrics.Sample{Name: "if_in_bps", Labels: lbl, Value: it.InBps},
			metrics.Sample{Name: "if_out_bps", Labels: lbl, Value: it.OutBps},
			metrics.Sample{Name: "net_errors_per_sec", Labels: map[string]string{"iface": it.Name}, Value: errs},
		)
		if it.SpeedBps > 0 && it.OperUp {
			it.UtilPct = math.Max(it.InBps, it.OutBps) * 100 / float64(it.SpeedBps)
			samples = append(samples, metrics.Sample{Name: "if_util_percent", Labels: lbl, Value: it.UtilPct})
		}
	}
	p.mu.Unlock()
	for _, m := range res.Metrics {
		samples = append(samples, metrics.Sample{Name: m.Name, Labels: m.Labels, Value: m.Value})
		if len(m.Labels) == 0 {
			info.Metrics[m.Name] = m.Value
		}
	}
	info.Interfaces = res.Interfaces
	if err := a.ts.Append(h.ID, h.TenantID, samples); err != nil {
		a.log.Debug("snmp tsdb", "err", err)
	}
	a.updateSummary(h.ID, samples)
	a.alerts.evaluateSamples(ctx, h.ID, samples)
	_, _ = a.db.ExecContext(ctx, `UPDATE hosts SET snmp_info=?, last_seen_at=?, status='up', updated_at=? WHERE id=?`, mustJSON(info), now.Unix(), now.Unix(), h.ID)
	a.latest.touch(h.ID)
	a.alerts.hostSeen(h.ID)
	// LLDP neighbours: replace.
	_ = a.db.Tx(ctx, func(tx *sqlxTx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM lldp_neighbors WHERE host_id=?`, h.ID); err != nil {
			return err
		}
		for _, n := range res.Neighbors {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO lldp_neighbors (tenant_id, host_id, local_port, remote_chassis, remote_port, remote_port_desc,
				remote_sysname, remote_sysdesc, remote_mgmt_addr, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
				h.TenantID, h.ID, n.LocalPort, n.RemoteChassis, n.RemotePort, n.RemotePortDesc, n.RemoteSysName, n.RemoteSysDesc, n.MgmtAddr, now.Unix()); err != nil {
				return err
			}
		}
		return nil
	})
	a.events.publish(Event{Type: "host.updated", TenantID: h.TenantID, HostID: h.ID})
}

// counterRate returns per-second rate handling 32/64-bit wrap.
func counterRate(prev, cur uint64, hc bool, dt float64) float64 {
	if cur >= prev {
		return float64(cur-prev) / dt
	}
	if !hc && prev < 1<<32 {
		return float64((1<<32)-prev+cur) / dt
	}
	// 64-bit wrap is practically impossible; treat as a counter reset.
	return 0
}

// forget drops counter state for a deleted host.
func (p *SNMPPoller) forget(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prefix := itoa(id) + "/"
	for k := range p.counters {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(p.counters, k)
		}
	}
	delete(p.next, id)
}

var _ = db.Now
