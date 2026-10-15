import { useMemo, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, ArrowLeft, ArrowUpCircle, Bell, ChartNoAxesCombined, Cpu, Gauge, ListChecks, Network, Package, RefreshCw, Settings2, Shield, ShieldAlert, SlidersHorizontal } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, bits, bytes, dateTime, duration, num, osLabel, pct } from '@/lib/format'
import type { Alert, HostDetail as HD, Series } from '@/lib/types'
import { Chart, RangePicker, rangeSeconds, type RangeId, type Unit } from '@/components/Chart'
import { ExportChartsButton, ExportDataButton } from '@/components/MetricsExport'
import { WritePolicyBadge } from '@/components/OperatorSign'
import { Badge, Button, Card, Empty, ErrorBox, Input, KV, Loading, Meter, MutedBadge, Note, PageHeader, SeverityBadge, StatusPill, Table, Td, Th, UptimePill, useConfirm, useToast } from '@/components/ui'
import { BeatBar, averageUptime, hostBeatsToBeats, useHostBeats } from '@/components/BeatBar'
import { hostStatusLabel } from './Hosts'
import { ChecksTable } from './Checks'
import { AlertsTable } from './Alerts'
import { JobsTable } from './Jobs'
import { critHigh, critHighLabel, critHighTitle } from '@/lib/vulns'
import { FindingsTable } from './Vulnerabilities'
import { HostThresholds } from './Thresholds'
import { HostPackages } from './host/Packages'
import { HostSecurity } from './host/Security'
import { HostSettings } from './host/Settings'
import { StoragePanel } from './host/DiskUsage'
import { DiskHealthCard } from './host/DiskHealth'

type Tab = 'overview' | 'metrics' | 'system' | 'interfaces' | 'packages' | 'vulns' | 'security' | 'checks' | 'alerts' | 'jobs' | 'thresholds' | 'settings'

