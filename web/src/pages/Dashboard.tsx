import { Link, useNavigate } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { BellOff, CheckCircle2, Plus } from 'lucide-react'
import { get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago } from '@/lib/format'
import type { Alert, AlertEvent, Host } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Empty, Loading, PageHeader, SeverityBadge, Stat, StatusDot, cx, useToast } from '@/components/ui'

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

export function Dashboard() {
  const { tenant } = useTenantFilter()
  const { can } = useAuth()
  const nav = useNavigate()
  const qc = useQueryClient()
  const toast = useToast()
  const d = useQuery({ queryKey: ['dashboard', tenant], queryFn: () => get<Dash>('/dashboard', { tenant_id: tenant ?? undefined }), refetchInterval: 30_000 })
  const hosts = useQuery({ queryKey: ['hosts', 'grid', tenant], queryFn: () => get<Host[]>('/hosts', { tenant_id: tenant ?? undefined }), refetchInterval: 30_000 })
  if (d.isLoading) return <Loading />
  const x = d.data
  if (!x) return null
  const v = x.vulnerabilities || {}

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
    <div>
      <PageHeader title="Dashboard" subtitle="Overall health at a glance"
        actions={can('hosts') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => nav('/hosts/new')}>Add host</Button>} />
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
        <Stat label="Hosts up" value={`${x.hosts.up}/${x.hosts.total}`} tone={x.hosts.down ? 'warn' : 'ok'} onClick={() => nav('/hosts')} />
        <Stat label="Hosts down" value={x.hosts.down} tone={x.hosts.down ? 'crit' : 'muted'} onClick={() => nav('/hosts?status=down')} sub={x.hosts.maintenance ? `${x.hosts.maintenance} in maintenance` : undefined} />
        <Stat label="Critical alerts" value={x.alerts.critical} tone={x.alerts.critical ? 'crit' : 'muted'} onClick={() => nav('/alerts')} />
        <Stat label="Warnings" value={x.alerts.warning} tone={x.alerts.warning ? 'warn' : 'muted'} onClick={() => nav('/alerts')} sub={x.alerts.acked ? `${x.alerts.acked} acknowledged` : undefined} />
        <Stat label="Checks down" value={`${x.checks.down}/${x.checks.total}`} tone={x.checks.down ? 'crit' : 'ok'} onClick={() => nav('/checks')} />
        <Stat label="Updates pending" value={x.upgrades_pending} sub={`${x.security_updates} security`} tone={x.security_updates ? 'warn' : 'muted'} onClick={() => nav('/hosts')} />
        <Stat label="Reboot required" value={x.reboot_required} tone={x.reboot_required ? 'warn' : 'muted'} />
        <Stat label="Critical CVEs" value={v.critical ?? 0} sub={`${v.high ?? 0} high`} tone={v.critical ? 'crit' : v.high ? 'warn' : 'muted'} onClick={() => nav('/vulnerabilities')} />
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <Card className="xl:col-span-2" title="Firing alerts" actions={<Link to="/alerts" className="text-xs text-accent hover:underline">View all</Link>} bodyClass="p-0">
          {x.firing_alerts.length === 0 ? (
            <Empty title="All quiet" icon={<CheckCircle2 className="size-8 text-ok" />}>No alerts are firing right now.</Empty>
          ) : (
            <ul className="divide-y divide-line">
              {x.firing_alerts.slice(0, 12).map((a) => (
                <li key={a.id} className="flex items-center gap-3 px-4 py-2.5">
                  <StatusDot status={a.severity === 'critical' ? 'down' : 'warning'} pulse={!a.acked_by} />
                  <div className="min-w-0 flex-1">
                    <Link to={`/alerts/${a.id}`} className="block truncate text-sm font-medium hover:underline">{a.title}</Link>
                    <div className="truncate text-xs text-muted">{a.message}</div>
                  </div>
                  <div className="hidden shrink-0 items-center gap-2 sm:flex">
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
            <ul className="divide-y divide-line">
              {x.recent_events.map((e) => (
                <li key={e.id} className="px-4 py-2">
                  <div className="flex items-center gap-2 text-xs">
                    <Badge tone={e.event === 'resolved' ? 'ok' : e.event === 'acked' ? 'info' : e.severity === 'critical' ? 'crit' : 'warn'}>{e.event}</Badge>
                    <span className="text-muted">{ago(e.created_at)}</span>
                    {e.actor && <span className="text-muted">by {e.actor}</span>}
                  </div>
                  <Link to={`/alerts/${e.alert_id}`} className="mt-0.5 block truncate text-sm hover:underline">{e.title}</Link>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

      <Card className="mt-4" title="Host health" subtitle="Each tile is a host; colour shows status, the bars CPU / memory / worst disk">
        {!hosts.data?.length ? (
          <Empty title="No hosts yet">{can('hosts') && <Link to="/hosts/new" className="text-accent hover:underline">Add your first host</Link>}</Empty>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-2">
            {hosts.data.map((h) => <HostTile key={h.id} h={h} />)}
          </div>
        )}
      </Card>
    </div>
  )
}

function bar(v?: number | null) {
  if (v === null || v === undefined) return null
  const c = v >= 90 ? 'bg-crit' : v >= 80 ? 'bg-warn' : 'bg-ok'
  return <div className="h-1 overflow-hidden rounded-full bg-panel-2"><div className={cx('h-full', c)} style={{ width: `${Math.min(100, v)}%` }} /></div>
}

function HostTile({ h }: { h: Host }) {
  const s = h.summary
  const tone = h.in_maintenance ? 'border-maint/60' : h.status === 'down' ? 'border-crit/70 bg-crit/5' : h.alert_severity === 'critical' ? 'border-crit/50' : h.alert_severity === 'warning' ? 'border-warn/50' : 'border-line'
  return (
    <Link to={`/hosts/${h.id}`} className={cx('block rounded-md border p-2.5 hover:border-accent', tone)}>
      <div className="flex items-center gap-1.5">
        <StatusDot status={h.in_maintenance ? 'unknown' : h.status} pulse />
        <span className="truncate text-sm font-medium">{h.name}</span>
      </div>
      <div className="mt-1.5 space-y-1">
        {bar(s?.cpu)}
        {bar(s?.memory)}
        {bar(s?.disk)}
      </div>
      <div className="mt-1.5 flex items-center gap-1 text-[11px] text-muted">
        <span className="truncate">{h.kind === 'agent' ? h.host_info?.platform || 'agent' : h.kind}</span>
        {h.alert_count > 0 && <span className="ml-auto text-crit">{h.alert_count}⚠</span>}
      </div>
    </Link>
  )
}
