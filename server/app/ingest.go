package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	pb "heartbeat/gen/heartbeatv1"
	"heartbeat/server/db"
	"heartbeat/server/metrics"
)

func (a *App) handleAgentMessage(ctx context.Context, hostID int64, m *pb.AgentMessage) {
	a.latest.touch(hostID)
	switch x := m.Msg.(type) {
	case *pb.AgentMessage_Metrics:
		a.ingestMetrics(ctx, hostID, x.Metrics)
	case *pb.AgentMessage_State:
		a.ingestState(ctx, hostID, x.State)
	case *pb.AgentMessage_Inventory:
		a.ingestInventory(ctx, hostID, x.Inventory)
	case *pb.AgentMessage_Security:
		a.ingestSecurity(ctx, hostID, x.Security)
	case *pb.AgentMessage_JobOutput:
		a.jobs.output(ctx, hostID, x.JobOutput)
	case *pb.AgentMessage_JobResult:
		a.jobs.result(ctx, hostID, x.JobResult)
	case *pb.AgentMessage_Pong:
	case *pb.AgentMessage_Hello:
		// Duplicate hello: ignore.
	}
}

// hostMeta caches tenant id per host to avoid a query per metric batch.
var hostTenantCache sync.Map // int64 -> int64

func (a *App) hostTenant(ctx context.Context, hostID int64) int64 {
	if v, ok := hostTenantCache.Load(hostID); ok {
		return v.(int64)
	}
	var t int64
	_ = a.db.GetContext(ctx, &t, `SELECT tenant_id FROM hosts WHERE id=?`, hostID)
	hostTenantCache.Store(hostID, t)
	return t
}

func (a *App) ingestMetrics(ctx context.Context, hostID int64, b *pb.MetricBatch) {
	tenant := a.hostTenant(ctx, hostID)
	samples := make([]metrics.Sample, 0, len(b.Samples))
	for _, s := range b.Samples {
		samples = append(samples, metrics.Sample{Name: s.Name, Labels: s.Labels, Value: s.Value, TsMs: s.TsMs})
	}
	if err := a.ts.Append(hostID, tenant, samples); err != nil {
		a.log.Debug("tsdb append", "host_id", hostID, "err", err)
	}
	a.updateSummary(hostID, samples)
	a.alerts.evaluateSamples(ctx, hostID, samples)
}

// updateSummary refreshes the headline values for list views.
func (a *App) updateSummary(hostID int64, samples []metrics.Sample) {
	a.latest.update(hostID, func(s *HostSummary) {
		var worstDisk *float64
		for _, smp := range samples {
			v := smp.Value
			switch smp.Name {
			case "cpu_usage_percent":
				s.CPU = &v
			case "mem_used_percent":
				s.Memory = &v
			case "load1_per_core":
				s.Load = &v
			case "uptime_seconds":
				s.Uptime = &v
			case "disk_used_percent":
				if worstDisk == nil || v > *worstDisk {
					vv := v
					worstDisk = &vv
				}
			}
		}
		if worstDisk != nil {
			s.Disk = worstDisk
		}
	})
}

func (a *App) ingestState(ctx context.Context, hostID int64, st *pb.HostState) {
	b, err := protoJSON.Marshal(st)
	if err != nil {
		return
	}
	now := db.Now()
	_, err = a.db.ExecContext(ctx, `UPDATE hosts SET state=?, reboot_required=?, last_seen_at=?, status='up', updated_at=? WHERE id=?`,
		string(b), st.RebootRequired, now, now, hostID)
	if err != nil {
		a.log.Error("state update", "err", err)
	}
	a.syncBlocks(ctx, hostID, st.BlockedIps)
	a.checks.evaluateAgentChecks(ctx, hostID, st)
	a.alerts.evaluateState(ctx, hostID, st)
	a.events.publish(Event{Type: "host.updated", TenantID: a.hostTenant(ctx, hostID), HostID: hostID})
}

