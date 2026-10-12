import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarClock, Pencil, Plus, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { dateTime, duration, localZone, timezones } from '@/lib/format'
import type { Host, HostGroup, MaintenanceWindow } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Checkbox, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, Segmented, Select, Table, Td, Th, Toggle, useConfirm, useToast } from '@/components/ui'

const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

function describe(w: MaintenanceWindow) {
  if (w.recurrence === 'none') return `${dateTime(w.starts_at)} → ${dateTime(w.ends_at)}`
  const d = duration(w.duration_minutes * 60)
  if (w.recurrence === 'daily') return `Daily at ${w.start_time} for ${d} (${w.timezone})`
  if (w.recurrence === 'weekly') return `${(w.weekdays || []).map((x) => days[x]).join(', ')} at ${w.start_time} for ${d} (${w.timezone})`
  return `Monthly on day ${new Date(w.starts_at * 1000).getDate()} at ${w.start_time} for ${d} (${w.timezone})`
}

export function Maintenance() {
  const { can } = useAuth()
  const { tenant } = useTenantFilter()
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const [edit, setEdit] = useState<Partial<MaintenanceWindow> | null>(null)
  const w = useQuery({ queryKey: ['maintenance', tenant], queryFn: () => get<MaintenanceWindow[]>('/maintenance', { tenant_id: tenant ?? undefined }), refetchInterval: 60_000 })
  const hosts = useQuery({ queryKey: ['hosts', 'all'], queryFn: () => get<Host[]>('/hosts') })
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => get<HostGroup[]>('/groups') })
  const scopeText = (m: MaintenanceWindow) => {
    if (m.scope === 'all') return 'All hosts'
    if (m.scope === 'tenant') return 'Whole tenant'
    const ids = m.scope_ids || []
    const names = m.scope === 'host' ? ids.map((i) => hosts.data?.find((h) => h.id === i)?.name || `#${i}`) : ids.map((i) => groups.data?.find((g) => g.id === i)?.name || `#${i}`)
    return `${m.scope === 'host' ? 'Hosts' : 'Groups'}: ${names.join(', ')}`
  }
  return (
    <div>
      <PageHeader title="Maintenance windows" subtitle="Silence alerts during planned work. Windows that allow jobs also run upgrades scheduled “during maintenance”."
        actions={can('monitoring') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({})}>New window</Button>} />
      <Card bodyClass="p-0">
        {w.isLoading ? <Loading /> : !w.data?.length ? <Empty title="No maintenance windows" icon={<CalendarClock className="size-8" />} /> : (
          <Table>
            <thead><tr><Th>Name</Th><Th>Schedule</Th><Th className="hidden md:table-cell">Applies to</Th><Th>Status</Th><Th className="hidden lg:table-cell">Next</Th><Th /></tr></thead>
            <tbody>
              {w.data.map((m) => (
                <tr key={m.id} className={m.enabled ? '' : 'opacity-50'}>
                  <Td className="font-medium">{m.name}<div className="flex gap-1">{m.suppress_alerts && <Badge>silences alerts</Badge>}{m.allow_jobs && <Badge tone="info">runs jobs</Badge>}</div></Td>
                  <Td className="text-xs">{describe(m)}</Td>
                  <Td className="hidden max-w-xs truncate text-xs md:table-cell">{scopeText(m)}</Td>
                  <Td>{m.active_now ? <Badge tone="maint">active now</Badge> : m.enabled ? <Badge>scheduled</Badge> : <Badge>disabled</Badge>}</Td>
                  <Td className="hidden text-xs text-muted lg:table-cell">{m.next_start ? `${dateTime(m.next_start)} → ${dateTime(m.next_end)}` : '–'}</Td>
                  <Td className="whitespace-nowrap text-right">
                    {can('monitoring') && <>
                      <Button size="sm" variant="ghost" icon={<Pencil className="size-3.5" />} onClick={() => setEdit(m)} />
                      <Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                        if (!(await confirm({ title: 'Delete window', danger: true, confirmLabel: 'Delete', body: <p>Delete <b>{m.name}</b>?</p> }))) return
                        try { await del(`/maintenance/${m.id}`); qc.invalidateQueries({ queryKey: ['maintenance'] }); toast('Deleted') } catch (e) { toast(errorMessage(e), 'crit') }
                      }} />
                    </>}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      {edit && <MaintEditor initial={edit} hosts={hosts.data || []} groups={groups.data || []} onClose={() => { setEdit(null); qc.invalidateQueries({ queryKey: ['maintenance'] }) }} />}
    </div>
  )
}

function toLocal(unix: number) {
  if (!unix) return ''
  const d = new Date(unix * 1000)
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset())
  return d.toISOString().slice(0, 16)
}