export function HostDetail() {
  const { id = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) || 'overview'
  const setTab = (t: Tab) => setParams({ tab: t }, { replace: true })
  const h = useQuery({ queryKey: ['host', id], queryFn: () => get<HD>(`/hosts/${id}`), refetchInterval: 30_000 })
  const alerts = useQuery({ queryKey: ['alerts', 'host', id], queryFn: () => get<Alert[]>('/alerts', { host_id: id }) })
  const { can } = useAuth()
  if (h.isLoading) return <Loading />
  if (h.error) return <ErrorBox error={h.error} />
  const host = h.data!
  const st = hostStatusLabel(host)
  const agent = host.kind === 'agent'
  const snmp = host.kind === 'snmp'
  const firing = (alerts.data || []).filter((a) => a.status === 'firing').length
  const hostTabs: { id: Tab; label: string; hidden?: boolean; count?: number; countTitle?: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'metrics', label: 'Metrics' },
    { id: 'system', label: 'System', hidden: !agent },
    { id: 'interfaces', label: 'Interfaces', hidden: !snmp },
    { id: 'checks', label: 'Checks', count: host.check_count },
    { id: 'alerts', label: 'Alerts', count: firing },
    { id: 'packages', label: 'Updates', hidden: !agent, count: host.upgrade_count },
    { id: 'vulns', label: 'Vulnerabilities', hidden: !agent || !host.vuln_enabled, count: critHigh(host.vuln_counts ?? host.open_findings), countTitle: critHighTitle(host.vuln_counts) },
    { id: 'security', label: 'Security', hidden: !agent },
    { id: 'jobs', label: 'Jobs', hidden: !agent },
    { id: 'thresholds', label: 'Thresholds' },
    { id: 'settings', label: 'Settings', hidden: !can('host_settings') && !can('hosts') },
  ]
  const hostIcons: Record<Tab, typeof Gauge> = {
    overview: Gauge, metrics: ChartNoAxesCombined, system: Cpu, interfaces: Network,
    checks: Activity, alerts: Bell, packages: Package, vulns: ShieldAlert,
    security: Shield, jobs: ListChecks, thresholds: SlidersHorizontal, settings: Settings2,
  }
  const activeTab = hostTabs.some((t) => t.id === tab && !t.hidden) ? tab : 'overview'


  return (
    <div className="host-workspace">
      <PageHeader
        back={<Link to="/hosts" className="mb-1 inline-flex items-center gap-1 text-xs text-muted hover:text-fg"><ArrowLeft className="size-3" />Hosts</Link>}
        title={host.name}
        subtitle={
          <span className="flex flex-wrap items-center gap-1.5">
            <StatusPill tone={st.tone} label={st.label} />
            <span>{host.address}</span>
            {agent && <span>· {osLabel(host.host_info)}</span>}
            {snmp && host.snmp_info?.system?.descr && <span className="max-w-md truncate">· {host.snmp_info.system.descr}</span>}
            {host.group_name && <Badge>{host.group_name}</Badge>}
            {(host.tags || []).map((t) => <Badge key={t} tone="info">{t}</Badge>)}
            {host.reboot_required && <span className="text-xs text-warn-ink">· Reboot needed</span>}
            {agent && host.vuln_enabled && critHigh(host.vuln_counts) > 0 && <span className="text-xs text-muted" title={critHighTitle(host.vuln_counts)}>· {critHighLabel(host.vuln_counts)} CVEs</span>}
            {host.revoked && <Badge tone="crit">revoked</Badge>}
            {agent && <WritePolicyBadge host={host} />}
          </span>
        }
        actions={agent && <HostActions host={host} />}
      />
      <div className="host-workspace-grid">
        <nav aria-label="Host sections" className="host-section-nav">
          <div className="host-nav-heading">Host workspace</div>
          <div className="host-section-links">
            {hostTabs.filter((t) => !t.hidden).map((t) => {
              const Icon = hostIcons[t.id]
              return <button key={t.id} type="button" onClick={() => setTab(t.id)}
                aria-current={activeTab === t.id ? 'page' : undefined} title={t.id === 'packages' ? 'Packages and updates' : t.label}
                className={`host-section-link ${activeTab === t.id ? 'is-active' : ''}`}>
                <Icon className="size-4 shrink-0" />
                <span className="host-section-label">{t.label}</span>
                {!!t.count && <span title={t.countTitle} className="host-section-count">{t.count.toLocaleString()}</span>}
              </button>
            })}
          </div>
        </nav>
        <div className="host-section-content" key={activeTab}>
          <div className="host-content-heading"><h2>{hostTabs.find((t) => t.id === activeTab)?.label}</h2><span>{host.name}</span></div>
      {activeTab === 'overview' && <Overview host={host} alerts={alerts.data || []} />}
      {activeTab === 'metrics' && <Metrics host={host} />}
      {activeTab === 'system' && <SystemTab host={host} />}
      {activeTab === 'interfaces' && <Interfaces host={host} />}
      {activeTab === 'packages' && <HostPackages host={host} />}
      {activeTab === 'vulns' && <FindingsTable hostId={host.id} />}
      {activeTab === 'security' && <HostSecurity host={host} />}
      {activeTab === 'checks' && <ChecksTable hostId={host.id} hostKind={host.kind} tenantId={host.tenant_id} />}
      {activeTab === 'alerts' && <AlertsTable hostId={host.id} />}
      {activeTab === 'jobs' && <JobsTable hostId={host.id} />}
      {activeTab === 'thresholds' && <HostThresholds host={host} />}
      {activeTab === 'settings' && <HostSettings host={host} />}
        </div>
      </div>
    </div>
  )
}

