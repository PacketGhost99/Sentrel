import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Plus, RefreshCcw, ScanSearch, Server } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, osLabel, plural } from '@/lib/format'
import type { Host, HostBeat, HostGroup } from '@/lib/types'
import { critHighLabel, critHighTitle } from '@/lib/vulns'
import { BeatBar, averageUptime, hostBeatsToBeats, useHostBeats } from '@/components/BeatBar'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Checkbox, Empty, Input, Loading, PageHeader, Select, StatusPill, UptimePill, cx, useConfirm, useToast } from '@/components/ui'

export function hostStatusLabel(h: Host): { label: string; tone: 'ok' | 'warn' | 'crit' | 'muted' | 'maint' } {
  if (h.revoked) return { label: 'revoked', tone: 'muted' }
  if (h.in_maintenance) return { label: 'maintenance', tone: 'maint' }
  if (h.status === 'down') return { label: 'down', tone: 'crit' }
  if (h.status === 'pending') return { label: 'pending', tone: 'muted' }
  if (h.alert_severity === 'critical') return { label: 'critical', tone: 'crit' }
  if (h.alert_severity === 'warning') return { label: 'warning', tone: 'warn' }
  return { label: 'healthy', tone: 'ok' }
}

const sortOptions: { id: string; label: string }[] = [
  { id: 'status', label: 'Sort: problems first' },
  { id: 'name', label: 'Sort: name' },
  { id: 'cpu', label: 'Sort: CPU' },
  { id: 'memory', label: 'Sort: memory' },
  { id: 'disk', label: 'Sort: disk' },
  { id: 'updates', label: 'Sort: updates' },
  { id: 'vulns', label: 'Sort: CVEs' },
  { id: 'seen', label: 'Sort: last seen' },
]

const sorters: Record<string, (h: Host) => number | string> = {
  name: (h) => h.name.toLowerCase(),
  status: (h) => -((h.status === 'down' ? 3 : h.alert_severity === 'critical' ? 2 : h.alert_severity === 'warning' ? 1 : 0) * 1000 - h.id / 1e6),
  cpu: (h) => -(h.summary?.cpu ?? -1),
  memory: (h) => -(h.summary?.memory ?? -1),
  disk: (h) => -(h.summary?.disk ?? -1),
  updates: (h) => -(h.security_count * 1000 + h.upgrade_count),
  vulns: (h) => -(h.vuln_critical * 1000 + h.vuln_high),
  seen: (h) => -h.last_seen_at,
}

/** Compact labelled meter used on host rows. */
export function MiniMeter({ label, value }: { label: string; value?: number | null }) {
  const has = value !== null && value !== undefined
  const color = !has ? '' : value! >= 90 ? 'bg-crit-fill' : value! >= 80 ? 'bg-warn-fill' : 'bg-ok-fill'
  return (
    <div className="w-[68px]" title={has ? `${label} ${value!.toFixed(1)}%` : `${label}: no data`}>
      <div className="flex items-baseline justify-between text-[11px] leading-4">
        <span className="text-muted">{label}</span>
        <span className="font-medium tabular-nums">{has ? `${Math.round(value!)}%` : '–'}</span>
      </div>
      <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-panel-2">
        {has && <div className={cx('h-full rounded-full transition-[width] duration-500', color)} style={{ width: `${Math.min(100, Math.max(0, value!))}%` }} />}
      </div>
    </div>
  )
}

