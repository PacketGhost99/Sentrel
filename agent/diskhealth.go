package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	pb "sentrel/gen/sentrelv1"
)

// Disk health (Linux only): SMART through the root helper, software RAID from
// /proc/mdstat and ZFS pools through the zpool CLI. The agent sends one JSON
// snapshot about every 30 minutes.

const (
	diskHealthInterval = 30 * time.Minute
	maxSmartDevices    = 32
	maxSmartOutput     = 2 << 20 // bytes read from one smartctl run
)

// SmartDevice is the per-device summary produced by `priv smart`.
type SmartDevice struct {
	Device        string `json:"device"`
	Model         string `json:"model,omitempty"`
	Serial        string `json:"serial,omitempty"` // last 4 characters only
	Protocol      string `json:"protocol,omitempty"`
	Health        string `json:"health"` // passed, failed, unknown
	TemperatureC  *int64 `json:"temperature_c,omitempty"`
	PowerOnHours  *int64 `json:"power_on_hours,omitempty"`
	Reallocated   *int64 `json:"reallocated_sectors,omitempty"`
	Pending       *int64 `json:"pending_sectors,omitempty"`
	Uncorrectable *int64 `json:"uncorrectable_sectors,omitempty"`
	PercentUsed   *int64 `json:"percentage_used,omitempty"`
	MediaErrors   *int64 `json:"media_errors,omitempty"`
	Error         string `json:"error,omitempty"`
}

// SmartReport is the output of `priv smart`.
type SmartReport struct {
	Available bool          `json:"available"`
	Reason    string        `json:"reason,omitempty"`
	Devices   []SmartDevice `json:"devices"`
}

// MdArray is one software RAID array from /proc/mdstat.
type MdArray struct {
	Name       string  `json:"name"`
	Level      string  `json:"level"`
	State      string  `json:"state"` // active, inactive, ...
	Members    int     `json:"members"`
	Working    int     `json:"working"`
	FailedDevs int     `json:"failed_devices"`
	Degraded   bool    `json:"degraded"`
	Failed     bool    `json:"failed"`
	SyncAction string  `json:"sync_action,omitempty"` // resync, recovery, reshape, check
	SyncPct    float64 `json:"sync_percent,omitempty"`
	Finish     string  `json:"sync_finish,omitempty"`
}

// ZPool is one ZFS pool.
type ZPool struct {
	Name   string `json:"name"`
	Health string `json:"health"`
	Size   string `json:"size"`
	Alloc  string `json:"alloc"`
}

// DiskHealth is the snapshot sent to the server.
type DiskHealth struct {
	SmartAvailable bool          `json:"smart_available"`
	SmartReason    string        `json:"smart_reason,omitempty"`
	Devices        []SmartDevice `json:"devices"`
	Raid           []MdArray     `json:"raid"`
	ZfsAvailable   bool          `json:"zfs_available"`
	ZfsError       string        `json:"zfs_error,omitempty"`
	Pools          []ZPool       `json:"pools"`
	ZfsStatus      string        `json:"zfs_status,omitempty"` // output of `zpool status -x` when unhealthy
}

// --- device allowlist ---

var (
	// smartDeviceRe is the only shape of device path ever passed to smartctl.
	smartDeviceRe = regexp.MustCompile(`^/dev/(sd[a-z]{1,3}|hd[a-z]{1,3}|vd[a-z]{1,3}|nvme[0-9]{1,3}n[0-9]{1,3})$`)
	smartTypeRe   = regexp.MustCompile(`^(ata|sat|scsi|nvme)$`)
)

func validSmartDevice(d string) bool { return smartDeviceRe.MatchString(d) }

// maskSerial keeps only the last four characters; enough to tell two drives
// apart without storing a unique hardware identifier.
func maskSerial(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "…" + s
	}
	return "…" + s[len(s)-4:]
}

func clipStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// --- smartctl JSON ---

