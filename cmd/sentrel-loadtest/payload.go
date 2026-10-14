package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	pb "sentrel/gen/sentrelv1"
)

// The payload shapes mirror a typical Linux server as the real agent reports
// it: about 60 metric samples, a handful of disks and interfaces, a service
// list, listening ports and top processes.

var started = time.Now()

var (
	mounts = []string{"/", "/boot", "/var", "/data"}
	devs   = []string{"sda", "sdb", "nvme0n1", "nvme1n1"}
	ifaces = []string{"eth0", "eth1", "docker0"}
	svcs   = []string{"sshd", "cron", "rsyslog", "systemd-journald", "systemd-logind", "nginx", "postgresql", "redis-server",
		"docker", "containerd", "chrony", "ufw", "fail2ban", "networkd-dispatcher", "unattended-upgrades", "polkit",
		"dbus", "snapd", "multipathd", "irqbalance", "udisks2", "ModemManager", "accounts-daemon", "cloud-init",
		"sentrel-agent", "getty@tty1", "user@1000", "apparmor", "rsync", "lvm2-monitor"}
	procs = []string{"nginx", "postgres", "redis-server", "dockerd", "containerd", "sshd", "python3", "node", "java", "sentrel-agent"}
)

func hostInfo(cfg simConfig, i int) *pb.HostInfo {
	sum := sha256.Sum256([]byte(cfg.hostname(i)))
	return &pb.HostInfo{
		Hostname: cfg.hostname(i), Os: "linux", Platform: "ubuntu", PlatformFamily: "debian", PlatformVersion: "24.04",
		KernelVersion: "6.8.0-45-generic", Arch: "amd64", CpuModel: "Simulated CPU @ 2.40GHz", CpuCores: 4, MemoryTotal: 8 << 30,
		BootTime: time.Now().Add(-72 * time.Hour).Unix(), IpAddresses: []string{fmt.Sprintf("10.%d.%d.%d", 1+i/65025, (i/255)%255, 1+i%255)},
		Virtualization: "kvm", OsId: "ubuntu", OsVersionId: "24.04", MachineId: hex.EncodeToString(sum[:16]),
	}
}

// walk moves a named value by a small random step inside [lo, hi].
func (a *simAgent) walk(name string, lo, hi, step float64) float64 {
	v, ok := a.vals[name]
	if !ok {
		v = lo + a.rng.Float64()*(hi-lo)
	}
	v += (a.rng.Float64() - 0.5) * 2 * step
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	a.vals[name] = v
	return v
}

func smp(ts int64, name string, v float64, labels ...string) *pb.Sample {
	s := &pb.Sample{Name: name, Value: v, TsMs: ts}
	if len(labels) >= 2 {
		s.Labels = map[string]string{}
		for i := 0; i+1 < len(labels); i += 2 {
			s.Labels[labels[i]] = labels[i+1]
		}
	}
	return s
}

// metrics returns 60 samples: host-wide gauges plus per-core, device,
// interface, mount and sensor series.
func (a *simAgent) metrics() *pb.MetricBatch {
	ts := time.Now().UnixMilli()
	const total = 8 << 30
	cpu := a.walk("cpu", 2, 90, 6)
	mem := a.walk("mem", 20, 85, 1)
	var s []*pb.Sample
	add := func(name string, v float64, l ...string) { s = append(s, smp(ts, name, v, l...)) }
	add("cpu_usage_percent", cpu)
	add("cpu_iowait_percent", a.walk("iow", 0, 10, 1))
	add("cpu_steal_percent", a.walk("steal", 0, 3, 0.3))
	add("load1", cpu/25)
	add("load5", cpu/27)
	add("load15", cpu/30)
	add("load1_per_core", cpu/100)
	add("mem_used_percent", mem)
	add("mem_used_bytes", mem/100*total)
	add("mem_total_bytes", total)
	add("mem_available_bytes", (100-mem)/100*total)
	add("swap_used_percent", a.walk("swap", 0, 20, 0.5))
	add("swap_used_bytes", a.walk("swapb", 0, 1<<30, 1<<20))
	add("uptime_seconds", float64(72*3600)+time.Since(started).Seconds())
	add("cpu_user_percent", cpu*0.7)
	add("cpu_system_percent", cpu*0.3)
	add("process_count", a.walk("procs", 120, 260, 4))
	add("agent_cpu_percent", a.walk("acpu", 0.1, 2, 0.1))
	add("agent_rss_bytes", a.walk("arss", 30<<20, 60<<20, 1<<20))
	for c := 0; c < 4; c++ {
		add("cpu_core_usage_percent", a.walk(fmt.Sprint("core", c), 1, 95, 8), "core", fmt.Sprint(c))
	}
	for _, d := range devs {
		add("disk_read_bytes_per_sec", a.walk("r"+d, 0, 5e7, 4e6), "device", d)
		add("disk_write_bytes_per_sec", a.walk("w"+d, 0, 5e7, 4e6), "device", d)
	}
	for _, n := range ifaces {
		add("net_rx_bytes_per_sec", a.walk("rx"+n, 0, 1e8, 5e6), "iface", n)
		add("net_tx_bytes_per_sec", a.walk("tx"+n, 0, 1e8, 5e6), "iface", n)
		add("net_errors_per_sec", 0, "iface", n)
	}
	for _, m := range mounts {
		u := a.walk("du"+m, 20, 90, 0.05)
		add("disk_used_percent", u, "mount", m)
		add("disk_used_bytes", u/100*1e11, "mount", m)
		add("disk_total_bytes", 1e11, "mount", m)
		add("disk_inodes_used_percent", a.walk("di"+m, 1, 30, 0.02), "mount", m)
	}
	for _, t := range []string{"coretemp-0", "coretemp-1", "nvme0", "acpitz"} {
		add("temperature_celsius", a.walk("t"+t, 35, 75, 1.5), "sensor", t)
	}
	return &pb.MetricBatch{Samples: s}
}