// syncBlocks reconciles the ip_blocks table with what the agent reports.
func (a *App) syncBlocks(ctx context.Context, hostID int64, reported []*pb.BlockedIP) {
	active := map[string]bool{}
	for _, b := range reported {
		active[b.Ip] = true
	}
	var rows []IPBlock
	_ = a.db.SelectContext(ctx, &rows, `SELECT * FROM ip_blocks WHERE host_id=? AND status IN ('pending','active')`, hostID)
	for _, r := range rows {
		switch {
		case r.Status == "active" && !active[r.IP]:
			_, _ = a.db.ExecContext(ctx, `UPDATE ip_blocks SET status='removed' WHERE id=?`, r.ID)
		case r.Status == "pending" && active[r.IP]:
			_, _ = a.db.ExecContext(ctx, `UPDATE ip_blocks SET status='active' WHERE id=?`, r.ID)
		}
	}
}

func (a *App) ingestInventory(ctx context.Context, hostID int64, inv *pb.Inventory) {
	tenant := a.hostTenant(ctx, hostID)
	now := db.Now()
	security := 0
	for _, p := range inv.Upgradable {
		if p.Security {
			security++
		}
	}
	// Merge upgradable info into installed rows keyed by name+arch.
	type key struct{ name, arch, kb string }
	rows := map[key]*pb.Package{}
	for _, p := range inv.Installed {
		rows[key{p.Name, p.Arch, p.Kb}] = proto.Clone(p).(*pb.Package)
	}
	for _, p := range inv.Upgradable {
		k := key{p.Name, p.Arch, p.Kb}
		if r, ok := rows[k]; ok {
			r.NewVersion = p.NewVersion
			r.Security = p.Security
		} else {
			// Windows updates and packages missing from the installed list.
			cp := proto.Clone(p).(*pb.Package)
			if cp.Version == "" {
				cp.Version = "-"
			}
			rows[k] = cp
		}
	}
	prevHash := ""
	_ = a.db.GetContext(ctx, &prevHash, `SELECT value FROM settings WHERE tenant_id=0 AND key=?`, "invhash:"+itoa(hostID))
	h := sha256.New()
	keys := make([]key, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].arch+keys[i].kb < keys[j].arch+keys[j].kb
	})
	for _, k := range keys {
		r := rows[k]
		h.Write([]byte(r.Name + "\x00" + r.Version + "\x00" + r.Arch + "\x00"))
	}
	newHash := hex.EncodeToString(h.Sum(nil))

	err := a.db.Tx(ctx, func(tx *sqlxTx) error {
		if inv.Error == "" || len(rows) > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM packages WHERE host_id=?`, hostID); err != nil {
				return err
			}
			stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO packages (tenant_id, host_id, name, version, arch, type, source, new_version, security, kb, publisher)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, k := range keys {
				p := rows[k]
				if _, err := stmt.ExecContext(ctx, tenant, hostID, p.Name, p.Version, p.Arch, p.Type, p.Source, p.NewVersion, p.Security, p.Kb, p.Publisher); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO inventory_status (tenant_id, host_id, package_manager, installed_count, upgradable_count, security_count, error, updated_at)
			VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(host_id) DO UPDATE SET package_manager=excluded.package_manager, installed_count=excluded.installed_count,
			upgradable_count=excluded.upgradable_count, security_count=excluded.security_count, error=excluded.error, updated_at=excluded.updated_at`,
			tenant, hostID, inv.PackageManager, len(inv.Installed), len(inv.Upgradable), security, inv.Error, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO settings (tenant_id, key, value, updated_at) VALUES (0,?,?,?)
			ON CONFLICT(tenant_id, key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, "invhash:"+itoa(hostID), newHash, now)
		return err
	})
	if err != nil {
		a.log.Error("inventory store", "host_id", hostID, "err", err)
		return
	}
	_ = a.ts.Append(hostID, tenant, []metrics.Sample{
		{Name: "packages_upgradable", Value: float64(len(inv.Upgradable))},
		{Name: "packages_security_updates", Value: float64(security)},
		{Name: "packages_installed", Value: float64(len(inv.Installed))},
	})
	a.alerts.evaluateSamples(ctx, hostID, []metrics.Sample{
		{Name: "packages_security_updates", Value: float64(security)},
	})
	a.events.publish(Event{Type: "host.inventory", TenantID: tenant, HostID: hostID})
	if newHash != prevHash && len(inv.Installed) > 0 && a.settings.Vuln().ScanAfterInventory {
		a.vuln.enqueue(hostID, "inventory")
	}
}

