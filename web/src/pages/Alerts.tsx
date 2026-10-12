import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, BellOff, BellRing, CheckCircle2, CircleCheck } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, dateTime, duration } from '@/lib/format'
import type { Alert, AlertEvent } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Checkbox, Empty, ErrorBox, Field, Input, KV, Loading, Modal, PageHeader, Segmented, SeverityBadge, StatusDot, Table, Td, Th, Textarea, useToast } from '@/components/ui'

export function Alerts() {
  return (
    <div>
      <PageHeader title="Alerts" subtitle="Firing and pending alerts, plus history. Acknowledge to stop repeat notifications." />
      <AlertsTable />
    </div>
  )
}

export function AlertsTable({ hostId }: { hostId?: number }) {
  const { tenant } = useTenantFilter()
  const { can } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const [status, setStatus] = useState<'active' | 'resolved' | 'all'>('active')
  const [sev, setSev] = useState('')
  const [q, setQ] = useState('')
  const [sel, setSel] = useState<Set<number>>(new Set())
  const [ackOpen, setAckOpen] = useState(false)
  const alerts = useQuery({ queryKey: ['alerts', status, sev, hostId, tenant], queryFn: () => get<Alert[]>('/alerts', { status, severity: sev, host_id: hostId, tenant_id: tenant ?? undefined, limit: 500 }), refetchInterval: 30_000 })
  const rows = (alerts.data || []).filter((a) => !q || a.title.toLowerCase().includes(q.toLowerCase()) || a.message.toLowerCase().includes(q.toLowerCase()))
  const ackable = rows.filter((a) => sel.has(a.id) && a.status === 'firing' && !a.acked_by)
  return (
    <Card bodyClass="p-0">
      <div className="flex flex-wrap items-center gap-2 border-b border-line p-3">
        <Segmented value={status} onChange={(v) => { setStatus(v); setSel(new Set()) }} options={[{ id: 'active', label: 'Active' }, { id: 'resolved', label: 'Resolved' }, { id: 'all', label: 'All fired' }]} />
        <Segmented value={sev} onChange={setSev} options={[{ id: '', label: 'Any' }, { id: 'critical', label: 'Critical' }, { id: 'warning', label: 'Warning' }]} />
        <Input placeholder="Filter" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-56" />
        <div className="flex-1" />
        {can('ack') && ackable.length > 0 && <Button size="sm" variant="primary" icon={<BellOff className="size-3.5" />} onClick={() => setAckOpen(true)}>Acknowledge {ackable.length}</Button>}
      </div>
      {alerts.isLoading ? <Loading /> : rows.length === 0 ? (
        <Empty title={status === 'active' ? 'No active alerts' : 'No alerts'} icon={<CheckCircle2 className="size-8 text-ok" />} />
      ) : (
        <Table>
          <thead><tr>
            {status === 'active' && can('ack') && <Th className="w-8"><Checkbox checked={sel.size > 0 && sel.size === rows.length} onChange={(v) => setSel(v ? new Set(rows.map((a) => a.id)) : new Set())} /></Th>}
            <Th>Severity</Th><Th>Alert</Th>{!hostId && <Th className="hidden md:table-cell">Host</Th>}<Th className="hidden sm:table-cell">State</Th><Th>Since</Th><Th className="hidden lg:table-cell">Duration</Th>
          </tr></thead>
          <tbody>
            {rows.map((a) => {
              const start = a.fired_at || a.started_at
              const end = a.resolved_at || Date.now() / 1000
              return (
                <tr key={a.id} className="hover:bg-panel-2/60">
                  {status === 'active' && can('ack') && <Td><Checkbox checked={sel.has(a.id)} onChange={(v) => { const s = new Set(sel); if (v) s.add(a.id); else s.delete(a.id); setSel(s) }} /></Td>}
                  <Td><div className="flex items-center gap-2"><StatusDot status={a.status === 'resolved' ? 'up' : a.severity === 'critical' ? 'down' : 'warning'} pulse={a.status === 'firing' && !a.acked_by} /><SeverityBadge severity={a.severity} /></div></Td>
                  <Td><Link to={`/alerts/${a.id}`} className="font-medium hover:underline">{a.title}</Link><div className="max-w-xl truncate text-xs text-muted">{a.message}</div></Td>
                  {!hostId && <Td className="hidden text-xs md:table-cell">{a.host_id ? <Link to={`/hosts/${a.host_id}`} className="hover:underline">{a.host_name}</Link> : <Badge>{a.source}</Badge>}</Td>}
                  <Td className="hidden sm:table-cell">
                    <div className="flex flex-wrap gap-1">
                      <Badge tone={a.status === 'firing' ? 'crit' : a.status === 'pending' ? 'warn' : 'ok'}>{a.status}{a.status === 'pending' ? ` ${a.pending_count}` : ''}</Badge>
                      {a.acked_by && <Badge tone="info" title={a.ack_comment}>acked</Badge>}
                      {a.flapping && <Badge tone="warn">flapping</Badge>}
                      {a.in_maintenance && <Badge tone="maint">maintenance</Badge>}
                    </div>
                  </Td>
                  <Td className="text-xs text-muted" title={dateTime(start)}>{ago(start)}</Td>
                  <Td className="hidden text-xs text-muted lg:table-cell">{duration(end - start)}</Td>
                </tr>
              )
            })}
          </tbody>
        </Table>
      )}
      {ackOpen && <AckDialog ids={ackable.map((a) => a.id)} onClose={() => { setAckOpen(false); setSel(new Set()); qc.invalidateQueries({ queryKey: ['alerts'] }) }} onDone={(n) => toast(`${n} alert(s) acknowledged`)} />}
    </Card>
  )
}