function MaintEditor({ initial, hosts, groups, onClose }: { initial: Partial<MaintenanceWindow>; hosts: Host[]; groups: HostGroup[]; onClose: () => void }) {
  const { me } = useAuth()
  const { tenant } = useTenantFilter()
  const toast = useToast()
  const now = Math.floor(Date.now() / 1000)
  const [m, setM] = useState<Partial<MaintenanceWindow>>({
    name: '', scope: 'host', scope_ids: [], recurrence: 'none', starts_at: now, ends_at: now + 3600, weekdays: [6], start_time: '02:00', duration_minutes: 120,
    timezone: localZone(), suppress_alerts: true, allow_jobs: true, enabled: true, tenant_id: tenant ?? 0, ...initial,
  })
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const ids = new Set(m.scope_ids || [])
  const toggleId = (id: number) => { const s = new Set(ids); if (s.has(id)) s.delete(id); else s.add(id); setM({ ...m, scope_ids: [...s] }) }
  const recurring = m.recurrence !== 'none'
  return (
    <Modal open onClose={onClose} title={m.id ? 'Edit maintenance window' : 'New maintenance window'} size="lg"
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" loading={busy} onClick={async () => {
        setBusy(true); setErr(null)
        try { if (m.id) await put(`/maintenance/${m.id}`, m); else await post('/maintenance', m); toast('Saved'); onClose() } catch (e) { setErr(e) } finally { setBusy(false) }
      }}>Save</Button></>}>
      <div className="space-y-4">
        <Field label="Name" required><Input value={m.name} onChange={(e) => setM({ ...m, name: e.target.value })} placeholder="Weekly patching" /></Field>
        <Field label="Applies to">
          <Segmented value={m.scope as any} onChange={(v) => setM({ ...m, scope: v, scope_ids: [] })} options={[
            ...(me?.user.tenant_id === 0 ? [{ id: 'all' as const, label: 'Everything' }] : []), { id: 'tenant' as const, label: 'Tenant' }, { id: 'group' as const, label: 'Groups' }, { id: 'host' as const, label: 'Hosts' },
          ]} />
        </Field>
        {(m.scope === 'host' || m.scope === 'group') && (
          <div className="max-h-48 overflow-y-auto rounded-xl border border-line/70 p-3">
            <div className="grid gap-1 sm:grid-cols-2">
              {(m.scope === 'host' ? hosts : groups).map((x) => <Checkbox key={x.id} checked={ids.has(x.id)} onChange={() => toggleId(x.id)} label={x.name} />)}
            </div>
          </div>
        )}
        <Field label="Schedule">
          <Segmented value={m.recurrence as any} onChange={(v) => setM({ ...m, recurrence: v })} options={[{ id: 'none', label: 'One-off' }, { id: 'daily', label: 'Daily' }, { id: 'weekly', label: 'Weekly' }, { id: 'monthly', label: 'Monthly' }]} />
        </Field>
        {!recurring ? (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Start"><Input type="datetime-local" value={toLocal(m.starts_at!)} onChange={(e) => setM({ ...m, starts_at: Math.floor(new Date(e.target.value).getTime() / 1000) })} /></Field>
            <Field label="End"><Input type="datetime-local" value={toLocal(m.ends_at!)} onChange={(e) => setM({ ...m, ends_at: Math.floor(new Date(e.target.value).getTime() / 1000) })} /></Field>
          </div>
        ) : (
          <div className="grid gap-3 sm:grid-cols-3">
            {m.recurrence === 'weekly' && (
              <div className="sm:col-span-3 flex flex-wrap gap-2">
                {days.map((d, i) => <Checkbox key={d} checked={(m.weekdays || []).includes(i)} onChange={(v) => setM({ ...m, weekdays: v ? [...(m.weekdays || []), i] : (m.weekdays || []).filter((x) => x !== i) })} label={d} />)}
              </div>
            )}
            {m.recurrence === 'monthly' && <Field label="First occurrence" className="sm:col-span-3"><Input type="date" value={toLocal(m.starts_at!).slice(0, 10)} onChange={(e) => setM({ ...m, starts_at: Math.floor(new Date(e.target.value).getTime() / 1000) })} /></Field>}
            <Field label="Start time"><Input type="time" value={m.start_time} onChange={(e) => setM({ ...m, start_time: e.target.value })} /></Field>
            <Field label="Duration (minutes)"><Input type="number" min={1} value={m.duration_minutes} onChange={(e) => setM({ ...m, duration_minutes: Number(e.target.value) })} /></Field>
            <Field label="Timezone"><Select value={m.timezone} onChange={(e) => setM({ ...m, timezone: e.target.value })}>
              {timezones().map((z) => <option key={z}>{z}</option>)}
            </Select></Field>
          </div>
        )}
        <div className="space-y-2">
          <Toggle checked={!!m.suppress_alerts} onChange={(v) => setM({ ...m, suppress_alerts: v })} label="Silence notifications" hint="Alerts are still recorded and shown, but nobody is paged" />
          <Toggle checked={!!m.allow_jobs} onChange={(v) => setM({ ...m, allow_jobs: v })} label="Run scheduled jobs" hint="Upgrades scheduled “during maintenance” run in this window" />
          <Toggle checked={m.enabled !== false} onChange={(v) => setM({ ...m, enabled: v })} label="Enabled" />
        </div>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
