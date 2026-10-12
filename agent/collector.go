package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/shirou/gopsutil/v4/sensors"

	pb "heartbeat/gen/heartbeatv1"
	"heartbeat/internal/version"
)

// CollectHostInfo gathers static host facts.
func CollectHostInfo(ctx context.Context) *pb.HostInfo {
	hi := &pb.HostInfo{Os: runtime.GOOS, Arch: runtime.GOARCH}
	if h, err := host.InfoWithContext(ctx); err == nil {
		hi.Hostname = h.Hostname
		hi.Platform = h.Platform
		hi.PlatformFamily = h.PlatformFamily
		hi.PlatformVersion = h.PlatformVersion
		hi.KernelVersion = h.KernelVersion
		hi.BootTime = int64(h.BootTime)
		hi.Virtualization = h.VirtualizationSystem
		hi.MachineId = h.HostID
	}
	if hi.Hostname == "" {
		hi.Hostname, _ = os.Hostname()
	}
	if ci, err := cpu.InfoWithContext(ctx); err == nil && len(ci) > 0 {
		hi.CpuModel = strings.TrimSpace(ci[0].ModelName)
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		hi.CpuCores = uint32(n)
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		hi.MemoryTotal = vm.Total
	}
	if runtime.GOOS == "linux" {
		id, ver, pretty := osRelease()
		hi.OsId, hi.OsVersionId = id, ver
		if pretty != "" && hi.Platform == "" {
			hi.Platform = pretty
		}
		if b, err := os.ReadFile("/etc/machine-id"); err == nil {
			hi.MachineId = strings.TrimSpace(string(b))
		}
	}
	if runtime.GOOS == "windows" {
		hi.OsId = "windows"
		hi.OsVersionId = hi.PlatformVersion
	}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 || ignoredIface(i.Name) {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				hi.IpAddresses = append(hi.IpAddresses, ipn.IP.String())
			}
		}
	}
	// Prefer IPv4 first.
	sort.SliceStable(hi.IpAddresses, func(i, j int) bool {
		return net.ParseIP(hi.IpAddresses[i]).To4() != nil && net.ParseIP(hi.IpAddresses[j]).To4() == nil
	})
	return hi
}

func osRelease() (id, versionID, pretty string) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		f, err = os.Open("/usr/lib/os-release")
		if err != nil {
			return
		}
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		k, v, ok := strings.Cut(s.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			id = v
		case "VERSION_ID":
			versionID = v
		case "PRETTY_NAME":
			pretty = v
		}
	}
	return
}

var ignoredIfaceRe = regexp.MustCompile(`^(veth|br-|docker_gwbridge|virbr\d+-nic|lo$|cali|flannel|cni|tap|vnet|isatap|Teredo|Loopback)`)

func ignoredIface(n string) bool { return ignoredIfaceRe.MatchString(n) }

var ignoredFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"devpts": true, "mqueue": true, "debugfs": true, "tracefs": true, "securityfs": true, "pstore": true, "bpf": true, "autofs": true,
	"hugetlbfs": true, "fusectl": true, "configfs": true, "binfmt_misc": true, "nsfs": true, "ramfs": true, "efivarfs": true, "fuse.lxcfs": true,
	"fuse.snapfuse": true, "nfsd": true, "rpc_pipefs": true, "iso9660": true, "udf": true,
}

// Collector produces metrics and host state each interval.
type Collector struct {
	mu sync.Mutex

	prevCPU    *cpu.TimesStat
	prevNet    map[string]gnet.IOCountersStat
	prevDisk   map[string]disk.IOCountersStat
	prevAt     time.Time
	procs      map[int32]*process.Process
	self       *process.Process
	diskHist   map[string][]diskPoint // mount -> 5-minute buckets, 24h
	histPath   string
	restarts   map[string]int // container -> last restart count
	docker     *dockerClient
	lastPorts  []*pb.ListeningPort
	portsAt    time.Time
	lastSvcs   []*pb.Service
	svcsAt     time.Time
	rebootAt   time.Time
	rebootVal  bool
	rebootPkgs []string
}

// InvalidateReboot forces a reboot-required re-check (after upgrades).
func (c *Collector) InvalidateReboot() {
	c.mu.Lock()
	c.rebootAt = time.Time{}
	c.mu.Unlock()
}

type diskPoint struct {
	T    int64   `json:"t"`
	Used float64 `json:"u"`
}