export function Hosts() {
  const { tenant } = useTenantFilter()
  const { can } = useAuth()
  const nav = useNavigate()
  const toast = useToast()
  const confirm = useConfirm()
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState(params.get('q') || '')
  const status = params.get('status') || ''
  const kind = params.get('kind') || ''
  const group = params.get('group_id') || ''
  const tag = params.get('tag') || ''
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [sortKey, setSortKey] = useState('status')
  const hosts = useQuery({ queryKey: ['hosts', tenant, status, kind, group, tag], queryFn: () => get<Host[]>('/hosts', { tenant_id: tenant ?? undefined, status, kind, group_id: group, tag }), refetchInterval: 30_000 })
  const groups = useQuery({ queryKey: ['groups', tenant], queryFn: () => get<HostGroup[]>('/groups', { tenant_id: tenant ?? undefined }) })
  const tags = useQuery({ queryKey: ['tags', tenant], queryFn: () => get<{ tag: string; count: number }[]>('/tags', { tenant_id: tenant ?? undefined }) })
  const beats = useHostBeats(30, tenant)

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    return (hosts.data || []).filter((h) => !s || h.name.toLowerCase().includes(s) || h.address.toLowerCase().includes(s) ||
      (h.tags || []).some((t) => t.toLowerCase().includes(s)) || (h.host_info?.platform || '').toLowerCase().includes(s))
  }, [hosts.data, q])
  const sorted = useMemo(() => {
    const g = sorters[sortKey] || sorters.status
    return [...filtered].sort((a, b) => {
      const va = g(a)
      const vb = g(b)
      return typeof va === 'number' && typeof vb === 'number' ? va - vb : String(va).localeCompare(String(vb), undefined, { numeric: true })
    })
  }, [filtered, sortKey])

  const setParam = (k: string, v: string) => {
    const p = new URLSearchParams(params)
    if (v) p.set(k, v)
    else p.delete(k)
    setParams(p, { replace: true })
  }
  const toggle = (id: number) => {
    const s = new Set(selected)
    if (s.has(id)) s.delete(id)
    else s.add(id)
    setSelected(s)
  }
  const agentIds = [...selected].filter((id) => hosts.data?.find((h) => h.id === id)?.kind === 'agent')

  const bulk = async (type: 'scan' | 'dry' | 'upgrade') => {
    if (!agentIds.length) return
    if (type === 'scan') {
      let failure = ''
      for (const id of agentIds) await post(`/hosts/${id}/scan`).catch((e) => { failure = failure || errorMessage(e) })
      if (failure) toast(failure, 'crit')
      else toast(`Queued vulnerability scans for ${agentIds.length} hosts`)
      return
    }
    if (type === 'upgrade') {
      const ok = await confirm({ title: 'Upgrade packages', danger: true, confirmLabel: 'Upgrade now', typeToConfirm: 'upgrade',
        body: <p>Install all pending package updates on <b>{agentIds.length}</b> host(s) now? Hosts without “allow upgrades” enabled will be skipped. Consider a dry run first.</p> })
      if (!ok) return
    }
    const r = await post<{ host_id: number; job_id?: string; status?: string; error?: string }[]>('/jobs/bulk', { host_ids: agentIds, type: 'upgrade', dry_run: type === 'dry', confirm: type === 'upgrade', schedule: 'now' })
    const okN = r.filter((x) => x.job_id).length
    const awaiting = r.filter((x) => x.status === 'awaiting_signature').length
    toast(`${okN} job(s) created${awaiting ? ` (${awaiting} awaiting operator signature)` : ''}${r.length - okN ? `, ${r.length - okN} skipped` : ''}`, okN ? 'ok' : 'crit')
    nav('/jobs')
  }

  return (
    <div>
      <PageHeader title="Hosts" subtitle={hosts.data ? `${hosts.data.length} monitored` : undefined}
        actions={can('hosts') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => nav('/hosts/new')}>Add host</Button>} />
      <Card bodyClass="p-0">
        <div className="flex flex-wrap items-center gap-2 border-b border-line/70 p-4">
          <Input placeholder="Filter by name, address, tag, OS…" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" />
          <div className="w-[calc(50%-4px)] sm:w-36"><Select value={status} onChange={(e) => setParam('status', e.target.value)}>
            <option value="">Any status</option><option value="up">Up</option><option value="down">Down</option><option value="pending">Pending</option>
          </Select></div>
          <div className="w-[calc(50%-4px)] sm:w-36"><Select value={kind} onChange={(e) => setParam('kind', e.target.value)}>
            <option value="">Any type</option><option value="agent">Agent</option><option value="snmp">SNMP device</option><option value="target">Target</option>
          </Select></div>
          <div className="w-[calc(50%-4px)] sm:w-40"><Select value={group} onChange={(e) => setParam('group_id', e.target.value)}>
            <option value="">Any group</option>{groups.data?.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </Select></div>
          {!!tags.data?.length && <div className="w-[calc(50%-4px)] sm:w-36"><Select value={tag} onChange={(e) => setParam('tag', e.target.value)}>
            <option value="">Any tag</option>{tags.data.map((t) => <option key={t.tag} value={t.tag}>{t.tag} ({t.count})</option>)}
          </Select></div>}
          <div className="w-[calc(50%-4px)] sm:w-44"><Select value={sortKey} onChange={(e) => setSortKey(e.target.value)}>
            {sortOptions.map((o) => <option key={o.id} value={o.id}>{o.label}</option>)}
          </Select></div>
          <Button variant="ghost" size="sm" aria-label="Refresh" icon={<RefreshCcw className="size-3.5" />} onClick={() => { hosts.refetch(); beats.refetch() }} />
          {selected.size > 0 && can('jobs') && (
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <span className="text-xs text-muted">{selected.size} selected</span>
              <Button size="sm" icon={<ScanSearch className="size-3.5" />} onClick={() => bulk('scan')}>Scan</Button>
              <Button size="sm" onClick={() => bulk('dry')}>Upgrade dry run</Button>
              <Button size="sm" variant="danger" onClick={() => bulk('upgrade')}>Upgrade</Button>
            </div>
          )}
        </div>
        {hosts.isLoading ? <Loading /> : sorted.length === 0 ? (
          <Empty title="No hosts" icon={<Server className="size-8" />}>{can('hosts') ? <>Install an agent, add an SNMP device or a monitored target. <Link to="/hosts/new" className="text-accent hover:underline">Add host</Link></> : 'Nothing to show.'}</Empty>
        ) : (
          <>
            <div className="flex items-center gap-3 border-b border-line/70 bg-panel-2/40 px-5 py-2 text-xs text-muted">
              <Checkbox checked={selected.size > 0 && selected.size === sorted.length} onChange={(v) => setSelected(v ? new Set(sorted.map((h) => h.id)) : new Set())} label="Select all" />
              <span className="ml-auto hidden lg:inline">30-day uptime</span>
            </div>
            <ul className="divide-y divide-line/60">
              {sorted.map((h) => (
                <HostRow key={h.id} h={h} selected={selected.has(h.id)} onToggle={() => toggle(h.id)} days={beats.data?.[String(h.id)]} />
              ))}
            </ul>
          </>
        )}
      </Card>
    </div>
  )
}