function HostActions({ host }: { host: HD }) {
  const { can } = useAuth()
  const toast = useToast()
  const confirm = useConfirm()
  const qc = useQueryClient()
  const outdated = host.latest_agent_version && host.agent_version && host.latest_agent_version !== host.agent_version
  return (
    <>
      <Button size="sm" icon={<RefreshCw className="size-3.5" />} onClick={async () => {
        try { await post(`/hosts/${host.id}/refresh-inventory`); toast('Inventory refresh requested') } catch (e) { toast(errorMessage(e), 'crit') }
      }}>Refresh inventory</Button>
      {can('jobs') && outdated && host.write_policy !== 'none' && (
        <Button size="sm" variant="primary" icon={<ArrowUpCircle className="size-3.5" />} onClick={async () => {
          const ok = await confirm({ title: 'Update agent', confirmLabel: 'Update', body: <p>Update the agent on <b>{host.name}</b> from {host.agent_version} to {host.latest_agent_version}? The new binary is signature-checked and self-tested before it replaces the old one, and it rolls back automatically if it cannot reconnect.</p> })
          if (!ok) return
          try { const j = await post<{ status?: string }>(`/hosts/${host.id}/update-agent`, { confirm: true }); toast(j?.status === 'awaiting_signature' ? 'Agent update is waiting for your operator signature (see Jobs)' : 'Agent update queued'); qc.invalidateQueries({ queryKey: ['jobs'] }) } catch (e) { toast(errorMessage(e), 'crit') }
        }}>Update agent</Button>
      )}
    </>
  )
}