// NewCollector creates a collector; stateDir persists disk history.
func NewCollector(stateDir string) *Collector {
	c := &Collector{prevNet: map[string]gnet.IOCountersStat{}, prevDisk: map[string]disk.IOCountersStat{}, procs: map[int32]*process.Process{},
		diskHist: map[string][]diskPoint{}, restarts: map[string]int{}, docker: newDockerClient()}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		c.self = p
	}
	if stateDir != "" {
		c.histPath = filepath.Join(stateDir, "disk-history.json")
		if b, err := os.ReadFile(c.histPath); err == nil {
			_ = json.Unmarshal(b, &c.diskHist)
		}
	}
	return c
}

// SaveHistory persists the disk history (called periodically and on exit).
func (c *Collector) SaveHistory() {
	if c.histPath == "" {
		return
	}
	c.mu.Lock()
	b, _ := json.Marshal(c.diskHist)
	c.mu.Unlock()
	_ = os.WriteFile(c.histPath, b, 0o600)
}

func smp(name string, v float64, labels ...string) *pb.Sample {
	s := &pb.Sample{Name: name, Value: v}
	if len(labels) >= 2 {
		s.Labels = map[string]string{}
		for i := 0; i+1 < len(labels); i += 2 {
			s.Labels[labels[i]] = labels[i+1]
		}
	}
	return s
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}