function HostRow({ h, selected, onToggle, days }: { h: Host; selected: boolean; onToggle: () => void; days?: HostBeat[] }) {
  const st = hostStatusLabel(h)
  const avg = averageUptime(days)
  const system = h.kind === 'agent' ? osLabel(h.host_info) : h.kind === 'snmp' ? (h.snmp_info?.profile ? `SNMP · ${h.snmp_info.profile}` : 'SNMP device') : 'Target'
  return (
    <li className={cx('flex flex-wrap lg:flex-nowrap items-center gap-x-5 gap-y-3 px-5 py-4 transition-colors hover:bg-panel-2/50', selected && 'bg-accent/5')}>
      <Checkbox checked={selected} onChange={onToggle} />
      <div className="flex min-w-0 flex-1 basis-60 items-center gap-3 lg:min-w-56">
        <StatusPill tone={st.tone} label={st.label} className="shrink-0" />
        <div className="min-w-0 flex-1">
          <Link to={`/hosts/${h.id}`} className="block truncate text-[15px] font-semibold tracking-tight hover:text-accent">{h.name}</Link>
          <div className="truncate text-xs text-muted" title={`${h.address} · ${system}`}>
            {h.address}<span className="hidden sm:inline"> · {system}</span>
          </div>
          {(h.group_name || !!h.tags?.length || (h.tenant_name && h.tenant_id !== 0)) && (
            <div className="mt-1 flex flex-wrap items-center gap-1">
              {h.group_name && <Badge>{h.group_name}</Badge>}
              {(h.tags || []).slice(0, 3).map((t) => <Badge key={t} tone="info">{t}</Badge>)}
              {h.tenant_name && h.tenant_id !== 0 && <Badge tone="accent">{h.tenant_name}</Badge>}
            </div>
          )}
        </div>
      </div>
      <div className="hidden shrink-0 items-center gap-3 md:flex">
        <MiniMeter label="CPU" value={h.summary?.cpu} />
        <MiniMeter label="Mem" value={h.summary?.memory} />
        <MiniMeter label="Disk" value={h.summary?.disk} />
      </div>
      <div className="hidden w-40 shrink-0 flex-col items-start gap-1 xl:flex">
        {h.kind === 'agent' && h.security_count > 0 && <Badge tone="crit">{h.security_count} security</Badge>}
        {h.kind === 'agent' && h.upgrade_count > 0 && <Badge tone="warn">{plural(h.upgrade_count, 'update')}</Badge>}
        {h.kind === 'agent' && h.reboot_required && <Badge tone="warn">reboot</Badge>}
        {h.vuln_critical + h.vuln_high > 0 && (
          <Badge tone={h.vuln_critical > 0 ? 'crit' : 'warn'} title={critHighTitle({ critical: h.vuln_critical, high: h.vuln_high })}>
            {critHighLabel({ critical: h.vuln_critical, high: h.vuln_high })}
          </Badge>
        )}
      </div>
      <div className="flex w-full items-center gap-3 lg:w-56 lg:shrink-0 2xl:w-72">
        <BeatBar beats={hostBeatsToBeats(days)} count={30} className="flex-1" />
        <UptimePill value={avg} title="Average uptime over 30 days" />
      </div>
      <div className="hidden w-20 text-right text-xs text-muted 2xl:block">{h.connected ? <span className="text-ok-ink">connected</span> : ago(h.last_seen_at)}</div>
    </li>
  )
}