function Overview({ host, alerts }: { host: HD; alerts: Alert[] }) {
  const st = host.state || {}
  const hi = host.host_info || {}
  const s = host.summary || {}
  const firing = alerts.filter((a) => a.status !== 'resolved')
  return (
    <div className="grid gap-4 xl:grid-cols-3">
      <div className="space-y-4 xl:col-span-2">
        {firing.length > 0 && (
          <Card title="Active alerts" bodyClass="p-0">
            <ul className="divide-y divide-line/60">
              {firing.map((a) => (
                <li key={a.id} className="flex items-center gap-3 px-5 py-3">
                  <SeverityBadge severity={a.severity} />
                  <Link to={`/alerts/${a.id}`} className="min-w-0 flex-1 truncate text-sm hover:underline">{a.title}<span className="text-muted"> — {a.message}</span></Link>
                  {a.status === 'pending' && <Badge>pending</Badge>}
                  {a.acked_by && <Badge tone="info">acked</Badge>}
                  {a.muted && <MutedBadge />}
                  <span className="text-xs text-muted">{ago(a.fired_at || a.started_at)}</span>
                </li>
              ))}
            </ul>
          </Card>
        )}
        {host.kind !== 'target' && (
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <MiniStat label="CPU" value={s.cpu} />
            <MiniStat label="Memory" value={s.memory} />
            <MiniStat label="Worst disk" value={s.disk} />
            <div className="h-full rounded-md border border-line/70 bg-panel p-4">
              <div className="text-xs font-medium text-muted">Load / core</div>
              <div className="mt-1 text-2xl font-semibold tabular-nums">{s.load !== undefined && s.load !== null ? s.load.toFixed(2) : '–'}</div>
              <div className="text-xs text-muted">uptime {s.uptime ? duration(s.uptime) : st.uptime_seconds ? duration(st.uptime_seconds) : '–'}</div>
            </div>
          </div>
        )}
        {host.kind !== 'target' && <QuickCharts host={host} />}
        {!!st.disks?.length && (
          <Card title="Disks" bodyClass="p-0">
            <Table>
              <thead><tr><Th>Mount</Th><Th>Filesystem</Th><Th>Usage</Th><Th className="hidden sm:table-cell">Size</Th><Th className="hidden md:table-cell">Inodes</Th><Th>Trend (24h)</Th></tr></thead>
              <tbody>
                {st.disks.map((d) => {
                  const eta = num(d.fill_eta_seconds)
                  const inodes = num(d.inodes_total) ? (num(d.inodes_used) * 100) / num(d.inodes_total) : null
                  return (
                    <tr key={d.mountpoint}>
                      <Td className="mono text-xs">{d.mountpoint}</Td>
                      <Td className="text-xs text-muted">{d.fstype} <span className="hidden lg:inline">· {d.device}</span></Td>
                      <Td><Meter value={d.used_percent} /></Td>
                      <Td className="hidden text-xs sm:table-cell">{bytes(d.used)} / {bytes(d.total)}</Td>
                      <Td className="hidden md:table-cell">{inodes !== null ? <Meter value={inodes} /> : '–'}</Td>
                      <Td className="text-xs">
                        {eta > 0 ? <Badge tone={eta < 86400 ? 'crit' : eta < 3 * 86400 ? 'warn' : 'muted'}>full in {duration(eta)}</Badge>
                          : d.growth_bytes_per_hour !== undefined ? <span className="text-muted">{num(d.growth_bytes_per_hour) > 0 ? '+' : ''}{bytes(d.growth_bytes_per_hour)}/h</span> : <span className="text-muted">collecting…</span>}
                      </Td>
                    </tr>
                  )
                })}
              </tbody>
            </Table>
          </Card>
        )}
        <StoragePanel host={host} alerts={alerts} />
        {!!st.collection_errors?.length && (
          <Note tone="warn"><b>Collection warnings:</b> {st.collection_errors.join('; ')}</Note>
        )}
      </div>
      <div className="space-y-4">
        <UptimeCard hostId={host.id} />
        <Card title="Details">
          {host.kind === 'agent' ? (
            <KV stacked items={[
              ['Hostname', hi.hostname, 'wide'],
              ['OS', osLabel(hi)],
              ['Kernel', hi.kernel_version],
              ['Architecture', hi.arch],
              ['Virtualization', hi.virtualization || 'none'],
              ['Memory', hi.memory_total ? bytes(hi.memory_total) : undefined],
              ['Interval', `${host.agent_config?.interval_seconds ?? 30}s`],
              ['Agent', <span key="a">{host.agent_version}{host.latest_agent_version && host.latest_agent_version !== host.agent_version && <Badge tone="warn" className="ml-1">{host.latest_agent_version} available</Badge>}</span>],
              ['Metric storage', host.storage_bytes ? bytes(host.storage_bytes) : 'computed nightly'],
              ['CPU', hi.cpu_model ? `${hi.cpu_model} (${hi.cpu_cores} cores)` : hi.cpu_cores, 'wide'],
              ['IP addresses', (hi.ip_addresses || []).slice(0, 6).join(', '), 'wide'],
              ['Booted', hi.boot_time ? dateTime(num(hi.boot_time)) : undefined],
              ['Enrolled', dateTime(host.created_at)],
              ['Connection', host.connection ? <span key="c" className="text-ok">connected from {host.connection.remote} {host.connection.mtls ? '(mTLS)' : '(signed hello)'} since {ago(host.connection.since)}</span> : <span key="c" className="text-muted">offline · last seen {ago(host.last_seen_at)}</span>, 'wide'],
            ]} />
          ) : host.kind === 'snmp' ? (
            <KV stacked items={[
              ['sysName', host.snmp_info?.system?.name],
              ['Description', host.snmp_info?.system?.descr, 'wide'],
              ['Object ID', <span key="o" className="mono text-xs">{host.snmp_info?.system?.object_id}</span>, 'wide'],
              ['Location', host.snmp_info?.system?.location],
              ['Contact', host.snmp_info?.system?.contact],
              ['Uptime', host.snmp_info?.system?.uptime_seconds ? duration(host.snmp_info.system.uptime_seconds) : undefined],
              ['Profile', host.snmp_info?.profile],
              ['SNMP', `v${host.snmp_config?.version ?? '2c'} · every ${host.snmp_config?.interval_seconds ?? 60}s`],
              ['Last poll', host.snmp_info?.last_poll ? `${ago(host.snmp_info.last_poll)} (${(host.snmp_info.duration_ms ?? 0).toFixed(0)} ms)` : 'never', 'wide'],
              ['Last error', host.snmp_info?.last_error ? <span key="e" className="text-crit">{host.snmp_info.last_error}</span> : undefined, 'wide'],
            ]} />
          ) : (
            <KV stacked items={[['Address', host.address], ['Type', 'Agentless target (checks only)', 'wide'], ['Created', dateTime(host.created_at), 'wide']]} />
          )}
        </Card>
        {host.reboot_required && (
          <Note tone="warn">A reboot is required{st.reboot_required_packages?.length ? <> (packages: {st.reboot_required_packages.join(', ')})</> : null}.</Note>
        )}
        {host.inventory && (
          <Card title="Updates">
            <KV items={[
              ['Package manager', host.inventory.package_manager],
              ['Installed', host.inventory.installed_count],
              ['Upgradable', <span key="u">{host.inventory.upgradable_count} {host.inventory.security_count > 0 && <Badge tone="crit">{host.inventory.security_count} security</Badge>}</span>],
              ['Checked', ago(host.inventory.updated_at)],
              ...(host.inventory.error ? [['Error', <span key="e" className="text-crit">{host.inventory.error}</span>] as [string, React.ReactNode]] : []),
            ]} />
          </Card>
        )}
        {host.notes && <Card title="Notes"><p className="whitespace-pre-wrap text-sm">{host.notes}</p></Card>}
      </div>
    </div>
  )
}