type nvmeLog struct {
	PercentageUsed *int64 `json:"percentage_used"`
	MediaErrors    *int64 `json:"media_errors"`
	Temperature    *int64 `json:"temperature"`
	PowerOnHours   *int64 `json:"power_on_hours"`
}

type smartctlJSON struct {
	JSONFormat []int `json:"json_format_version"`
	Smartctl   struct {
		Messages []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	Device struct {
		Protocol string `json:"protocol"`
	} `json:"device"`
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
	SmartStatus  *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current *int64 `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours *int64 `json:"hours"`
	} `json:"power_on_time"`
	AtaSmartAttributes *struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	Nvme        *nvmeLog `json:"nvme_smart_health_information_log"`
	ScsiDefects *int64   `json:"scsi_grown_defect_list"`
}

// parseSmartctl summarises the JSON from `smartctl -a -j`. dev is the
// validated path we asked for.
func parseSmartctl(dev string, data []byte) (SmartDevice, error) {
	var j smartctlJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return SmartDevice{Device: dev, Health: "unknown"}, fmt.Errorf("unreadable smartctl output: %w", err)
	}
	if len(j.JSONFormat) == 0 {
		return SmartDevice{Device: dev, Health: "unknown"}, errors.New("not smartctl JSON (smartctl 7.0 or newer is required)")
	}
	d := SmartDevice{Device: dev, Model: clipStr(j.ModelName, 80), Serial: maskSerial(j.SerialNumber), Protocol: clipStr(j.Device.Protocol, 16), Health: "unknown"}
	if j.SmartStatus != nil {
		d.Health = "failed"
		if j.SmartStatus.Passed {
			d.Health = "passed"
		}
	}
	if j.Temperature != nil {
		d.TemperatureC = j.Temperature.Current
	}
	if j.PowerOnTime != nil {
		d.PowerOnHours = j.PowerOnTime.Hours
	}
	if j.AtaSmartAttributes != nil {
		for _, a := range j.AtaSmartAttributes.Table {
			v := a.Raw.Value
			switch a.ID {
			case 5:
				d.Reallocated = &v
			case 197:
				d.Pending = &v
			case 198:
				d.Uncorrectable = &v
			}
		}
	}
	if j.Nvme != nil {
		d.PercentUsed = j.Nvme.PercentageUsed
		d.MediaErrors = j.Nvme.MediaErrors
		if d.TemperatureC == nil {
			d.TemperatureC = j.Nvme.Temperature
		}
		if d.PowerOnHours == nil {
			d.PowerOnHours = j.Nvme.PowerOnHours
		}
	}
	if j.ScsiDefects != nil {
		d.Reallocated = j.ScsiDefects
	}
	if d.Health == "unknown" && d.Model == "" {
		for _, m := range j.Smartctl.Messages {
			if m.Severity == "error" {
				d.Error = clipStr(m.String, 200)
				break
			}
		}
	}
	return d, nil
}

// scanTarget is a validated device with an optional smartctl -d type.
type scanTarget struct{ Name, Type string }

// parseSmartScan returns the allowlisted devices from `smartctl --scan-open -j`.
// Anything not matching the allowlist is silently dropped.
func parseSmartScan(data []byte) ([]scanTarget, error) {
	var s struct {
		Devices []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	var out []scanTarget
	seen := map[string]bool{}
	for _, d := range s.Devices {
		if !validSmartDevice(d.Name) || seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		t := scanTarget{Name: d.Name}
		if smartTypeRe.MatchString(d.Type) {
			t.Type = d.Type
		}
		out = append(out, t)
		if len(out) >= maxSmartDevices {
			break
		}
	}
	return out, nil
}

var smartctlPaths = []string{"/usr/sbin/smartctl", "/usr/bin/smartctl", "/sbin/smartctl", "/usr/local/sbin/smartctl", "/usr/local/bin/smartctl"}

// findSmartctl looks only in fixed system locations, never PATH, because the
// caller is root.
func findSmartctl() string {
	for _, p := range smartctlPaths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// runCapped runs a command without a shell, with a deadline and capped stdout.
// A non-zero exit is not an error here: smartctl uses its exit status as a
// bit mask that is non-zero for ordinary findings.
func runCapped(ctx context.Context, limit int64, name string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait(); _ = pw.Close() }()
	b, _ := io.ReadAll(io.LimitReader(pr, limit))
	_, _ = io.Copy(io.Discard, pr)
	if cctx.Err() != nil {
		return b, cctx.Err()
	}
	return b, nil
}

// collectSmart is the body of the root-run `priv smart` verb.
func collectSmart(ctx context.Context) SmartReport {
	rep := SmartReport{Devices: []SmartDevice{}}
	bin := findSmartctl()
	if bin == "" {
		rep.Reason = "smartctl (smartmontools) is not installed"
		return rep
	}
	out, err := runCapped(ctx, maxSmartOutput, bin, "--scan-open", "-j")
	if err != nil {
		rep.Reason = "smartctl scan failed: " + clipStr(err.Error(), 120)
		return rep
	}
	targets, err := parseSmartScan(out)
	if err != nil {
		rep.Reason = "smartctl could not report JSON (smartmontools 7.0 or newer is required)"
		return rep
	}
	rep.Available = true
	for _, t := range targets {
		args := []string{"-a", "-j"}
		if t.Type != "" {
			args = append(args, "-d", t.Type)
		}
		args = append(args, t.Name)
		b, err := runCapped(ctx, maxSmartOutput, bin, args...)
		if err != nil && len(b) == 0 {
			rep.Devices = append(rep.Devices, SmartDevice{Device: t.Name, Health: "unknown", Error: clipStr(err.Error(), 120)})
			continue
		}
		d, perr := parseSmartctl(t.Name, b)
		if perr != nil {
			d.Error = clipStr(perr.Error(), 120)
		}
		rep.Devices = append(rep.Devices, d)
	}
	return rep
}

// privSmart implements `priv smart`: JSON SmartReport on w.
func privSmart(ctx context.Context, w io.Writer, rest []string) error {
	if len(rest) != 0 {
		return errors.New("usage: smart")
	}
	return json.NewEncoder(w).Encode(collectSmart(ctx))
}

// --- /proc/mdstat ---

var (
	mdHeadRe  = regexp.MustCompile(`^(md[0-9A-Za-z_]+)\s*:\s*(\S+)\s*(.*)$`)
	mdBlockRe = regexp.MustCompile(`\[(\d+)/(\d+)\]\s*\[([U_]+)\]`)
	mdSyncRe  = regexp.MustCompile(`(resync|recovery|reshape|check|repair)\s*=\s*([0-9.]+)%`)
	mdEtaRe   = regexp.MustCompile(`finish=(\S+)`)
)

func parseMdstat(data []byte) []MdArray {
	arrays := []MdArray{}
	var cur *MdArray
	flush := func() {
		if cur == nil {
			return
		}
		missing := cur.Members - cur.Working
		cur.Degraded = cur.Degraded || missing > 0 || cur.FailedDevs > 0
		if cur.State == "inactive" {
			cur.Failed = true
		}
		switch cur.Level {
		case "raid0", "linear":
			if missing > 0 || cur.FailedDevs > 0 {
				cur.Failed = true
			}
		case "raid1":
			if cur.Members > 0 && cur.Working == 0 {
				cur.Failed = true
			}
		case "raid4", "raid5":
			if missing > 1 {
				cur.Failed = true
			}
		case "raid6":
			if missing > 2 {
				cur.Failed = true
			}
		}
		arrays = append(arrays, *cur)
		cur = nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if m := mdHeadRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &MdArray{Name: m[1], State: m[2]}
			for _, f := range strings.Fields(m[3]) {
				switch {
				case cur.Level == "" && (strings.HasPrefix(f, "raid") || f == "linear" || f == "multipath"):
					cur.Level = f
				case strings.Contains(f, "[") && strings.HasSuffix(f, "(F)"):
					cur.FailedDevs++
				}
			}
			continue
		}
		if cur == nil {
			continue
		}
		if m := mdBlockRe.FindStringSubmatch(line); m != nil {
			cur.Members, _ = strconv.Atoi(m[1])
			cur.Working, _ = strconv.Atoi(m[2])
			if strings.Contains(m[3], "_") {
				cur.Degraded = true
			}
		}
		if m := mdSyncRe.FindStringSubmatch(line); m != nil {
			cur.SyncAction = m[1]
			cur.SyncPct, _ = strconv.ParseFloat(m[2], 64)
			if e := mdEtaRe.FindStringSubmatch(line); e != nil {
				cur.Finish = clipStr(e[1], 24)
			}
		}
	}
	flush()
	return arrays
}

// --- ZFS ---

func parseZpoolList(data []byte) []ZPool {
	pools := []ZPool{}
	for _, l := range strings.Split(string(data), "\n") {
		f := strings.Split(strings.TrimRight(l, "\r"), "\t")
		if len(f) < 4 || strings.TrimSpace(f[0]) == "" {
			continue
		}
		pools = append(pools, ZPool{Name: clipStr(f[0], 64), Health: clipStr(f[1], 24), Size: clipStr(f[2], 16), Alloc: clipStr(f[3], 16)})
		if len(pools) >= 64 {
			break
		}
	}
	return pools
}

// zpool list/status are read-only and work for an unprivileged user (the
// /dev/zfs node is world-accessible), so no root helper is involved.
func collectZFS(ctx context.Context, h *DiskHealth) {
	bin, err := exec.LookPath("zpool")
	if err != nil {
		return
	}
	h.ZfsAvailable = true
	out, err := runCapped(ctx, 1<<20, bin, "list", "-H", "-o", "name,health,size,alloc")
	if err != nil {
		h.ZfsError = clipStr(err.Error(), 120)
		return
	}
	h.Pools = parseZpoolList(out)
	st, _ := runCapped(ctx, 64<<10, bin, "status", "-x")
	if s := strings.TrimSpace(string(st)); s != "" && !strings.Contains(s, "all pools are healthy") && !strings.Contains(s, "no pools available") {
		h.ZfsStatus = clipStr(s, 2000)
	}
}

// collectDiskHealth gathers a full snapshot.
func (a *Agent) collectDiskHealth(ctx context.Context) *DiskHealth {
	h := &DiskHealth{Devices: []SmartDevice{}, Raid: []MdArray{}, Pools: []ZPool{}}
	out, err := a.priv(ctx, time.Minute, "smart")
	var sr SmartReport
	if i, j := bytes.IndexByte(out, '{'), bytes.LastIndexByte(out, '}'); i >= 0 && j > i && json.Unmarshal(out[i:j+1], &sr) == nil {
		h.SmartAvailable, h.SmartReason = sr.Available, sr.Reason
		if sr.Devices != nil {
			h.Devices = sr.Devices
		}
	} else {
		h.SmartReason = "SMART helper failed"
		if err != nil {
			h.SmartReason += ": " + clipStr(err.Error(), 120)
		}
	}
	if b, err := os.ReadFile("/proc/mdstat"); err == nil {
		h.Raid = parseMdstat(b)
	}
	collectZFS(ctx, h)
	return h
}

// diskHealthLoop sends a snapshot shortly after start and then every 30
// minutes. Linux only.
func (a *Agent) diskHealthLoop(ctx context.Context) {
	if runtime.GOOS != "linux" {
		return
	}
	next := time.After(2 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-next:
		}
		next = time.After(diskHealthInterval)
		b, err := json.Marshal(a.collectDiskHealth(ctx))
		if err != nil {
			continue
		}
		a.send(&pb.AgentMessage{Msg: &pb.AgentMessage_DiskHealth{DiskHealth: &pb.DiskHealthReport{TsMs: time.Now().UnixMilli(), Json: b}}})
	}
}