function AckDialog({ ids, onClose, onDone }: { ids: number[]; onClose: () => void; onDone: (n: number) => void }) {
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  return (
    <Modal open onClose={onClose} title={`Acknowledge ${ids.length} alert(s)`} size="sm"
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" loading={busy} onClick={async () => {
        setBusy(true)
        try { const r = await post<{ acked: number }>('/alerts/ack', { ids, comment }); onDone(r.acked); onClose() } catch (e) { setErr(e) } finally { setBusy(false) }
      }}>Acknowledge</Button></>}>
      <Field label="Comment (optional)" hint="Shown to the team; acknowledged alerts stop repeating notifications but still resolve normally.">
        <Textarea autoFocus value={comment} onChange={(e) => setComment(e.target.value)} placeholder="Investigating — disk cleanup in progress" />
      </Field>
      <ErrorBox error={err} className="mt-2" />
    </Modal>
  )
}

const eventTone: Record<string, 'ok' | 'warn' | 'crit' | 'info' | 'muted' | 'maint'> = {
  fired: 'crit', resolved: 'ok', acked: 'info', unacked: 'muted', escalated: 'crit', flapping: 'warn', flapping_stopped: 'ok', notified: 'muted', silenced: 'maint',
}

export function AlertDetail() {
  const { id = '' } = useParams()
  const { can } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const [ack, setAck] = useState(false)
  const d = useQuery({ queryKey: ['alert', id], queryFn: () => get<{ alert: Alert; events: AlertEvent[] }>(`/alerts/${id}`), refetchInterval: 30_000 })
  if (d.isLoading) return <Loading />
  if (d.error) return <ErrorBox error={d.error} />
  const { alert: a, events } = d.data!
  const act = async (path: string, msg: string) => {
    try { await post(`/alerts/${a.id}/${path}`); toast(msg); qc.invalidateQueries({ queryKey: ['alert', id] }) } catch (e) { toast(errorMessage(e), 'crit') }
  }
  return (
    <div>
      <PageHeader back={<Link to="/alerts" className="mb-1 inline-flex items-center gap-1 text-xs text-muted hover:text-fg"><ArrowLeft className="size-3" />Alerts</Link>}
        title={a.title} subtitle={a.message}
        actions={a.status !== 'resolved' && can('ack') && <>
          {a.acked_by ? <Button icon={<BellRing className="size-4" />} onClick={() => act('unack', 'Acknowledgement removed')}>Unacknowledge</Button>
            : <Button variant="primary" icon={<BellOff className="size-4" />} onClick={() => setAck(true)}>Acknowledge</Button>}
          <Button icon={<CircleCheck className="size-4" />} onClick={() => act('resolve', 'Alert resolved')}>Resolve</Button>
        </>} />
      <div className="grid gap-4 xl:grid-cols-3">
        <Card title="Details">
          <KV items={[
            ['Status', <span key="s" className="flex flex-wrap gap-1"><Badge tone={a.status === 'firing' ? 'crit' : a.status === 'pending' ? 'warn' : 'ok'}>{a.status}</Badge>{a.flapping && <Badge tone="warn">flapping</Badge>}{a.in_maintenance && <Badge tone="maint">in maintenance</Badge>}</span>],
            ['Severity', <SeverityBadge key="v" severity={a.severity} />],
            ['Source', a.source + (a.metric ? ` · ${a.metric}` : '') + (a.labels ? ` · ${a.labels}` : '')],
            ['Host', a.host_id ? <Link key="h" to={`/hosts/${a.host_id}`} className="text-accent hover:underline">{a.host_name}</Link> : '–'],
            ['Check', a.check_id ? <Link key="c" to={`/checks/${a.check_id}`} className="text-accent hover:underline">view check</Link> : '–'],
            ['Value', a.value !== null ? `${a.value?.toFixed(2)}${a.threshold !== null ? ` (threshold ${a.threshold})` : ''}` : '–'],
            ['Started', dateTime(a.started_at)],
            ['Fired', dateTime(a.fired_at)],
            ['Resolved', a.resolved_at ? dateTime(a.resolved_at) : '–'],
            ['Acknowledged', a.acked_by ? `${a.acked_by}, ${ago(a.acked_at)}${a.ack_comment ? ` — “${a.ack_comment}”` : ''}` : 'no'],
            ['Notifications', `${a.notify_count} sent${a.last_notified_at ? `, last ${ago(a.last_notified_at)}` : ''}`],
          ]} />
        </Card>
        <Card title="Timeline" className="xl:col-span-2" bodyClass="p-0">
          {events.length === 0 ? <Empty title="No events" /> : (
            <ol className="divide-y divide-line">
              {events.map((e) => (
                <li key={e.id} className="flex items-start gap-3 px-4 py-2">
                  <Badge tone={eventTone[e.event] || 'muted'} className="mt-0.5 w-24 justify-center">{e.event.replace('_', ' ')}</Badge>
                  <div className="min-w-0 flex-1 text-sm">{e.message}{e.actor && <span className="text-muted"> — {e.actor}</span>}</div>
                  <span className="shrink-0 text-xs text-muted" title={dateTime(e.created_at)}>{ago(e.created_at)}</span>
                </li>
              ))}
            </ol>
          )}
        </Card>
      </div>
      {ack && <AckDialog ids={[a.id]} onClose={() => { setAck(false); qc.invalidateQueries({ queryKey: ['alert', id] }) }} onDone={() => toast('Alert acknowledged')} />}
    </div>
  )
}
