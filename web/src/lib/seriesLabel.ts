// Readable legend names for host metric series. The server names a series
// "<metric>" or "<metric> <label value>"; charts show a short description
// instead, and exports to CSV keep the raw names.

const LABELS: Record<string, string> = {
  cpu_usage_percent: 'Usage',
  cpu_iowait_percent: 'I/O wait',
  cpu_steal_percent: 'Steal',
  mem_used_percent: 'Memory',
  swap_used_percent: 'Swap',
  load1: '1 min',
  load5: '5 min',
  load15: '15 min',
  disk_used_percent: '',
  disk_read_bytes_per_sec: 'Read',
  disk_write_bytes_per_sec: 'Write',
  net_rx_bytes_per_sec: 'Received',
  net_tx_bytes_per_sec: 'Sent',
  temperature_celsius: '',
  cpu_temperature_celsius: 'CPU',
  process_count: 'Processes',
  container_cpu_percent: '',
  container_memory_bytes: '',
  if_in_bps: 'In',
  if_out_bps: 'Out',
  if_util_percent: '',
  agent_cpu_percent: 'Agent CPU',
  agent_rss_bytes: 'Agent memory',
  packages_upgradable: 'Upgradable',
  packages_security_updates: 'Security updates',
  auth_failures_window_max: 'Failed logins',
  vulnerabilities_open: '',
  wireless_signal_dbm: 'Signal',
  wireless_clients: 'Clients',
  check_latency_ms: 'Latency',
}

export function seriesLabel(name: string): string {
  const i = name.indexOf(' ')
  const metric = i < 0 ? name : name.slice(0, i)
  const rest = i < 0 ? '' : name.slice(i + 1)
  const label = LABELS[metric]
  if (label === undefined) return name
  return [label, rest].filter(Boolean).join(' ') || name
}