// Collect gathers one round of metrics and state.
func (c *Collector) Collect(ctx context.Context, cfg *pb.AgentConfig) (*pb.MetricBatch, *pb.HostState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	ts := now.UnixMilli()
	var samples []*pb.Sample
	st := &pb.HostState{TsMs: ts}
	errs := func(what string, err error) {
		if err != nil {
			st.CollectionErrors = append(st.CollectionErrors, what+": "+err.Error())
		}
	}
	dt := now.Sub(c.prevAt).Seconds()
	first := c.prevAt.IsZero()

	// CPU.
	if t, err := cpu.TimesWithContext(ctx, false); err == nil && len(t) > 0 {
		cur := t[0]
		if c.prevCPU != nil {
			total := cpuTotal(cur) - cpuTotal(*c.prevCPU)
			if total > 0 {
				idle := (cur.Idle + cur.Iowait) - (c.prevCPU.Idle + c.prevCPU.Iowait)
				samples = append(samples,
					smp("cpu_usage_percent", round(clamp100((total-idle)/total*100), 2)),
					smp("cpu_iowait_percent", round(clamp100((cur.Iowait-c.prevCPU.Iowait)/total*100), 2)),
					smp("cpu_steal_percent", round(clamp100((cur.Steal-c.prevCPU.Steal)/total*100), 2)),
				)
			}
		}
		c.prevCPU = &cur
	} else {
		errs("cpu", err)
	}
	cores, _ := cpu.CountsWithContext(ctx, true)
	if cores < 1 {
		cores = 1
	}
	// Load.
	if l, err := load.AvgWithContext(ctx); err == nil {
		samples = append(samples, smp("load1", round(l.Load1, 2)), smp("load5", round(l.Load5, 2)), smp("load15", round(l.Load15, 2)),
			smp("load1_per_core", round(l.Load1/float64(cores), 3)))
	}
	// Memory.
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		used := vm.Total - vm.Available
		samples = append(samples, smp("mem_used_percent", round(float64(used)*100/float64(vm.Total), 2)),
			smp("mem_used_bytes", float64(used)), smp("mem_total_bytes", float64(vm.Total)), smp("mem_available_bytes", float64(vm.Available)))
	} else {
		errs("memory", err)
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil && sw.Total > 0 {
		samples = append(samples, smp("swap_used_percent", round(sw.UsedPercent, 2)), smp("swap_used_bytes", float64(sw.Used)))
	}
	// Disks.
	samples = append(samples, c.collectDisks(ctx, st, now)...)
	// Disk IO.
	if io, err := disk.IOCountersWithContext(ctx); err == nil {
		for name, cur := range io {
			if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "sr") {
				continue
			}
			if prev, ok := c.prevDisk[name]; ok && !first && dt > 0 {
				samples = append(samples,
					smp("disk_read_bytes_per_sec", round(rate(prev.ReadBytes, cur.ReadBytes, dt), 1), "device", name),
					smp("disk_write_bytes_per_sec", round(rate(prev.WriteBytes, cur.WriteBytes, dt), 1), "device", name))
			}
			c.prevDisk[name] = cur
		}
	}
	// Network.
	if nio, err := gnet.IOCountersWithContext(ctx, true); err == nil {
		ifaces := map[string]*pb.NetInterface{}
		if sys, err := net.Interfaces(); err == nil {
			for _, i := range sys {
				ni := &pb.NetInterface{Name: i.Name, Mac: i.HardwareAddr.String(), Up: i.Flags&net.FlagUp != 0}
				addrs, _ := i.Addrs()
				for _, a := range addrs {
					ni.Addresses = append(ni.Addresses, a.String())
				}
				ifaces[i.Name] = ni
			}
		}
		for _, cur := range nio {
			if ignoredIface(cur.Name) {
				continue
			}
			ni := ifaces[cur.Name]
			if ni == nil {
				ni = &pb.NetInterface{Name: cur.Name}
			}
			ni.BytesRecv, ni.BytesSent, ni.ErrorsIn, ni.ErrorsOut = cur.BytesRecv, cur.BytesSent, cur.Errin, cur.Errout
			st.Interfaces = append(st.Interfaces, ni)
			if prev, ok := c.prevNet[cur.Name]; ok && !first && dt > 0 {
				samples = append(samples,
					smp("net_rx_bytes_per_sec", round(rate(prev.BytesRecv, cur.BytesRecv, dt), 1), "iface", cur.Name),
					smp("net_tx_bytes_per_sec", round(rate(prev.BytesSent, cur.BytesSent, dt), 1), "iface", cur.Name),
					smp("net_errors_per_sec", round(rate(prev.Errin+prev.Errout+prev.Dropin, cur.Errin+cur.Errout+cur.Dropin, dt), 3), "iface", cur.Name))
			}
			c.prevNet[cur.Name] = cur
		}
		sort.Slice(st.Interfaces, func(i, j int) bool { return st.Interfaces[i].Name < st.Interfaces[j].Name })
	}
	// Uptime.
	if up, err := host.UptimeWithContext(ctx); err == nil {
		st.UptimeSeconds = up
		samples = append(samples, smp("uptime_seconds", float64(up)))
	}
	// Processes.
	samples = append(samples, c.collectProcesses(ctx, st, cfg)...)
	// Services (expensive on Windows; refresh at most every 60s).
	if cfg.GetCollectServices() || len(cfg.GetWatchedServices()) > 0 {
		if time.Since(c.svcsAt) > 55*time.Second || c.lastSvcs == nil {
			svcs, err := listServices(ctx, cfg.GetCollectServices(), cfg.GetWatchedServices())
			errs("services", err)
			c.lastSvcs, c.svcsAt = svcs, now
		}
		st.Services = c.lastSvcs
	}
	// Listening ports.
	if cfg.GetCollectPorts() {
		if time.Since(c.portsAt) > 55*time.Second || c.lastPorts == nil {
			c.lastPorts, c.portsAt = listeningPorts(ctx), now
		}
		st.Ports = c.lastPorts
	}
	// Temperatures.
	if cfg.GetCollectTemperatures() {
		if temps, err := sensors.TemperaturesWithContext(ctx); err == nil || len(temps) > 0 {
			seen := map[string]bool{}
			for _, t := range temps {
				if t.Temperature <= 0 || t.Temperature > 150 || seen[t.SensorKey] {
					continue
				}
				seen[t.SensorKey] = true
				st.Temperatures = append(st.Temperatures, &pb.Temperature{Sensor: t.SensorKey, Celsius: round(t.Temperature, 1)})
				samples = append(samples, smp("temperature_celsius", round(t.Temperature, 1), "sensor", t.SensorKey))
			}
		}
	}
	// Docker.
	if cfg.GetCollectDocker() {
		cs, ds, available := c.collectDocker(ctx)
		st.DockerAvailable = available
		st.Containers = cs
		samples = append(samples, ds...)
	}
	// Reboot required (can be slow on RHEL: cache for 10 minutes).
	if now.Sub(c.rebootAt) > 10*time.Minute {
		c.rebootVal, c.rebootPkgs = rebootRequired()
		c.rebootAt = now
	}
	st.RebootRequired, st.RebootRequiredPackages = c.rebootVal, c.rebootPkgs
	// Agent footprint.
	if c.self != nil {
		if pct, err := c.self.PercentWithContext(ctx, 0); err == nil && !first {
			samples = append(samples, smp("agent_cpu_percent", round(pct/float64(cores), 3)))
		}
		if mi, err := c.self.MemoryInfoWithContext(ctx); err == nil {
			samples = append(samples, smp("agent_rss_bytes", float64(mi.RSS)))
		}
	}
	for _, s := range samples {
		s.TsMs = ts
	}
	c.prevAt = now
	return &pb.MetricBatch{Samples: samples}, st
}

