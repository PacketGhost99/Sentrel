import { Link, useNavigate } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell, BellOff, Plus, Server, ShieldAlert, Wrench } from 'lucide-react'
import { get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago } from '@/lib/format'
import type { Alert, AlertEvent, Check, Host } from '@/lib/types'
import { BeatBar, checkBeatsToBeats, hostBeatsToBeats, useCheckBeats, useHostBeats } from '@/components/BeatBar'
import { useTenantFilter } from '@/components/Layout'
import { AllClear, Badge, Button, Card, Empty, Loading, MutedBadge, PageHeader, SeverityBadge, Stat, StatusDot, StatusPill, cx, useToast } from '@/components/ui'
import { hostStatusLabel, MiniMeter } from './Hosts'

interface Dash {
  hosts: { total: number; up: number; down: number; pending: number; maintenance: number }
  alerts: { critical: number; warning: number; acked: number }
  checks: { total: number; up: number; down: number; warning: number }
  upgrades_pending: number
  security_updates: number
  reboot_required: number
  vulnerabilities: Record<string, number>
  recent_events: AlertEvent[]
  firing_alerts: Alert[]
}

type Tone = 'ok' | 'crit' | 'warn' | 'maint'

const tileStyle: Record<Tone, { ring: string; text: string; dot: string; label: string }> = {
  ok: { ring: 'from-ok/12', text: 'text-ok-ink', dot: 'bg-ok-fill', label: 'Up' },
  crit: { ring: 'from-crit/12', text: 'text-crit-ink', dot: 'bg-crit-fill', label: 'Down' },
  warn: { ring: 'from-warn/14', text: 'text-warn-ink', dot: 'bg-warn-fill', label: 'Warning' },
  maint: { ring: 'from-maint/12', text: 'text-maint-ink', dot: 'bg-maint', label: 'Maintenance' },
}

function StatusTile({ tone, value, sub, onClick }: { tone: Tone; value: number; sub: string; onClick?: () => void }) {
  const t = tileStyle[tone]
  return (
    <button onClick={onClick}
      className={cx('group relative overflow-hidden rounded-2xl border border-line/70 bg-panel bg-linear-to-br to-transparent p-5 text-left shadow-card transition-[transform,box-shadow] duration-150 hover:-translate-y-px hover:shadow-pop', t.ring)}>
      <div className="flex items-center gap-2 text-sm font-medium text-muted">
        <span className={cx('size-2 rounded-full', t.dot)} />{t.label}
      </div>
      <div className={cx('mt-2 text-5xl font-semibold tabular-nums tracking-tight', value === 0 && tone !== 'ok' ? 'text-muted/60' : t.text)}>{value}</div>
      <div className="mt-1 text-xs text-muted">{sub}</div>
    </button>
  )
}

