import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Plus, RefreshCcw, ScanSearch, Server } from 'lucide-react'
import { get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, osLabel } from '@/lib/format'
import type { Host, HostGroup } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Checkbox, Empty, Input, Loading, Meter, PageHeader, Select, StatusDot, Table, Td, Th, useConfirm, useSort, useToast } from '@/components/ui'

export function hostStatusLabel(h: Host): { label: string; tone: 'ok' | 'warn' | 'crit' | 'muted' | 'maint' } {
  if (h.revoked) return { label: 'revoked', tone: 'muted' }
  if (h.in_maintenance) return { label: 'maintenance', tone: 'maint' }
  if (h.status === 'down') return { label: 'down', tone: 'crit' }
  if (h.status === 'pending') return { label: 'pending', tone: 'muted' }
  if (h.alert_severity === 'critical') return { label: 'critical', tone: 'crit' }
  if (h.alert_severity === 'warning') return { label: 'warning', tone: 'warn' }
  return { label: 'healthy', tone: 'ok' }
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
  const hosts = useQuery({ queryKey: ['hosts', tenant, status, kind, group, tag], queryFn: () => get<Host[]>('/hosts', { tenant_id: tenant ?? undefined, status, kind, group_id: group, tag }), refetchInterval: 30_000 })
  const groups = useQuery({ queryKey: ['groups', tenant], queryFn: () => get<HostGroup[]>('/groups', { tenant_id: tenant ?? undefined }) })
  const tags = useQuery({ queryKey: ['tags', tenant], queryFn: () => get<{ tag: string; count: number }[]>('/tags', { tenant_id: tenant ?? undefined }) })

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    return (hosts.data || []).filter((h) => !s || h.name.toLowerCase().includes(s) || h.address.toLowerCase().includes(s) ||
      (h.tags || []).some((t) => t.toLowerCase().includes(s)) || (h.host_info?.platform || '').toLowerCase().includes(s))
  }, [hosts.data, q])
  const { sorted, th } = useSort(filtered, { key: 'status', dir: 'desc' }, {
    name: (h) => h.name.toLowerCase(),
    status: (h) => (h.status === 'down' ? 3 : h.alert_severity === 'critical' ? 2 : h.alert_severity === 'warning' ? 1 : 0) * 1000 - h.id / 1e6,
    cpu: (h) => h.summary?.cpu ?? -1,
    memory: (h) => h.summary?.memory ?? -1,
    disk: (h) => h.summary?.disk ?? -1,
    updates: (h) => h.security_count * 1000 + h.upgrade_count,
    vulns: (h) => h.vuln_critical * 1000 + h.vuln_high,
    seen: (h) => h.last_seen_at,
  })
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
      for (const id of agentIds) await post(`/hosts/${id}/scan`).catch(() => {})
      toast(`Queued vulnerability scans for ${agentIds.length} hosts`)
      return
    }
    if (type === 'upgrade') {
      const ok = await confirm({ title: 'Upgrade packages', danger: true, confirmLabel: 'Upgrade now', typeToConfirm: 'upgrade',
        body: <p>Install all pending package updates on <b>{agentIds.length}</b> host(s) now? Hosts without “allow upgrades” enabled will be skipped. Consider a dry run first.</p> })
      if (!ok) return
    }
    const r = await post<{ host_id: number; job_id?: string; error?: string }[]>('/jobs/bulk', { host_ids: agentIds, type: 'upgrade', dry_run: type === 'dry', confirm: type === 'upgrade', schedule: 'now' })
    const okN = r.filter((x) => x.job_id).length
    toast(`${okN} job(s) created${r.length - okN ? `, ${r.length - okN} skipped` : ''}`, okN ? 'ok' : 'crit')
    nav('/jobs')
  }

  return (
    <div>
      <PageHeader title="Hosts" subtitle={hosts.data ? `${hosts.data.length} monitored` : undefined}
        actions={can('hosts') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => nav('/hosts/new')}>Add host</Button>} />
      <Card bodyClass="p-0">
        <div className="flex flex-wrap items-center gap-2 border-b border-line p-3">
          <Input placeholder="Filter by name, address, tag, OS…" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" />
          <div className="w-36"><Select value={status} onChange={(e) => setParam('status', e.target.value)}>
            <option value="">Any status</option><option value="up">Up</option><option value="down">Down</option><option value="pending">Pending</option>
          </Select></div>
          <div className="w-36"><Select value={kind} onChange={(e) => setParam('kind', e.target.value)}>
            <option value="">Any type</option><option value="agent">Agent</option><option value="snmp">SNMP device</option><option value="target">Target</option>
          </Select></div>
          <div className="w-40"><Select value={group} onChange={(e) => setParam('group_id', e.target.value)}>
            <option value="">Any group</option>{groups.data?.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </Select></div>
          {!!tags.data?.length && <div className="w-36"><Select value={tag} onChange={(e) => setParam('tag', e.target.value)}>
            <option value="">Any tag</option>{tags.data.map((t) => <option key={t.tag} value={t.tag}>{t.tag} ({t.count})</option>)}
          </Select></div>}
          <Button variant="ghost" size="sm" icon={<RefreshCcw className="size-3.5" />} onClick={() => hosts.refetch()} />
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
          <Table>
            <thead>
              <tr>
                <Th className="w-8"><Checkbox checked={selected.size > 0 && selected.size === sorted.length} onChange={(v) => setSelected(v ? new Set(sorted.map((h) => h.id)) : new Set())} /></Th>
                <Th {...th('status')}>Status</Th>
                <Th {...th('name')}>Host</Th>
                <Th className="hidden lg:table-cell">System</Th>
                <Th {...th('cpu')} className="hidden md:table-cell">CPU</Th>
                <Th {...th('memory')} className="hidden md:table-cell">Memory</Th>
                <Th {...th('disk')} className="hidden xl:table-cell">Disk</Th>
                <Th {...th('updates')} className="hidden lg:table-cell">Updates</Th>
                <Th {...th('vulns')} className="hidden xl:table-cell">CVEs</Th>
                <Th {...th('seen')} className="hidden sm:table-cell">Last seen</Th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((h) => {
                const st = hostStatusLabel(h)
                return (
                  <tr key={h.id} className="hover:bg-panel-2/60">
                    <Td><Checkbox checked={selected.has(h.id)} onChange={() => toggle(h.id)} /></Td>
                    <Td><div className="flex items-center gap-2"><StatusDot status={h.status === 'down' ? 'down' : st.tone === 'warn' ? 'warning' : st.tone === 'crit' ? 'down' : h.status} pulse /><Badge tone={st.tone}>{st.label}</Badge></div></Td>
                    <Td>
                      <Link to={`/hosts/${h.id}`} className="font-medium hover:underline">{h.name}</Link>
                      <div className="flex flex-wrap items-center gap-1 text-xs text-muted">
                        <span>{h.address}</span>
                        {h.group_name && <Badge>{h.group_name}</Badge>}
                        {(h.tags || []).slice(0, 4).map((t) => <Badge key={t} tone="info">{t}</Badge>)}
                        {h.tenant_name && h.tenant_id !== 0 && <Badge tone="accent">{h.tenant_name}</Badge>}
                      </div>
                    </Td>
                    <Td className="hidden text-xs lg:table-cell">
                      <div>{h.kind === 'agent' ? osLabel(h.host_info) : h.kind === 'snmp' ? h.snmp_info?.profile ? `SNMP · ${h.snmp_info.profile}` : 'SNMP' : 'Target'}</div>
                      {h.kind === 'agent' && <div className="text-muted">agent {h.agent_version || '–'}</div>}
                    </Td>
                    <Td className="hidden md:table-cell"><Meter value={h.summary?.cpu} /></Td>
                    <Td className="hidden md:table-cell"><Meter value={h.summary?.memory} /></Td>
                    <Td className="hidden xl:table-cell"><Meter value={h.summary?.disk} /></Td>
                    <Td className="hidden lg:table-cell">
                      {h.kind === 'agent' ? (
                        <div className="flex items-center gap-1">
                          {h.security_count > 0 && <Badge tone="crit">{h.security_count} sec</Badge>}
                          {h.upgrade_count > 0 ? <Badge tone="warn">{h.upgrade_count}</Badge> : <span className="text-xs text-muted">0</span>}
                          {h.reboot_required && <Badge tone="warn">reboot</Badge>}
                        </div>
                      ) : <span className="text-muted">–</span>}
                    </Td>
                    <Td className="hidden xl:table-cell">
                      {h.vuln_critical > 0 && <Badge tone="crit">{h.vuln_critical} crit</Badge>} {h.vuln_high > 0 && <Badge tone="warn">{h.vuln_high} high</Badge>}
                      {!h.vuln_critical && !h.vuln_high && <span className="text-xs text-muted">–</span>}
                    </Td>
                    <Td className="hidden text-xs text-muted sm:table-cell">{h.connected ? <span className="text-ok">connected</span> : ago(h.last_seen_at)}</Td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  )
}