func (a *App) ingestSecurity(ctx context.Context, hostID int64, r *pb.SecurityReport) {
	tenant := a.hostTenant(ctx, hostID)
	ws := r.WindowStartMs / 1000
	we := r.WindowEndMs / 1000
	if we == 0 {
		we = db.Now()
	}
	for _, f := range r.FailedLogins {
		if f.Count == 0 || net.ParseIP(f.Ip) == nil {
			continue
		}
		users := f.Usernames
		if len(users) > 20 {
			users = users[:20]
		}
		_, _ = a.db.ExecContext(ctx, `INSERT INTO security_events (tenant_id, host_id, ip, count, usernames, window_start, window_end, source) VALUES (?,?,?,?,?,?,?,?)`,
			tenant, hostID, f.Ip, f.Count, StrList(users), ws, we, r.Source)
	}
	if r.Fail2Ban != nil {
		b, _ := protoJSON.Marshal(r.Fail2Ban)
		_, _ = a.db.ExecContext(ctx, `INSERT INTO fail2ban_status (tenant_id, host_id, data, updated_at) VALUES (?,?,?,?)
			ON CONFLICT(host_id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`, tenant, hostID, string(b), db.Now())
	}
	a.evaluateBruteforce(ctx, hostID)
}

// evaluateBruteforce sums failures per IP over the configured window, raises
// an alert and optionally auto-blocks.
func (a *App) evaluateBruteforce(ctx context.Context, hostID int64) {
	d := a.settings.Agent()
	window := d.BruteforceWindowMin
	if window <= 0 {
		window = 5
	}
	since := time.Now().Add(-time.Duration(window) * time.Minute).Unix()
	type row struct {
		IP    string `db:"ip"`
		Total int    `db:"total"`
	}
	var rows []row
	_ = a.db.SelectContext(ctx, &rows, `SELECT ip, SUM(count) AS total FROM security_events WHERE host_id=? AND window_end>=? GROUP BY ip ORDER BY total DESC`, hostID, since)
	top := 0.0
	topIP := ""
	for _, r := range rows {
		if float64(r.Total) > top {
			top, topIP = float64(r.Total), r.IP
		}
	}
	tenant := a.hostTenant(ctx, hostID)
	_ = a.ts.Append(hostID, tenant, []metrics.Sample{{Name: "auth_failures_window_max", Value: top}})
	a.alerts.evaluateSamples(ctx, hostID, []metrics.Sample{{Name: "auth_failures_window_max", Value: top, Labels: map[string]string{"ip": topIP}}})

	host, err := a.getHost(ctx, hostID)
	if err != nil || !host.AutoBlock || !host.AllowIPBlock || d.AutoBlockThreshold <= 0 {
		return
	}
	for _, r := range rows {
		if r.Total < d.AutoBlockThreshold || ipWhitelisted(r.IP, d.AutoBlockWhitelist) {
			continue
		}
		var n int
		_ = a.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM ip_blocks WHERE host_id=? AND ip=? AND status IN ('pending','active') AND expires_at>?`, hostID, r.IP, db.Now())
		if n > 0 {
			continue
		}
		minutes := d.AutoBlockMinutes
		if minutes <= 0 {
			minutes = 60
		}
		reason := "auto-block: " + itoa(int64(r.Total)) + " failed logins in " + itoa(int64(window)) + "m"
		if _, err := a.blockIP(ctx, host, r.IP, minutes, reason, "system:auto-block"); err != nil {
			a.log.Warn("auto-block failed", "host_id", hostID, "ip", r.IP, "err", err)
		}
	}
}

func ipWhitelisted(ip string, list []string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return true
	}
	if p.IsLoopback() || p.IsUnspecified() {
		return true
	}
	for _, w := range list {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if strings.Contains(w, "/") {
			if _, n, err := net.ParseCIDR(w); err == nil && n.Contains(p) {
				return true
			}
		} else if net.ParseIP(w).Equal(p) {
			return true
		}
	}
	return false
}

// hostState decodes a host's latest state JSON.
func hostState(h *Host) *pb.HostState {
	st := &pb.HostState{}
	if len(h.State) > 2 {
		_ = protoUnmarshalJSON(h.State, st)
	}
	return st
}

func hostInfo(h *Host) *pb.HostInfo {
	hi := &pb.HostInfo{}
	if len(h.HostInfo) > 2 {
		_ = protoUnmarshalJSON(h.HostInfo, hi)
	}
	return hi
}

func decodeJSON[T any](b []byte) T {
	var v T
	if len(b) > 0 {
		_ = json.Unmarshal(b, &v)
	}
	return v
}