export function Dashboard() {
  const { tenant } = useTenantFilter()
  const { can } = useAuth()
  const nav = useNavigate()
  const qc = useQueryClient()
  const toast = useToast()
  const d = useQuery({ queryKey: ['dashboard', tenant], queryFn: () => get<Dash>('/dashboard', { tenant_id: tenant ?? undefined }), refetchInterval: 30_000 })
  const hosts = useQuery({ queryKey: ['hosts', 'grid', tenant], queryFn: () => get<Host[]>('/hosts', { tenant_id: tenant ?? undefined }), refetchInterval: 30_000 })
  const checks = useQuery({ queryKey: ['checks', 'all', tenant], queryFn: () => get<Check[]>('/checks', { host_id: undefined, tenant_id: tenant ?? undefined }), refetchInterval: 30_000 })
  const hostBeats = useHostBeats(30, tenant)
  const checkBeats = useCheckBeats(40, tenant)
  if (d.isLoading) return <Loading />
  const x = d.data
  if (!x) return null
  const v = x.vulnerabilities || {}

  const allHosts = hosts.data || []
  const problemHosts = allHosts.filter((h) => !h.revoked && !h.in_maintenance && (h.status === 'down' || !!h.alert_severity))
    .sort((a, b) => (b.status === 'down' ? 2 : b.alert_severity === 'critical' ? 1 : 0) - (a.status === 'down' ? 2 : a.alert_severity === 'critical' ? 1 : 0))
  const warnHosts = allHosts.filter((h) => !h.in_maintenance && h.status !== 'down' && !!h.alert_severity).length
  const problemChecks = (checks.data || []).filter((c) => c.enabled && (c.status === 'down' || c.status === 'warning'))
    .sort((a, b) => (b.status === 'down' ? 1 : 0) - (a.status === 'down' ? 1 : 0))
  const warnTotal = (hosts.data ? warnHosts : 0) + x.checks.warning
  const upHosts = hosts.data ? allHosts.filter((h) => h.status === 'up' && !h.alert_severity && !h.in_maintenance).length : x.hosts.up

  const ack = async (a: Alert) => {
    try {
      await post(`/alerts/${a.id}/ack`, { comment: '' })
      toast('Alert acknowledged')
      qc.invalidateQueries({ queryKey: ['dashboard'] })
    } catch (e) {
      toast(String(e), 'crit')
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader title="Dashboard" subtitle="Overall health at a glance"
        actions={can('hosts') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => nav('/hosts/new')}>Add host</Button>} />

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatusTile tone="ok" value={upHosts + x.checks.up} sub={`${upHosts} hosts · ${x.checks.up} checks`} onClick={() => nav('/hosts?status=up')} />
        <StatusTile tone="crit" value={x.hosts.down + x.checks.down} sub={`${x.hosts.down} hosts · ${x.checks.down} checks`} onClick={() => nav(x.hosts.down ? '/hosts?status=down' : '/checks')} />
        <StatusTile tone="warn" value={warnTotal} sub={`${hosts.data ? warnHosts : 0} hosts · ${x.checks.warning} checks`} onClick={() => nav('/alerts')} />
        <StatusTile tone="maint" value={x.hosts.maintenance} sub={x.hosts.maintenance === 1 ? '1 host in a maintenance window' : `${x.hosts.maintenance} hosts in a maintenance window`} onClick={() => nav('/maintenance')} />
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Stat label="Active alerts" icon={<Bell />} value={x.alerts.critical + x.alerts.warning} tone={x.alerts.critical ? 'crit' : x.alerts.warning ? 'warn' : 'muted'}
          sub={`${x.alerts.critical} critical · ${x.alerts.warning} warning${x.alerts.acked ? ` · ${x.alerts.acked} acknowledged` : ''}`} onClick={() => nav('/alerts')} />
        <Stat label="Updates pending" icon={<Wrench />} value={x.upgrades_pending} tone={x.security_updates ? 'warn' : 'muted'} sub={`${x.security_updates} security${x.reboot_required ? ` · ${x.reboot_required} need a reboot` : ''}`} onClick={() => nav('/hosts')} />
        <Stat label="Critical CVEs" icon={<ShieldAlert />} value={v.critical ?? 0} tone={v.critical ? 'crit' : v.high ? 'warn' : 'muted'} sub={`${v.high ?? 0} high`} onClick={() => nav('/vulnerabilities')} />
        <Stat label="Monitored" icon={<Server />} value={x.hosts.total} tone="muted" sub={`${x.checks.total} checks${x.hosts.pending ? ` · ${x.hosts.pending} pending` : ''}`} onClick={() => nav('/hosts')} />
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card title="Hosts needing attention" subtitle={problemHosts.length ? `${problemHosts.length} with a problem` : undefined} bodyClass="p-0"
          actions={<Link to="/hosts" className="text-xs font-medium text-accent hover:underline">All hosts</Link>}>
          {hosts.isLoading ? <Loading /> : problemHosts.length === 0 ? (
            <AllClear title="All hosts healthy">No host is down or has an active alert.</AllClear>
          ) : (
            <ul className="divide-y divide-line/60">
              {problemHosts.slice(0, 8).map((h) => {
                const st = hostStatusLabel(h)
                return (
                  <li key={h.id}>
                    <Link to={`/hosts/${h.id}`} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3 hover:bg-panel-2/50">
                      <StatusPill tone={st.tone} label={st.label} />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-semibold">{h.name}</div>
                        <div className="truncate text-xs text-muted">{h.status === 'down' ? `Last seen ${ago(h.last_seen_at)}` : `${h.alert_count} active alert${h.alert_count === 1 ? '' : 's'}`}</div>
                      </div>
                      <div className="hidden w-40 sm:block"><BeatBar beats={hostBeatsToBeats(hostBeats.data?.[String(h.id)])} count={14} height={20} /></div>
                    </Link>
                  </li>
                )
              })}
            </ul>
          )}
        </Card>

        <Card title="Checks needing attention" subtitle={problemChecks.length ? `${problemChecks.length} failing` : undefined} bodyClass="p-0"
          actions={<Link to="/checks" className="text-xs font-medium text-accent hover:underline">All checks</Link>}>
          {checks.isLoading ? <Loading /> : problemChecks.length === 0 ? (
            <AllClear title="All checks passing">Nothing is failing right now.</AllClear>
          ) : (
            <ul className="divide-y divide-line/60">
              {problemChecks.slice(0, 8).map((c) => (
                <li key={c.id}>
                  <Link to={`/checks/${c.id}`} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3 hover:bg-panel-2/50">
                    <StatusPill status={c.status} label={c.status} />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-semibold">{c.name}</div>
                      <div className="truncate text-xs text-muted" title={c.last_message}>{c.last_message || c.target}</div>
                    </div>
                    <div className="hidden w-40 sm:block"><BeatBar beats={checkBeatsToBeats(checkBeats.data?.[String(c.id)])} count={20} height={20} /></div>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

      <div className="grid gap-4 xl:grid-cols-3">
        <Card className="xl:col-span-2" title="Firing alerts" actions={<Link to="/alerts" className="text-xs font-medium text-accent hover:underline">View all</Link>} bodyClass="p-0">
          {x.firing_alerts.length === 0 ? (
            <AllClear title="All quiet">No alerts are firing right now.</AllClear>
          ) : (
            <ul className="divide-y divide-line/60">
              {x.firing_alerts.slice(0, 12).map((a) => (
                <li key={a.id} className="flex items-center gap-3 px-5 py-3">
                  <StatusDot status={a.severity === 'critical' ? 'down' : 'warning'} pulse={!a.acked_by} />
                  <div className="min-w-0 flex-1">
                    <Link to={`/alerts/${a.id}`} className="block truncate text-sm font-medium hover:text-accent">{a.title}</Link>
                    <div className="truncate text-xs text-muted">{a.message}</div>
                  </div>
                  <div className="hidden shrink-0 items-center gap-2 sm:flex">
                    {a.muted && <MutedBadge />}
                    {a.in_maintenance && <Badge tone="maint">maintenance</Badge>}
                    {a.flapping && <Badge tone="warn">flapping</Badge>}
                    {a.acked_by ? <Badge title={a.ack_comment}>acked by {a.acked_by}</Badge> : <SeverityBadge severity={a.severity} />}
                    <span className="w-16 text-right text-xs text-muted">{ago(a.fired_at)}</span>
                  </div>
                  {!a.acked_by && can('ack') && <Button size="sm" variant="ghost" icon={<BellOff className="size-3.5" />} onClick={() => ack(a)}>Ack</Button>}
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card title="Recent events" bodyClass="p-0">
          {x.recent_events.length === 0 ? <Empty title="No events yet" /> : (
            <ul className="divide-y divide-line/60">
              {x.recent_events.map((e) => (
                <li key={e.id} className="px-5 py-3">
                  <div className="flex items-center gap-2 text-xs">
                    <Badge tone={e.event === 'resolved' ? 'ok' : e.event === 'acked' ? 'info' : e.event === 'silenced' ? 'maint' : e.severity === 'critical' ? 'crit' : 'warn'}>{e.event}</Badge>
                    <span className="text-muted">{ago(e.created_at)}</span>
                    {e.actor && <span className="text-muted">by {e.actor}</span>}
                  </div>
                  <Link to={`/alerts/${e.alert_id}`} className="mt-1 block truncate text-sm hover:text-accent">{e.title}</Link>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

      <Card title="Host overview" subtitle="Status, CPU, memory and disk for every host" bodyClass="p-4">
        {!allHosts.length ? (
          <Empty title="No hosts yet">{can('hosts') && <Link to="/hosts/new" className="text-accent hover:underline">Add your first host</Link>}</Empty>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(250px,1fr))] gap-3">
            {allHosts.map((h) => <HostTile key={h.id} h={h} />)}
          </div>
        )}
      </Card>
    </div>
  )
}

function HostTile({ h }: { h: Host }) {
  const s = h.summary
  const st = hostStatusLabel(h)
  const edge = h.in_maintenance ? 'bg-maint' : h.status === 'down' || h.alert_severity === 'critical' ? 'bg-crit-fill' : h.alert_severity === 'warning' ? 'bg-warn-fill' : h.status === 'up' ? 'bg-ok-fill' : 'bg-unknown-fill'
  return (
    <Link to={`/hosts/${h.id}`} className="group relative block overflow-hidden rounded-xl border border-line/70 bg-panel p-3.5 pl-4 transition-[transform,box-shadow] duration-150 hover:-translate-y-px hover:shadow-pop">
      <span className={cx('absolute inset-y-0 left-0 w-1', edge)} />
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-sm font-semibold">{h.name}</span>
        <StatusDot status={h.in_maintenance ? 'maintenance' : st.tone === 'warn' ? 'warning' : st.tone === 'crit' ? 'down' : h.status} pulse />
      </div>
      <div className="mt-0.5 truncate text-[11px] text-muted">{h.kind === 'agent' ? h.host_info?.platform || 'agent' : h.kind}{h.alert_count > 0 ? ` · ${h.alert_count} alert${h.alert_count === 1 ? '' : 's'}` : ''}</div>
      <div className="mt-3 flex items-center gap-2">
        <MiniMeter label="CPU" value={s?.cpu} />
        <MiniMeter label="Mem" value={s?.memory} />
        <MiniMeter label="Disk" value={s?.disk} />
      </div>
    </Link>
  )
}
