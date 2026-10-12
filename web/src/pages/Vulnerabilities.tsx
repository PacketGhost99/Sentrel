import { useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarClock, DatabaseZap, ExternalLink, Plus, ScanSearch, ShieldCheck, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, bytes, dateTime } from '@/lib/format'
import type { Finding, HostGroup, VulnScan } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Checkbox, Empty, ErrorBox, Field, Input, KV, Loading, Modal, Note, PageHeader, Segmented, Select, Stat, Table, Tabs, Td, Th, useToast } from '@/components/ui'

export function sevTone(s: string): 'crit' | 'warn' | 'info' | 'muted' {
  return s === 'Critical' ? 'crit' : s === 'High' ? 'warn' : s === 'Medium' ? 'info' : 'muted'
}

export function Vulnerabilities() {
  const [tab, setTab] = useState<'hosts' | 'findings' | 'scans' | 'schedules'>('hosts')
  const { tenant } = useTenantFilter()
  const { can } = useAuth()
  const toast = useToast()
  const qc = useQueryClient()
  const sum = useQuery({ queryKey: ['vulns', 'summary', tenant], queryFn: () => get<any>('/vulnerabilities/summary', { tenant_id: tenant ?? undefined }), refetchInterval: 30_000 })
  const db = sum.data?.db
  const totals = (sum.data?.hosts || []).reduce((a: any, h: any) => ({ c: a.c + h.critical, h: a.h + h.high, m: a.m + h.medium, f: a.f + h.fixable, k: a.k + h.kev }), { c: 0, h: 0, m: 0, f: 0, k: 0 })
  return (
    <div>
      <PageHeader title="Vulnerabilities" subtitle="Installed packages matched against the Grype vulnerability database on this server. No network scanning; agents only send package lists."
        actions={<>
          {can('jobs') && <Button icon={<ScanSearch className="size-4" />} onClick={async () => { try { const r = await post<any>('/vulnerabilities/scan-all'); toast(`${r.queued} scans queued`) } catch (e) { toast(errorMessage(e), 'crit') } }}>Scan all</Button>}
          {can('settings') && <Button icon={<DatabaseZap className="size-4" />} disabled={db?.updating} onClick={async () => { try { await post('/vulnerabilities/db/sync'); toast('Database update started'); qc.invalidateQueries({ queryKey: ['vulns'] }) } catch (e) { toast(errorMessage(e), 'crit') } }}>Update DB</Button>}
        </>} />
      {sum.data && !sum.data.enabled && <Note tone="warn" className="mb-4">Vulnerability scanning is disabled in Settings → Vulnerabilities.</Note>}
      {db && !db.available && <Note tone="warn" className="mb-4">{db.updating ? 'Downloading the vulnerability database (about 3 GB uncompressed; first download takes several minutes)…' : `Vulnerability database not available yet${db.last_error ? `: ${db.last_error}` : ''}.`}</Note>}
      <div className="mb-4 grid grid-cols-2 gap-3 md:grid-cols-5">
        <Stat label="Critical" value={totals.c} tone={totals.c ? 'crit' : 'muted'} />
        <Stat label="High" value={totals.h} tone={totals.h ? 'warn' : 'muted'} />
        <Stat label="Medium" value={totals.m} tone="muted" />
        <Stat label="Fixable" value={totals.f} sub="have a fixed version" />
        <Stat label="Known exploited" value={totals.k} tone={totals.k ? 'crit' : 'muted'} sub="CISA KEV" />
      </div>
      {db?.available && <p className="mb-3 text-xs text-muted">Database built {dateTime(Date.parse(db.built) / 1000)} · schema {db.schema_version} · {bytes(db.size_bytes)} · checked {ago(Date.parse(db.last_check) / 1000)}{sum.data?.queue ? ` · ${sum.data.queue} scans queued` : ''}{db.updating ? ' · updating…' : ''}</p>}
      <Tabs value={tab} onChange={setTab} className="mb-4" tabs={[{ id: 'hosts', label: 'By host' }, { id: 'findings', label: 'All findings' }, { id: 'scans', label: 'Scan history' }, { id: 'schedules', label: 'Schedules' }]} />
      {tab === 'hosts' && (
        <Card bodyClass="p-0">
          {sum.isLoading ? <Loading /> : !sum.data?.hosts?.length ? <Empty title="No agent hosts" /> : (
            <Table>
              <thead><tr><Th>Host</Th><Th>Critical</Th><Th>High</Th><Th className="hidden sm:table-cell">Medium</Th><Th className="hidden sm:table-cell">Low</Th><Th className="hidden md:table-cell">Fixable</Th><Th className="hidden md:table-cell">KEV</Th><Th>Last scan</Th><Th /></tr></thead>
              <tbody>
                {sum.data.hosts.map((h: any) => (
                  <tr key={h.host_id}>
                    <Td><Link to={`/hosts/${h.host_id}?tab=vulns`} className="font-medium hover:underline">{h.host_name}</Link></Td>
                    <Td>{h.critical ? <Badge tone="crit">{h.critical}</Badge> : 0}</Td>
                    <Td>{h.high ? <Badge tone="warn">{h.high}</Badge> : 0}</Td>
                    <Td className="hidden sm:table-cell">{h.medium}</Td><Td className="hidden sm:table-cell">{h.low}</Td>
                    <Td className="hidden md:table-cell">{h.fixable}</Td><Td className="hidden md:table-cell">{h.kev ? <Badge tone="crit">{h.kev}</Badge> : 0}</Td>
                    <Td className="text-xs">{h.last_scan ? <span title={h.last_error}>{ago(h.last_scan)} <Badge tone={h.last_status === 'completed' ? 'ok' : h.last_status === 'failed' ? 'crit' : 'muted'}>{h.last_status}</Badge></span> : <span className="text-muted">never</span>}</Td>
                    <Td className="text-right">{can('jobs') && <Button size="sm" variant="ghost" icon={<ScanSearch className="size-3.5" />} onClick={async () => { await post(`/hosts/${h.host_id}/scan`); toast('Scan queued') }} />}</Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>
      )}
      {tab === 'findings' && <FindingsTable />}
      {tab === 'scans' && <ScanHistory />}
      {tab === 'schedules' && <Schedules />}
    </div>
  )
}

export function FindingsTable({ hostId }: { hostId?: number }) {
  const { tenant } = useTenantFilter()
  const { can } = useAuth()
  const toast = useToast()
  const [sev, setSev] = useState('Critical,High')
  const [status, setStatus] = useState('open')
  const [fixable, setFixable] = useState(false)
  const [kev, setKev] = useState(false)
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<Finding | null>(null)
  const f = useQuery({ queryKey: ['vulns', 'findings', hostId, tenant, sev, status, fixable, kev, q], queryFn: () => get<Finding[]>('/vulnerabilities', { host_id: hostId, tenant_id: tenant ?? undefined, severity: sev, status, fixable: fixable ? 1 : '', kev: kev ? 1 : '', q, limit: 2000 }) })
  const scans = useQuery({ queryKey: ['vulns', 'scans', hostId], queryFn: () => get<VulnScan[]>('/vulnerabilities/scans', { host_id: hostId }), enabled: !!hostId })
  const last = scans.data?.[0]
  return (
    <div className="space-y-3">
      {hostId && (
        <div className="flex flex-wrap items-center gap-3">
          {last ? <span className="text-sm text-muted">Last scan {ago(last.started_at)}: <Badge tone={last.status === 'completed' ? 'ok' : last.status === 'failed' ? 'crit' : 'muted'}>{last.status}</Badge> {last.packages} packages, {last.findings} findings {last.error && <span className="text-crit">— {last.error}</span>}</span> : <span className="text-sm text-muted">Not scanned yet.</span>}
          {can('jobs') && <Button size="sm" icon={<ScanSearch className="size-3.5" />} onClick={async () => { try { const r = await post<any>(`/hosts/${hostId}/scan`); toast(r.queued ? 'Scan queued' : r.message, r.queued ? 'ok' : 'info') } catch (e) { toast(errorMessage(e), 'crit') } }}>Scan now</Button>}
        </div>
      )}
      <Card bodyClass="p-0">
        <div className="flex flex-wrap items-center gap-2 border-b border-line p-3">
          <Segmented value={sev} onChange={setSev} options={[{ id: 'Critical', label: 'Critical' }, { id: 'Critical,High', label: 'High+' }, { id: 'Critical,High,Medium', label: 'Medium+' }, { id: '', label: 'All' }]} />
          <Segmented value={status} onChange={setStatus} options={[{ id: 'open', label: 'Open' }, { id: 'ignored', label: 'Ignored' }, { id: 'fixed', label: 'Fixed' }]} />
          <Checkbox checked={fixable} onChange={setFixable} label="Fix available" />
          <Checkbox checked={kev} onChange={setKev} label="Known exploited" />
          <Input placeholder="CVE or package" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-48" />
        </div>
        {f.isLoading ? <Loading /> : !f.data?.length ? <Empty title="No findings" icon={<ShieldCheck className="size-8 text-ok" />} /> : (
          <Table>
            <thead><tr><Th>Vulnerability</Th><Th>Severity</Th><Th className="hidden sm:table-cell">CVSS</Th><Th>Package</Th><Th className="hidden md:table-cell">Installed</Th><Th className="hidden md:table-cell">Fixed in</Th>{!hostId && <Th className="hidden lg:table-cell">Host</Th>}<Th className="hidden xl:table-cell">First seen</Th></tr></thead>
            <tbody>
              {f.data.map((x) => (
                <tr key={x.id} className="cursor-pointer hover:bg-panel-2/60" onClick={() => setOpen(x)}>
                  <Td className="mono text-xs">{x.vuln_id} {x.kev && <Badge tone="crit">KEV</Badge>}</Td>
                  <Td><Badge tone={sevTone(x.severity)}>{x.severity}</Badge></Td>
                  <Td className="hidden tabular-nums sm:table-cell">{x.cvss ? x.cvss.toFixed(1) : '–'}</Td>
                  <Td className="mono text-xs" title={x.package}>{x.package.split(', ')[0]}{x.package.includes(', ') && <Badge className="ml-1">+{x.package.split(', ').length - 1}</Badge>}</Td>
                  <Td className="mono hidden text-xs md:table-cell">{x.version}</Td>
                  <Td className="mono hidden text-xs md:table-cell">{x.fixed_version ? <span className="text-ok">{x.fixed_version}</span> : <span className="text-muted">{x.fix_state || 'no fix'}</span>}</Td>
                  {!hostId && <Td className="hidden text-xs lg:table-cell">{x.host_name}</Td>}
                  <Td className="hidden text-xs text-muted xl:table-cell">{ago(x.first_seen_at)}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
        {f.data && f.data.length >= 2000 && <p className="p-3 text-xs text-muted">Showing the first 2000 findings; narrow the filters to see more.</p>}
      </Card>
      {open && <FindingModal f={open} onClose={() => { setOpen(null); f.refetch() }} />}
    </div>
  )
}

function FindingModal({ f, onClose }: { f: Finding; onClose: () => void }) {
  const { can } = useAuth()
  const toast = useToast()
  const [reason, setReason] = useState('')
  const setStatus = async (status: string) => {
    try { await put(`/vulnerabilities/${f.id}`, { status, reason }); toast(status === 'ignored' ? 'Finding ignored' : 'Finding reopened'); onClose() } catch (e) { toast(errorMessage(e), 'crit') }
  }
  return (
    <Modal open onClose={onClose} title={f.vuln_id} size="lg" footer={can('monitoring') && (f.status === 'ignored'
      ? <Button onClick={() => setStatus('open')}>Reopen</Button>
      : f.status === 'open' ? <><Input placeholder="Reason (e.g. not reachable, mitigated)" value={reason} onChange={(e) => setReason(e.target.value)} className="w-72" /><Button onClick={() => setStatus('ignored')}>Ignore</Button></> : null)}>
      <div className="space-y-4">
        <div className="flex flex-wrap gap-2"><Badge tone={sevTone(f.severity)}>{f.severity}</Badge>{f.kev && <Badge tone="crit">Known exploited (CISA KEV)</Badge>}<Badge>{f.status}</Badge></div>
        <KV items={[
          ['Packages', <span key="p" className="mono break-all">{f.package} ({f.version})</span>],
          ['Fixed in', f.fixed_version || f.fix_state || 'no fix available'],
          ['CVSS', f.cvss ? `${f.cvss.toFixed(1)}  ${f.cvss_vector}` : '–'],
          ['EPSS', f.epss ? `${(f.epss * 100).toFixed(2)}% probability of exploitation in 30 days` : '–'],
          ['Host', f.host_name || `#${f.host_id}`],
          ['First seen', dateTime(f.first_seen_at)],
          ['Last seen', dateTime(f.last_seen_at)],
        ]} />
        {f.description && <p className="whitespace-pre-line text-sm">{f.description}</p>}
        {!!f.urls?.length && <ul className="space-y-1 text-sm">{f.urls.map((u) => <li key={u}><a href={u} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1 break-all text-accent hover:underline">{u}<ExternalLink className="size-3" /></a></li>)}</ul>}
      </div>
    </Modal>
  )
}

function ScanHistory() {
  const s = useQuery({ queryKey: ['vulns', 'scans'], queryFn: () => get<VulnScan[]>('/vulnerabilities/scans'), refetchInterval: 15_000 })
  return (
    <Card bodyClass="p-0">
      {!s.data?.length ? <Empty title="No scans yet" /> : (
        <Table>
          <thead><tr><Th>Host</Th><Th>Status</Th><Th>Trigger</Th><Th className="hidden sm:table-cell">Packages</Th><Th>Findings</Th><Th className="hidden md:table-cell">Started</Th><Th className="hidden lg:table-cell">Duration</Th></tr></thead>
          <tbody>{s.data.map((x) => (
            <tr key={x.id}>
              <Td><Link to={`/hosts/${x.host_id}?tab=vulns`} className="hover:underline">{x.host_name}</Link></Td>
              <Td><Badge tone={x.status === 'completed' ? 'ok' : x.status === 'failed' ? 'crit' : 'muted'}>{x.status}</Badge>{x.error && <div className="max-w-xs truncate text-xs text-muted" title={x.error}>{x.error}</div>}</Td>
              <Td className="text-xs">{x.triggered_by}</Td>
              <Td className="hidden sm:table-cell">{x.packages}</Td>
              <Td className="text-xs">{x.findings} {x.critical > 0 && <Badge tone="crit">{x.critical} crit</Badge>} {x.high > 0 && <Badge tone="warn">{x.high} high</Badge>}</Td>
              <Td className="hidden text-xs md:table-cell">{dateTime(x.started_at)}</Td>
              <Td className="hidden text-xs lg:table-cell">{x.finished_at ? `${x.finished_at - x.started_at}s` : '…'}</Td>
            </tr>
          ))}</tbody>
        </Table>
      )}
    </Card>
  )
}

const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

function Schedules() {
  const { can, me } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const [adding, setAdding] = useState(false)
  const s = useQuery({ queryKey: ['vulns', 'schedules'], queryFn: () => get<any[]>('/vuln-schedules') })
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => get<HostGroup[]>('/groups') })
  return (
    <Card bodyClass="p-0" title="Scheduled scans" subtitle="Hosts are also rescanned automatically whenever their package inventory changes (configurable in settings)."
      actions={can('jobs') && <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setAdding(true)}>Add schedule</Button>}>
      {!s.data?.length ? <Empty title="No schedules" icon={<CalendarClock className="size-8" />} /> : (
        <Table>
          <thead><tr><Th>Scope</Th><Th>When</Th><Th>Last run</Th><Th /></tr></thead>
          <tbody>{s.data.map((x) => (
            <tr key={x.id}>
              <Td>{x.scope === 'group' ? `Group: ${groups.data?.find((g) => g.id === x.scope_id)?.name || x.scope_id}` : x.scope === 'host' ? `Host #${x.scope_id}` : x.scope === 'tenant' ? 'Tenant' : 'All hosts'}</Td>
              <Td>{x.frequency === 'weekly' ? `${weekdays[x.weekday]}s` : 'Daily'} at {x.time_of_day}</Td>
              <Td className="text-xs text-muted">{ago(x.last_run_at)}</Td>
              <Td className="text-right">{can('jobs') && <Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => { await del(`/vuln-schedules/${x.id}`); qc.invalidateQueries({ queryKey: ['vulns', 'schedules'] }); toast('Deleted') }} />}</Td>
            </tr>
          ))}</tbody>
        </Table>
      )}
      {adding && <ScheduleEditor provider={me?.user.tenant_id === 0} groups={groups.data || []} onClose={() => { setAdding(false); qc.invalidateQueries({ queryKey: ['vulns', 'schedules'] }) }} />}
    </Card>
  )
}

function ScheduleEditor({ provider, groups, onClose }: { provider: boolean; groups: HostGroup[]; onClose: () => void }) {
  const [x, setX] = useState({ scope: provider ? 'all' : 'tenant', scope_id: 0, frequency: 'daily', weekday: 0, time_of_day: '03:00' })
  const [err, setErr] = useState<unknown>(null)
  return (
    <Modal open onClose={onClose} title="Add scan schedule" size="sm" footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={async () => { try { await post('/vuln-schedules', x); onClose() } catch (e) { setErr(e) } }}>Save</Button></>}>
      <div className="space-y-3">
        <Field label="Scope"><Select value={x.scope} onChange={(e) => setX({ ...x, scope: e.target.value })}>
          {provider && <option value="all">All hosts</option>}<option value="tenant">Tenant</option><option value="group">Group</option>
        </Select></Field>
        {x.scope === 'group' && <Field label="Group"><Select value={x.scope_id} onChange={(e) => setX({ ...x, scope_id: Number(e.target.value) })}><option value={0}>Choose…</option>{groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</Select></Field>}
        <Field label="Frequency"><Segmented value={x.frequency} onChange={(v) => setX({ ...x, frequency: v })} options={[{ id: 'daily', label: 'Daily' }, { id: 'weekly', label: 'Weekly' }]} /></Field>
        {x.frequency === 'weekly' && <Field label="Day"><Select value={x.weekday} onChange={(e) => setX({ ...x, weekday: Number(e.target.value) })}>{weekdays.map((d, i) => <option key={d} value={i}>{d}</option>)}</Select></Field>}
        <Field label="Time (server timezone)"><Input type="time" value={x.time_of_day} onChange={(e) => setX({ ...x, time_of_day: e.target.value })} /></Field>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