func cpuTotal(t cpu.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func clamp100(v float64) float64 { return math.Max(0, math.Min(100, v)) }

func rate(prev, cur uint64, dt float64) float64 {
	if cur < prev || dt <= 0 {
		return 0
	}
	return float64(cur-prev) / dt
}

func (c *Collector) collectDisks(ctx context.Context, st *pb.HostState, now time.Time) []*pb.Sample {
	var out []*pb.Sample
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		st.CollectionErrors = append(st.CollectionErrors, "disks: "+err.Error())
		return nil
	}
	seenDev := map[string]bool{}
	seenMount := map[string]bool{}
	for _, p := range parts {
		if ignoredFS[p.Fstype] || seenMount[p.Mountpoint] || strings.HasPrefix(p.Mountpoint, "/snap/") || strings.HasPrefix(p.Mountpoint, "/var/lib/docker/") ||
			strings.HasPrefix(p.Mountpoint, "/run/") {
			continue
		}
		// Bind mounts of the same device: keep the first (shortest) mount.
		if runtime.GOOS != "windows" && seenDev[p.Device] && !strings.HasPrefix(p.Device, "/dev/mapper") {
			continue
		}
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		seenDev[p.Device] = true
		seenMount[p.Mountpoint] = true
		d := &pb.Disk{Mountpoint: p.Mountpoint, Device: p.Device, Fstype: p.Fstype, Total: u.Total, Used: u.Used, UsedPercent: round(u.UsedPercent, 2),
			InodesTotal: u.InodesTotal, InodesUsed: u.InodesUsed, FillEtaSeconds: -1}
		out = append(out, smp("disk_used_percent", d.UsedPercent, "mount", p.Mountpoint),
			smp("disk_used_bytes", float64(u.Used), "mount", p.Mountpoint),
			smp("disk_total_bytes", float64(u.Total), "mount", p.Mountpoint))
		if u.InodesTotal > 0 {
			out = append(out, smp("disk_inodes_used_percent", round(u.InodesUsedPercent, 2), "mount", p.Mountpoint))
		}
		// Prediction.
		slope, ok := c.recordDisk(p.Mountpoint, now, float64(u.Used))
		if ok {
			d.GrowthBytesPerHour = round(slope*3600, 0)
			if slope > 0 {
				eta := float64(u.Free) / slope
				d.FillEtaSeconds = round(eta, 0)
				if eta < 60*24*3600 {
					out = append(out, smp("disk_fill_eta_hours", round(eta/3600, 2), "mount", p.Mountpoint))
				}
			}
		}
		st.Disks = append(st.Disks, d)
	}
	sort.Slice(st.Disks, func(i, j int) bool { return st.Disks[i].Mountpoint < st.Disks[j].Mountpoint })
	return out
}

// recordDisk stores usage in 5-minute buckets over 24 hours and returns the
// least-squares growth rate in bytes/second.
func (c *Collector) recordDisk(mount string, now time.Time, used float64) (float64, bool) {
	bucket := now.Unix() / 300 * 300
	h := c.diskHist[mount]
	if n := len(h); n > 0 && h[n-1].T == bucket {
		h[n-1].Used = used
	} else {
		h = append(h, diskPoint{T: bucket, Used: used})
	}
	cut := now.Add(-24 * time.Hour).Unix()
	i := 0
	for i < len(h) && h[i].T < cut {
		i++
	}
	h = h[i:]
	c.diskHist[mount] = h
	if len(h) < 12 || h[len(h)-1].T-h[0].T < 3600 {
		return 0, false
	}
	return linearSlope(h), true
}