func (a *simAgent) state() *pb.HostState {
	st := &pb.HostState{TsMs: time.Now().UnixMilli(), UptimeSeconds: 72 * 3600, ProcessCount: 180, DockerAvailable: true}
	for i, m := range mounts {
		st.Disks = append(st.Disks, &pb.Disk{Mountpoint: m, Device: "/dev/" + devs[i], Fstype: "ext4", Total: 1e11, Used: uint64(a.vals["du"+m] / 100 * 1e11),
			UsedPercent: a.vals["du"+m], InodesTotal: 6e6, InodesUsed: 4e5, FillEtaSeconds: -1, GrowthBytesPerHour: 1e6})
	}
	for i, n := range ifaces {
		st.Interfaces = append(st.Interfaces, &pb.NetInterface{Name: n, Addresses: []string{fmt.Sprintf("10.0.%d.%d/24", i, 10+a.idx%200)},
			Mac: fmt.Sprintf("02:00:00:00:%02x:%02x", a.idx%256, i), Up: true, BytesSent: uint64(time.Now().Unix()) * 1000, BytesRecv: uint64(time.Now().Unix()) * 2000})
	}
	for _, s := range svcs {
		st.Services = append(st.Services, &pb.Service{Name: s + ".service", DisplayName: s, State: "active", SubState: "running", StartType: "enabled"})
	}
	for i, p := range []uint32{22, 80, 443, 5432, 6379, 9100, 8080, 25} {
		st.Ports = append(st.Ports, &pb.ListeningPort{Protocol: "tcp", Address: "0.0.0.0", Port: p, Pid: int32(500 + i*37), Process: procs[i%len(procs)]})
	}
	for i, p := range procs {
		st.TopProcesses = append(st.TopProcesses, &pb.Process{Pid: int32(500 + i*37), Name: p, User: "root", CpuPercent: a.walk("pc"+p, 0, 30, 3),
			MemoryPercent: 2, Rss: 200 << 20, Cmdline: "/usr/bin/" + p + " --config /etc/" + p + "/" + p + ".conf --log-level info"})
	}
	for i := 0; i < 150; i++ {
		st.ProcessNames = append(st.ProcessNames, fmt.Sprintf("proc-%03d", i))
	}
	for i := 0; i < 3; i++ {
		st.Containers = append(st.Containers, &pb.Container{Id: fmt.Sprintf("%064x", i+1), Name: fmt.Sprintf("app%d", i), Image: "registry.example/app:1.2.3",
			State: "running", Status: "Up 3 days", Created: time.Now().Add(-72 * time.Hour).Unix(), CpuPercent: a.walk(fmt.Sprint("cc", i), 0, 20, 2),
			MemoryUsage: 300 << 20, MemoryLimit: 1 << 30})
	}
	st.Temperatures = []*pb.Temperature{{Sensor: "coretemp-0", Celsius: 52}, {Sensor: "nvme0", Celsius: 44}}
	return st
}

func (a *simAgent) inventory() *pb.Inventory {
	inv := &pb.Inventory{TsMs: time.Now().UnixMilli(), PackageManager: "apt", SecurityUpdatesKnown: true}
	for i := 0; i < a.cfg.packages; i++ {
		inv.Installed = append(inv.Installed, &pb.Package{Name: fmt.Sprintf("lib-sim-%04d", i), Version: fmt.Sprintf("1.%d.%d-1ubuntu1", i%20, i%7), Arch: "amd64", Type: "deb", Source: "noble"})
	}
	for i := 0; i < 8 && i < a.cfg.packages; i++ {
		inv.Upgradable = append(inv.Upgradable, &pb.Package{Name: fmt.Sprintf("lib-sim-%04d", i), Version: fmt.Sprintf("1.%d.%d-1ubuntu1", i%20, i%7),
			NewVersion: "2.0-1ubuntu1", Arch: "amd64", Type: "deb", Security: i%3 == 0})
	}
	return inv
}