function UptimeCard({ hostId }: { hostId: number }) {
  const beats = useHostBeats(30)
  const days = beats.data?.[String(hostId)]
  return (
    <Card title="Uptime" subtitle="Last 30 days" actions={<UptimePill value={averageUptime(days)} />}>
      <BeatBar beats={hostBeatsToBeats(days)} count={30} height={34} />
    </Card>
  )
}

function MiniStat({ label, value }: { label: string; value?: number | null }) {
  const tone = value === undefined || value === null ? '' : value >= 90 ? 'text-crit-ink' : value >= 80 ? 'text-warn-ink' : ''
  return (
    <div className="h-full rounded-md border border-line/70 bg-panel p-4">
      <div className="text-xs font-medium text-muted">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${tone}`}>{pct(value)}</div>
      <div className="mt-2"><Meter value={value} className="flex-1" showValue={false} /></div>
    </div>
  )
}

function useSeries(hostId: number, panel: string, range: RangeId, enabled = true) {
  return useQuery({
    queryKey: ['metrics', hostId, panel, range],
    queryFn: () => get<{ series: Series[] }>(`/hosts/${hostId}/metrics`, { panel, start: `${rangeSeconds(range)}s` }),
    refetchInterval: range === '1h' ? 30_000 : 120_000,
    enabled,
  })
}

function QuickCharts({ host }: { host: HD }) {
  const cpu = useSeries(host.id, host.kind === 'snmp' ? 'cpu' : 'cpu', '6h')
  const mem = useSeries(host.id, 'memory', '6h')
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card className="h-full" title="CPU (6h)"><Chart series={(cpu.data?.series || []).filter((s) => s.metric === 'cpu_usage_percent')} unit="percent" height={160} max={100} title="CPU" host={host.name} rangeLabel="Last 6h" /></Card>
      <Card className="h-full" title="Memory (6h)"><Chart series={mem.data?.series || []} unit="percent" height={160} max={100} title="Memory" host={host.name} rangeLabel="Last 6h" /></Card>
    </div>
  )
}

const agentPanels: { id: string; title: string; unit: Unit; max?: number }[] = [
  { id: 'cpu', title: 'CPU', unit: 'percent', max: 100 },
  { id: 'memory', title: 'Memory & swap', unit: 'percent', max: 100 },
  { id: 'load', title: 'Load average', unit: 'number' },
  { id: 'disk', title: 'Disk usage', unit: 'percent', max: 100 },
  { id: 'diskio', title: 'Disk I/O', unit: 'bytes/s' },
  { id: 'network', title: 'Network', unit: 'bytes/s' },
  { id: 'temperature', title: 'Temperature', unit: 'celsius' },
  { id: 'processes', title: 'Processes', unit: 'number' },
  { id: 'containers_cpu', title: 'Container CPU', unit: 'percent' },
  { id: 'containers_mem', title: 'Container memory', unit: 'bytes' },
  { id: 'packages', title: 'Pending updates', unit: 'number' },
  { id: 'auth', title: 'Failed logins (max per IP per window)', unit: 'number' },
  { id: 'agent', title: 'Agent footprint (CPU % / RSS)', unit: 'number' },
]
const snmpPanels: typeof agentPanels = [
  { id: 'if_traffic', title: 'Interface traffic', unit: 'bits/s' },
  { id: 'if_util', title: 'Interface utilisation', unit: 'percent', max: 100 },
  { id: 'cpu', title: 'CPU', unit: 'percent', max: 100 },
  { id: 'memory', title: 'Memory', unit: 'percent', max: 100 },
  { id: 'disk', title: 'Storage', unit: 'percent', max: 100 },
  { id: 'temperature', title: 'Temperature', unit: 'celsius' },
  { id: 'wireless', title: 'Wireless', unit: 'number' },
]
const targetPanels: typeof agentPanels = [{ id: 'checks', title: 'Check latency', unit: 'ms' }]

function Metrics({ host }: { host: HD }) {
  const [range, setRange] = useState<RangeId>('6h')
  const panels = host.kind === 'agent' ? agentPanels : host.kind === 'snmp' ? snmpPanels : targetPanels
  const results = useQueries({
    queries: panels.map((p) => ({
      queryKey: ['metrics', host.id, p.id, range],
      queryFn: () => get<{ series: Series[] }>(`/hosts/${host.id}/metrics`, { panel: p.id, start: `${rangeSeconds(range)}s` }),
      refetchInterval: range === '1h' ? 30_000 : 120_000,
    })),
  })
  const exportPanels = panels.map((p, i) => {
    const series = results[i].data?.series || []
    return { title: p.title, unit: p.id === 'agent' ? 'percent' as Unit : p.unit, max: p.max, series: p.id === 'agent' ? series.filter((x) => x.metric === 'agent_cpu_percent') : series }
  })
  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-end gap-2">
        <ExportDataButton hostId={host.id} hostName={host.name} />
        <ExportChartsButton hostName={host.name} rangeLabel={`Last ${range}`} panels={exportPanels} />
        <RangePicker value={range} onChange={setRange} />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        {panels.map((p, i) => {
          const series = results[i].data?.series || []
          if (!results[i].isLoading && series.length === 0 && !['cpu', 'memory', 'network', 'disk', 'if_traffic', 'checks'].includes(p.id)) return null
          const s = p.id === 'agent' ? series.filter((x) => x.metric === 'agent_cpu_percent') : series
          return (
            <Card key={p.id} className="h-full" title={p.title}>
              {results[i].isLoading ? <Loading /> : <Chart series={s} unit={p.id === 'agent' ? 'percent' : p.unit} max={p.max} height={200} title={p.title} host={host.name} rangeLabel={`Last ${range}`} />}
              {p.id === 'agent' && series.find((x) => x.metric === 'agent_rss_bytes') && (
                <p className="mt-2 text-xs text-muted">Agent memory: {bytes(series.find((x) => x.metric === 'agent_rss_bytes')!.points?.slice(-1)[0]?.[1])}</p>
              )}
            </Card>
          )
        })}
      </div>
    </div>
  )
}

function SystemTab({ host }: { host: HD }) {
  const st = host.state || {}
  const [svcQ, setSvcQ] = useState('')
  const services = useMemo(() => (st.services || []).filter((s) => !svcQ || s.name.toLowerCase().includes(svcQ.toLowerCase()) || (s.display_name || '').toLowerCase().includes(svcQ.toLowerCase())), [st.services, svcQ])
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card className="h-full" title="Top processes" bodyClass="p-0" subtitle={st.process_count ? `${st.process_count} processes` : undefined}>
        {!st.top_processes?.length ? <Empty title="No process data" /> : (
          <Table>
            <thead><tr><Th>PID</Th><Th>Name</Th><Th>User</Th><Th>CPU</Th><Th>Mem</Th><Th className="hidden sm:table-cell">RSS</Th></tr></thead>
            <tbody>
              {st.top_processes.map((p) => (
                <tr key={p.pid} title={p.cmdline}>
                  <Td className="mono text-xs">{p.pid}</Td>
                  <Td className="max-w-48 truncate">{p.name}</Td>
                  <Td className="text-xs text-muted">{p.user}</Td>
                  <Td className="tabular-nums">{(p.cpu_percent ?? 0).toFixed(1)}%</Td>
                  <Td className="tabular-nums">{(p.memory_percent ?? 0).toFixed(1)}%</Td>
                  <Td className="hidden text-xs sm:table-cell">{bytes(p.rss)}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card className="h-full" title="Listening ports" bodyClass="p-0">
        {!st.ports?.length ? <Empty title="No listening ports reported" /> : (
          <Table>
            <thead><tr><Th>Port</Th><Th>Protocol</Th><Th>Address</Th><Th>Process</Th></tr></thead>
            <tbody>
              {st.ports.map((p, i) => (
                <tr key={i}><Td className="mono">{p.port}</Td><Td className="text-xs">{p.protocol}</Td><Td className="mono text-xs">{p.address}</Td><Td className="text-xs">{p.process || (p.pid ? `pid ${p.pid}` : <span className="text-muted">–</span>)}</Td></tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card className="h-full" title="Containers" bodyClass="p-0" subtitle={st.docker_available ? undefined : 'Docker not detected'}
        info={st.docker_available ? undefined : 'Docker was not detected, or the agent cannot read the Docker socket. Reinstall the agent with --docker, or run: sudo usermod -aG docker sentrel-agent && sudo systemctl restart sentrel-agent'}>
        {!st.containers?.length ? <Empty title="No containers" /> : (
          <Table>
            <thead><tr><Th>Name</Th><Th>Image</Th><Th>State</Th><Th>CPU</Th><Th>Memory</Th><Th>Restarts</Th></tr></thead>
            <tbody>
              {st.containers.map((c) => (
                <tr key={c.id}>
                  <Td className="font-medium">{c.name}</Td>
                  <Td className="max-w-48 truncate text-xs text-muted" title={c.image}>{c.image}</Td>
                  <Td><Badge tone={c.state === 'running' ? (c.health === 'unhealthy' ? 'crit' : 'ok') : 'crit'}>{c.state}{c.health ? ` · ${c.health}` : ''}</Badge><div className="text-[11px] text-muted">{c.status}</div></Td>
                  <Td className="tabular-nums">{c.state === 'running' ? `${(c.cpu_percent ?? 0).toFixed(1)}%` : '–'}</Td>
                  <Td className="text-xs">{c.state === 'running' ? `${bytes(c.memory_usage)}${num(c.memory_limit) && num(c.memory_limit) < 1e15 ? ' / ' + bytes(c.memory_limit) : ''}` : '–'}</Td>
                  <Td>{c.restart_count ? <Badge tone="warn">{c.restart_count}</Badge> : 0}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card className="h-full" title="Network interfaces" bodyClass="p-0">
        {!st.interfaces?.length ? <Empty title="No interfaces" /> : (
          <Table>
            <thead><tr><Th>Interface</Th><Th>Addresses</Th><Th>Received</Th><Th>Sent</Th><Th>Errors</Th></tr></thead>
            <tbody>
              {st.interfaces.map((i) => (
                <tr key={i.name}>
                  <Td><span className="font-medium">{i.name}</span> {i.up === false && <Badge tone="crit">down</Badge>}<div className="mono text-[11px] text-muted">{i.mac}</div></Td>
                  <Td className="mono text-xs">{(i.addresses || []).join(', ')}</Td>
                  <Td className="text-xs">{bytes(i.bytes_recv)}</Td>
                  <Td className="text-xs">{bytes(i.bytes_sent)}</Td>
                  <Td className="text-xs">{num(i.errors_in) + num(i.errors_out)}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <DiskHealthCard hostId={host.id} />
      {!!st.temperatures?.length && (
        <Card className="h-full" title="Temperatures">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            {st.temperatures.map((t) => (
              <div key={t.sensor} className="rounded-md border border-line/70 p-3">
                <div className="truncate text-xs text-muted">{t.sensor}</div>
                <div className={`text-lg font-semibold ${t.celsius >= 85 ? 'text-crit' : t.celsius >= 75 ? 'text-warn' : ''}`}>{t.celsius.toFixed(1)} °C</div>
              </div>
            ))}
          </div>
        </Card>
      )}
      <Card className="xl:col-span-2" title="Services" bodyClass="p-0" actions={<Input placeholder="Filter services" value={svcQ} onChange={(e) => setSvcQ(e.target.value)} className="h-7 w-48" />}>
        {!services.length ? <Empty title="No services reported">Enable service collection in host settings, or add service checks to watch specific units.</Empty> : (
          <Table className="max-h-[480px] overflow-y-auto">
            <thead><tr><Th>Service</Th><Th>State</Th><Th className="hidden md:table-cell">Start type</Th><Th className="hidden lg:table-cell">Description</Th></tr></thead>
            <tbody>
              {services.map((s) => (
                <tr key={s.name}>
                  <Td className="mono text-xs">{s.name}</Td>
                  <Td><Badge tone={s.state === 'active' || s.state === 'running' ? 'ok' : s.state === 'failed' ? 'crit' : 'muted'}>{s.state}{s.sub_state && s.sub_state !== s.state ? ` (${s.sub_state})` : ''}</Badge></Td>
                  <Td className="hidden text-xs text-muted md:table-cell">{s.start_type}</Td>
                  <Td className="hidden max-w-md truncate text-xs text-muted lg:table-cell">{s.display_name}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  )
}

function Interfaces({ host }: { host: HD }) {
  const lldp = useQuery({ queryKey: ['lldp', host.id], queryFn: () => get<any[]>(`/hosts/${host.id}/lldp`) })
  const ifs = host.snmp_info?.interfaces || []
  return (
    <div className="space-y-4">
      {host.snmp_info?.errors?.length ? <Note tone="warn">Profile warnings: {host.snmp_info.errors.join('; ')}</Note> : null}
      <Card title="Interfaces" bodyClass="p-0" subtitle={`${ifs.length} interfaces`} info="Rates are calculated from the last two polls.">
        {!ifs.length ? <Empty title="No interface data yet" /> : (
          <Table>
            <thead><tr><Th>#</Th><Th>Name</Th><Th>Status</Th><Th>Speed</Th><Th>In</Th><Th>Out</Th><Th>Utilisation</Th><Th className="hidden lg:table-cell">MAC</Th></tr></thead>
            <tbody>
              {ifs.map((i) => (
                <tr key={i.index}>
                  <Td className="text-xs text-muted">{i.index}</Td>
                  <Td><div className="font-medium">{i.name}</div>{i.alias && <div className="text-xs text-muted">{i.alias}</div>}</Td>
                  <Td><Badge tone={!i.admin_up ? 'muted' : i.oper_up ? 'ok' : 'crit'}>{!i.admin_up ? 'admin down' : i.oper_up ? 'up' : 'down'}</Badge></Td>
                  <Td className="text-xs">{i.speed_bps ? bits(i.speed_bps) : '–'}</Td>
                  <Td className="text-xs tabular-nums">{bits(i.in_bps)}</Td>
                  <Td className="text-xs tabular-nums">{bits(i.out_bps)}</Td>
                  <Td>{i.speed_bps && i.oper_up ? <Meter value={i.util_percent} warn={80} crit={95} /> : '–'}</Td>
                  <Td className="mono hidden text-xs lg:table-cell">{i.mac}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card title="LLDP neighbours" bodyClass="p-0">
        {!lldp.data?.length ? <Empty title="No LLDP neighbours">The device does not expose LLDP-MIB or has no neighbours.</Empty> : (
          <Table>
            <thead><tr><Th>Local port</Th><Th>Neighbour</Th><Th>Remote port</Th><Th className="hidden md:table-cell">Chassis ID</Th><Th className="hidden lg:table-cell">Management IP</Th></tr></thead>
            <tbody>
              {lldp.data.map((n, i) => (
                <tr key={i}>
                  <Td>{n.local_port}</Td>
                  <Td>{n.remote_host_id ? <Link className="text-accent hover:underline" to={`/hosts/${n.remote_host_id}`}>{n.remote_sysname}</Link> : n.remote_sysname || '–'}<div className="max-w-sm truncate text-xs text-muted">{n.remote_sysdesc}</div></Td>
                  <Td>{n.remote_port}{n.remote_port_desc && <div className="text-xs text-muted">{n.remote_port_desc}</div>}</Td>
                  <Td className="mono hidden text-xs md:table-cell">{n.remote_chassis}</Td>
                  <Td className="mono hidden text-xs lg:table-cell">{n.remote_mgmt_addr}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  )
}