func linearSlope(h []diskPoint) float64 {
	n := float64(len(h))
	var sx, sy, sxx, sxy float64
	t0 := float64(h[0].T)
	for _, p := range h {
		x := float64(p.T) - t0
		sx += x
		sy += p.Used
		sxx += x * x
		sxy += x * p.Used
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

func (c *Collector) collectProcesses(ctx context.Context, st *pb.HostState, cfg *pb.AgentConfig) []*pb.Sample {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		st.CollectionErrors = append(st.CollectionErrors, "processes: "+err.Error())
		return nil
	}
	st.ProcessCount = uint32(len(procs))
	alive := map[int32]bool{}
	names := map[string]bool{}
	type pinfo struct {
		p   *process.Process
		cpu float64
		rss uint64
	}
	var list []pinfo
	for _, p := range procs {
		alive[p.Pid] = true
		cached, ok := c.procs[p.Pid]
		if !ok {
			cached = p
			c.procs[p.Pid] = p
		}
		name, err := cached.NameWithContext(ctx)
		if err == nil && name != "" {
			names[name] = true
		}
		if cfg.GetTopProcesses() > 0 {
			pct, err := cached.PercentWithContext(ctx, 0)
			if err == nil {
				pi := pinfo{p: cached, cpu: pct}
				if mi, err := cached.MemoryInfoWithContext(ctx); err == nil {
					pi.rss = mi.RSS
				}
				list = append(list, pi)
			}
		}
	}
	for pid := range c.procs {
		if !alive[pid] {
			delete(c.procs, pid)
		}
	}
	for n := range names {
		st.ProcessNames = append(st.ProcessNames, n)
	}
	sort.Strings(st.ProcessNames)
	if n := int(cfg.GetTopProcesses()); n > 0 {
		sort.Slice(list, func(i, j int) bool { return list[i].cpu > list[j].cpu })
		// Top by CPU, then fill with top by memory.
		picked := map[int32]bool{}
		add := func(pi pinfo) {
			if picked[pi.p.Pid] {
				return
			}
			picked[pi.p.Pid] = true
			pr := &pb.Process{Pid: pi.p.Pid, CpuPercent: round(pi.cpu, 1)}
			pr.Name, _ = pi.p.NameWithContext(ctx)
			pr.User, _ = pi.p.UsernameWithContext(ctx)
			if mp, err := pi.p.MemoryPercentWithContext(ctx); err == nil {
				pr.MemoryPercent = float32(round(float64(mp), 1))
			}
			pr.Rss = pi.rss
			if cl, err := pi.p.CmdlineWithContext(ctx); err == nil {
				if len(cl) > 300 {
					cl = cl[:300]
				}
				pr.Cmdline = cl
			}
			st.TopProcesses = append(st.TopProcesses, pr)
		}
		for i := 0; i < len(list) && i < n; i++ {
			add(list[i])
		}
		sort.Slice(list, func(i, j int) bool { return list[i].rss > list[j].rss })
		for i := 0; i < len(list) && i < n/2; i++ {
			add(list[i])
		}
	}
	return []*pb.Sample{smp("process_count", float64(len(procs)))}
}

func listeningPorts(ctx context.Context) []*pb.ListeningPort {
	conns, err := gnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	names := map[int32]string{}
	var out []*pb.ListeningPort
	for _, c := range conns {
		proto := ""
		switch {
		case c.Type == 1 && c.Status == "LISTEN": // SOCK_STREAM
			proto = "tcp"
		case c.Type == 2 && c.Raddr.IP == "" && c.Laddr.Port > 0: // SOCK_DGRAM, unconnected
			proto = "udp"
		default:
			continue
		}
		if c.Family == 10 || c.Family == 23 { // AF_INET6 on linux / windows
			proto += "6"
		}
		key := proto + c.Laddr.IP + ":" + itoa(int64(c.Laddr.Port))
		if seen[key] {
			continue
		}
		seen[key] = true
		lp := &pb.ListeningPort{Protocol: proto, Address: c.Laddr.IP, Port: c.Laddr.Port, Pid: c.Pid}
		if c.Pid > 0 {
			if n, ok := names[c.Pid]; ok {
				lp.Process = n
			} else if p, err := process.NewProcessWithContext(ctx, c.Pid); err == nil {
				lp.Process, _ = p.NameWithContext(ctx)
				names[c.Pid] = lp.Process
			}
		}
		out = append(out, lp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

func (c *Collector) collectDocker(ctx context.Context) ([]*pb.Container, []*pb.Sample, bool) {
	if c.docker == nil {
		return nil, nil, false
	}
	cs, err := c.docker.containers(ctx)
	if err != nil {
		return nil, nil, false
	}
	var samples []*pb.Sample
	for _, ct := range cs {
		if ct.State == "running" {
			samples = append(samples, smp("container_cpu_percent", round(ct.CpuPercent, 2), "container", ct.Name),
				smp("container_memory_bytes", float64(ct.MemoryUsage), "container", ct.Name))
		}
		prev, ok := c.restarts[ct.Name]
		c.restarts[ct.Name] = int(ct.RestartCount)
		if ok {
			d := int(ct.RestartCount) - prev
			if d < 0 {
				d = 0
			}
			samples = append(samples, smp("container_restarts_delta", float64(d), "container", ct.Name))
		}
	}
	return cs, samples, true
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// AgentVersion is the running agent version.
func AgentVersion() string { return version.Version }
