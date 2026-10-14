package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pb "sentrel/gen/sentrelv1"
	"sentrel/server/db"
	"sentrel/server/metrics"
)

// Disk health (Linux agents): the latest SMART, software RAID and ZFS snapshot
// per host, a few numeric metrics and alerts. Windows is not covered.

const (
	maxDiskHealthJSON = 256 << 10
	nvmeWearWarnPct   = 90
	// sectorQuietPeriod is how long a bad-sector warning stays raised after
	// the count last rose; after that the new count becomes the baseline.
	sectorQuietPeriod = 7 * 24 * 3600
)

// DiskHealthDevice is one SMART device.
type DiskHealthDevice struct {
	Device        string `json:"device"`
	Model         string `json:"model,omitempty"`
	Serial        string `json:"serial,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	Health        string `json:"health"`
	TemperatureC  *int64 `json:"temperature_c,omitempty"`
	PowerOnHours  *int64 `json:"power_on_hours,omitempty"`
	Reallocated   *int64 `json:"reallocated_sectors,omitempty"`
	Pending       *int64 `json:"pending_sectors,omitempty"`
	Uncorrectable *int64 `json:"uncorrectable_sectors,omitempty"`
	PercentUsed   *int64 `json:"percentage_used,omitempty"`
	MediaErrors   *int64 `json:"media_errors,omitempty"`
	Error         string `json:"error,omitempty"`
	// Set by the server, never taken from the agent: the bad-sector count
	// treated as normal for this drive, and when the count last rose.
	SectorBaseline *int64 `json:"sector_baseline,omitempty"`
	SectorsRoseAt  int64  `json:"sectors_rose_at,omitempty"`
}

// DiskHealthRaid is one md array.
type DiskHealthRaid struct {
	Name       string  `json:"name"`
	Level      string  `json:"level"`
	State      string  `json:"state"`
	Members    int     `json:"members"`
	Working    int     `json:"working"`
	FailedDevs int     `json:"failed_devices"`
	Degraded   bool    `json:"degraded"`
	Failed     bool    `json:"failed"`
	SyncAction string  `json:"sync_action,omitempty"`
	SyncPct    float64 `json:"sync_percent,omitempty"`
	Finish     string  `json:"sync_finish,omitempty"`
}

// DiskHealthPool is one ZFS pool.
type DiskHealthPool struct {
	Name   string `json:"name"`
	Health string `json:"health"`
	Size   string `json:"size"`
	Alloc  string `json:"alloc"`
}

// DiskHealth is the stored snapshot.
type DiskHealth struct {
	SmartAvailable bool               `json:"smart_available"`
	SmartReason    string             `json:"smart_reason,omitempty"`
	Devices        []DiskHealthDevice `json:"devices"`
	Raid           []DiskHealthRaid   `json:"raid"`
	ZfsAvailable   bool               `json:"zfs_available"`
	ZfsError       string             `json:"zfs_error,omitempty"`
	Pools          []DiskHealthPool   `json:"pools"`
	ZfsStatus      string             `json:"zfs_status,omitempty"`
}

// DiskHealthResp is the GET /hosts/{id}/disk-health response.
type DiskHealthResp struct {
	Supported bool       `json:"supported"` // false for Windows hosts
	Known     bool       `json:"known"`     // a snapshot has been received
	UpdatedAt int64      `json:"updated_at"`
	Data      DiskHealth `json:"data"`
}

// sanitizeDiskHealth bounds every list and string from the agent.
func sanitizeDiskHealth(in *DiskHealth) {
	if len(in.Devices) > 64 {
		in.Devices = in.Devices[:64]
	}
	if len(in.Raid) > 64 {
		in.Raid = in.Raid[:64]
	}
	if len(in.Pools) > 64 {
		in.Pools = in.Pools[:64]
	}
	in.SmartReason = truncateStr(in.SmartReason, 200)
	in.ZfsError = truncateStr(in.ZfsError, 200)
	in.ZfsStatus = truncateStr(in.ZfsStatus, 2000)
	for i := range in.Devices {
		d := &in.Devices[i]
		d.Device, d.Model, d.Serial = truncateStr(d.Device, 40), truncateStr(d.Model, 80), truncateStr(d.Serial, 12)
		d.Protocol, d.Error = truncateStr(d.Protocol, 16), truncateStr(d.Error, 200)
		if d.Health != "passed" && d.Health != "failed" {
			d.Health = "unknown"
		}
	}
	for i := range in.Raid {
		r := &in.Raid[i]
		r.Name, r.Level, r.State, r.SyncAction, r.Finish = truncateStr(r.Name, 40), truncateStr(r.Level, 16), truncateStr(r.State, 24), truncateStr(r.SyncAction, 16), truncateStr(r.Finish, 24)
	}
	for i := range in.Pools {
		p := &in.Pools[i]
		p.Name, p.Health, p.Size, p.Alloc = truncateStr(p.Name, 64), truncateStr(p.Health, 24), truncateStr(p.Size, 16), truncateStr(p.Alloc, 16)
	}
	if in.Devices == nil {
		in.Devices = []DiskHealthDevice{}
	}
	if in.Raid == nil {
		in.Raid = []DiskHealthRaid{}
	}
	if in.Pools == nil {
		in.Pools = []DiskHealthPool{}
	}
}

func (a *App) ingestDiskHealth(ctx context.Context, hostID int64, r *pb.DiskHealthReport) {
	if len(r.Json) == 0 || len(r.Json) > maxDiskHealthJSON {
		return
	}
	var cur DiskHealth
	if json.Unmarshal(r.Json, &cur) != nil {
		return
	}
	sanitizeDiskHealth(&cur)
	tenant := a.hostTenant(ctx, hostID)
	var prev DiskHealth
	var prevData string
	if a.db.GetContext(ctx, &prevData, `SELECT data FROM disk_health WHERE host_id=?`, hostID) == nil {
		_ = json.Unmarshal([]byte(prevData), &prev)
	}
	carrySectorBaselines(&prev, &cur, db.Now())
	b, _ := json.Marshal(cur)
	_, _ = a.db.ExecContext(ctx, `INSERT INTO disk_health (tenant_id, host_id, data, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(host_id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at, tenant_id=excluded.tenant_id`, tenant, hostID, string(b), db.Now())

	var samples []metrics.Sample
	for _, d := range cur.Devices {
		lb := map[string]string{"device": d.Device}
		add := func(name string, v *int64) {
			if v != nil {
				samples = append(samples, metrics.Sample{Name: name, Labels: lb, Value: float64(*v)})
			}
		}
		add("smart_temperature_celsius", d.TemperatureC)
		add("smart_reallocated_sectors", d.Reallocated)
		add("smart_pending_sectors", d.Pending)
		add("smart_uncorrectable_sectors", d.Uncorrectable)
		add("smart_nvme_percentage_used", d.PercentUsed)
		add("smart_power_on_hours", d.PowerOnHours)
	}
	if len(samples) > 0 {
		_ = a.ts.Append(hostID, tenant, samples)
	}
	a.applyDiskHealthAlerts(ctx, hostID, &prev, &cur)
}

func sectorTotal(d DiskHealthDevice) int64 {
	var n int64
	for _, p := range []*int64{d.Reallocated, d.Pending, d.Uncorrectable} {
		if p != nil {
			n += *p
		}
	}
	return n
}

// diskHealthConditions maps every alert key the snapshot governs to its
// current condition (severity "ok" when healthy).
func diskHealthConditions(hostID int64, hostName string, tenant int64, s *DiskHealth, prev *DiskHealth) map[string]condition {
	out := map[string]condition{}
	hid := hostID
	base := func(key, source, metric string) condition {
		return condition{Key: "diskhealth:" + itoa(hostID) + ":" + key, TenantID: tenant, HostID: &hid, Source: source, Metric: metric, Severity: "ok", ForCount: 1}
	}
	for _, d := range s.Devices {
		label := d.Device
		if d.Model != "" {
			label += " (" + d.Model + ")"
		}
		h := base("smart:"+d.Device, "disk_health", "smart_health")
		if d.Health == "failed" {
			h.Severity = "critical"
			h.Title = "SMART health check failed on " + hostName
			h.Message = fmt.Sprintf("%s reports a failed SMART overall-health check on %s; back up and replace the drive", label, hostName)
		}
		out[h.Key] = h

		sc := base("sectors:"+d.Device, "disk_health", "smart_sectors")
		if n, b := sectorTotal(d), sectorBaseline(d); n > b {
			sc.Severity = "warning"
			v := float64(n)
			sc.Value = &v
			sc.Title = "Bad sectors increasing on " + label + " on " + hostName
			sc.Message = fmt.Sprintf("%s on %s: %s (up from %d)", label, hostName, sectorSummary(d), b)
		}
		out[sc.Key] = sc

		wr := base("wear:"+d.Device, "disk_health", "smart_nvme_percentage_used")
		if d.PercentUsed != nil && *d.PercentUsed >= nvmeWearWarnPct {
			wr.Severity = "warning"
			v, thr := float64(*d.PercentUsed), float64(nvmeWearWarnPct)
			wr.Value, wr.Threshold = &v, &thr
			wr.Title = "NVMe wear high on " + hostName
			wr.Message = fmt.Sprintf("%s on %s has used %d%% of its rated endurance", label, hostName, *d.PercentUsed)
		}
		out[wr.Key] = wr
	}
	for _, r := range s.Raid {
		c := base("md:"+r.Name, "disk_health", "md_degraded")
		if r.Failed || r.Degraded {
			c.Severity = "critical"
			state := "degraded"
			if r.Failed {
				state = "failed"
			}
			c.Title = fmt.Sprintf("RAID array %s %s on %s", r.Name, state, hostName)
			c.Message = fmt.Sprintf("md array %s (%s) on %s is %s: %d of %d members working", r.Name, r.Level, hostName, state, r.Working, r.Members)
			if r.SyncAction != "" {
				c.Message += fmt.Sprintf("; %s %.1f%% done", r.SyncAction, r.SyncPct)
			}
		}
		out[c.Key] = c
	}
	for _, p := range s.Pools {
		c := base("zfs:"+p.Name, "disk_health", "zfs_pool_health")
		if !strings.EqualFold(p.Health, "ONLINE") {
			c.Severity = "critical"
			c.Title = fmt.Sprintf("ZFS pool %s is %s on %s", p.Name, p.Health, hostName)
			c.Message = fmt.Sprintf("ZFS pool %s on %s reports %s", p.Name, hostName, p.Health)
		}
		out[c.Key] = c
	}
	return out
}

// sectorBaseline is the bad-sector count treated as normal for a device.
func sectorBaseline(d DiskHealthDevice) int64 {
	if d.SectorBaseline != nil {
		return *d.SectorBaseline
	}
	return sectorTotal(d)
}

// carrySectorBaselines sets each device's baseline from the previous snapshot.
// A drive seen for the first time (or replaced: a different serial) starts at
// its current count, so long-stable reallocated sectors do not warn. A rise
// above the baseline warns until sectorQuietPeriod passes without a further
// rise, then the current count becomes the new baseline.
func carrySectorBaselines(prev, cur *DiskHealth, now int64) {
	old := map[string]DiskHealthDevice{}
	for _, d := range prev.Devices {
		old[d.Device] = d
	}
	for i := range cur.Devices {
		d := &cur.Devices[i]
		n := sectorTotal(*d)
		p, seen := old[d.Device]
		if !seen || (p.Serial != "" && d.Serial != "" && p.Serial != d.Serial) {
			d.SectorBaseline, d.SectorsRoseAt = &n, 0
			continue
		}
		base := sectorBaseline(p)
		roseAt := p.SectorsRoseAt
		if n > sectorTotal(p) {
			roseAt = now
		}
		if n < base || (roseAt > 0 && now-roseAt > sectorQuietPeriod) {
			base, roseAt = n, 0
		}
		d.SectorBaseline, d.SectorsRoseAt = &base, roseAt
	}
}

func sectorSummary(d DiskHealthDevice) string {
	var parts []string
	if d.Reallocated != nil && *d.Reallocated > 0 {
		parts = append(parts, fmt.Sprintf("%d reallocated", *d.Reallocated))
	}
	if d.Pending != nil && *d.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", *d.Pending))
	}
	if d.Uncorrectable != nil && *d.Uncorrectable > 0 {
		parts = append(parts, fmt.Sprintf("%d uncorrectable", *d.Uncorrectable))
	}
	return strings.Join(parts, ", ") + " sectors"
}

func (a *App) applyDiskHealthAlerts(ctx context.Context, hostID int64, prev, cur *DiskHealth) {
	h, ok := a.alerts.host(hostID)
	if !ok {
		return
	}
	now := diskHealthConditions(hostID, h.Name, h.TenantID, cur, prev)
	// Devices, arrays and pools that disappeared: clear their alerts.
	for k, c := range diskHealthConditions(hostID, h.Name, h.TenantID, prev, &DiskHealth{}) {
		if _, still := now[k]; !still {
			c.Severity = "ok"
			now[k] = c
		}
	}
	for _, c := range now {
		a.alerts.apply(ctx, c)
	}
}

func (a *App) apiHostDiskHealth(c *Ctx) (any, error) {
	h, err := c.visibleHost(c.ID())
	if err != nil {
		return nil, err
	}
	out := DiskHealthResp{Supported: !strings.EqualFold(hostInfo(h).Os, "windows"), Data: DiskHealth{Devices: []DiskHealthDevice{}, Raid: []DiskHealthRaid{}, Pools: []DiskHealthPool{}}}
	var row struct {
		Data      string `db:"data"`
		UpdatedAt int64  `db:"updated_at"`
	}
	if a.db.GetContext(c.ctx(), &row, `SELECT data, updated_at FROM disk_health WHERE host_id=?`, h.ID) == nil {
		if json.Unmarshal([]byte(row.Data), &out.Data) == nil {
			out.Known, out.UpdatedAt = true, row.UpdatedAt
			sanitizeDiskHealth(&out.Data)
		}
	}
	return out, nil
}
