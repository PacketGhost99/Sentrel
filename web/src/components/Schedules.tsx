import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarClock, Pencil, Plus, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago } from '@/lib/format'
import type { Host, HostGroup, UpdateSchedule } from '@/lib/types'
import { Button, Card, Empty, ErrorBox, Field, InfoTip, Input, Modal, Segmented, Select, Table, Td, Th, Toggle, useConfirm, useToast } from '@/components/ui'

const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

export function ScanSchedules() {
  const { can, me } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const [adding, setAdding] = useState(false)
  const s = useQuery({ queryKey: ['vulns', 'schedules'], queryFn: () => get<any[]>('/vuln-schedules') })
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => get<HostGroup[]>('/groups') })
  return (
    <Card bodyClass="p-0" title="Scan schedules" info="Hosts are also rescanned automatically whenever their package inventory changes."
      actions={can('jobs') && <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setAdding(true)}>Add schedule</Button>}>
      {!s.data?.length ? <Empty title="No scan schedules" icon={<CalendarClock className="size-8" />} /> : (
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
      {adding && <ScanScheduleEditor provider={me?.user.tenant_id === 0} groups={groups.data || []} onClose={() => { setAdding(false); qc.invalidateQueries({ queryKey: ['vulns', 'schedules'] }) }} />}
    </Card>
  )
}

function ScanScheduleEditor({ provider, groups, onClose }: { provider: boolean; groups: HostGroup[]; onClose: () => void }) {
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

const KEY = ['update-schedules']

export function whenLabel(s: Pick<UpdateSchedule, 'frequency' | 'weekday' | 'month_day' | 'time_of_day'>): string {
  switch (s.frequency) {
    case 'weekly': return `Weekly, ${weekdays[s.weekday]?.slice(0, 3)} ${s.time_of_day}`
    case 'monthly': return `Monthly, day ${s.month_day} ${s.time_of_day}`
    case 'maintenance': return 'Maintenance windows'
    default: return `Daily ${s.time_of_day}`
  }
}

export function UpdateSchedules() {
  const { can, me } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const [editing, setEditing] = useState<UpdateSchedule | 'new' | null>(null)
  const list = useQuery({ queryKey: KEY, queryFn: () => get<UpdateSchedule[]>('/update-schedules'), refetchInterval: 30_000 })
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => get<HostGroup[]>('/groups') })
  const hosts = useQuery({ queryKey: ['hosts', 'agents'], queryFn: () => get<Host[]>('/hosts', { kind: 'agent' }) })
  const manage = can('jobs')
  const scopeName = (x: UpdateSchedule) => x.scope === 'group' ? groups.data?.find((g) => g.id === x.scope_id)?.name || `Group #${x.scope_id}`
    : x.scope === 'host' ? hosts.data?.find((h) => h.id === x.scope_id)?.name || `Host #${x.scope_id}` : x.scope === 'tenant' ? 'Tenant' : 'All hosts'
  const toggle = async (x: UpdateSchedule, enabled: boolean) => {
    try {
      await put(`/update-schedules/${x.id}`, { ...x, enabled })
      qc.invalidateQueries({ queryKey: KEY })
    } catch (e) { toast(errorMessage(e), 'crit') }
  }
  const remove = async (x: UpdateSchedule) => {
    if (!(await confirm({ title: 'Delete schedule', body: `Delete ${x.name || 'this schedule'}? Jobs it already queued are not cancelled.`, confirmLabel: 'Delete', danger: true }))) return
    try { await del(`/update-schedules/${x.id}`); qc.invalidateQueries({ queryKey: KEY }); toast('Deleted') } catch (e) { toast(errorMessage(e), 'crit') }
  }
  return (
    <Card bodyClass="p-0" title="Update schedules" info="Queues package upgrades on a schedule. Hosts with remote upgrades off, read-only agents and operator-signed agents are skipped. Schedules never reboot hosts."
      actions={manage && <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEditing('new')}>Add schedule</Button>}>
      {!list.data?.length ? <Empty title="No update schedules" icon={<CalendarClock className="size-8" />} /> : (
        <Table>
          <thead><tr><Th>Name</Th><Th>Scope</Th><Th>When</Th><Th className="hidden md:table-cell">Installs</Th><Th className="hidden lg:table-cell">Last run</Th><Th>Enabled</Th><Th /></tr></thead>
          <tbody>{list.data.map((x) => {
            const skipped = x.last_result?.skipped || []
            return (
              <tr key={x.id}>
                <Td className="font-medium">{x.name || <span className="text-muted">Untitled</span>}</Td>
                <Td>{scopeName(x)}</Td>
                <Td>{whenLabel(x)}</Td>
                <Td className="hidden md:table-cell">{x.security_only ? 'Security updates' : 'All updates'}</Td>
                <Td className="hidden text-xs text-muted lg:table-cell">
                  {x.last_run_at ? <>
                    {ago(x.last_run_at)}
                    <div>{x.last_result?.queued ?? 0} queued{skipped.length > 0 && <> · {skipped.length} skipped
                      <InfoTip label="Skipped hosts"><ul className="space-y-1">{skipped.map((s) => <li key={s.host_id}><span className="font-medium">{s.host}</span>: {s.reason}</li>)}</ul></InfoTip></>}</div>
                  </> : 'Never'}
                </Td>
                <Td><Toggle checked={x.enabled} disabled={!manage} onChange={(v) => toggle(x, v)} /></Td>
                <Td className="text-right whitespace-nowrap">{manage && <>
                  <Button size="sm" variant="ghost" aria-label="Edit schedule" icon={<Pencil className="size-3.5" />} onClick={() => setEditing(x)} />
                  <Button size="sm" variant="ghost" aria-label="Delete schedule" icon={<Trash2 className="size-3.5" />} onClick={() => remove(x)} />
                </>}</Td>
              </tr>
            )
          })}</tbody>
        </Table>
      )}
      {editing && <UpdateScheduleEditor schedule={editing === 'new' ? null : editing} provider={me?.user.tenant_id === 0} groups={groups.data || []} hosts={hosts.data || []}
        onClose={() => { setEditing(null); qc.invalidateQueries({ queryKey: KEY }) }} />}
    </Card>
  )
}

function UpdateScheduleEditor({ schedule, provider, groups, hosts, onClose }: { schedule: UpdateSchedule | null; provider: boolean; groups: HostGroup[]; hosts: Host[]; onClose: () => void }) {
  const [x, setX] = useState(() => ({
    name: schedule?.name ?? '', scope: schedule?.scope ?? (provider ? 'all' : 'tenant'), scope_id: schedule?.scope_id ?? 0,
    frequency: schedule?.frequency ?? 'weekly', weekday: schedule?.weekday ?? 0, month_day: schedule?.month_day ?? 1, time_of_day: schedule?.time_of_day ?? '03:00',
    security_only: schedule?.security_only ?? true, enabled: schedule?.enabled ?? true,
  }))
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    try {
      if (schedule) await put(`/update-schedules/${schedule.id}`, x)
      else await post('/update-schedules', x)
      onClose()
    } catch (e) { setErr(e); setBusy(false) }
  }
  return (
    <Modal open onClose={onClose} title={schedule ? 'Edit update schedule' : 'Add update schedule'} size="sm"
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" loading={busy} onClick={save}>Save</Button></>}>
      <div className="space-y-3">
        <Field label="Name"><Input value={x.name} maxLength={80} placeholder="Weekly security patching" onChange={(e) => setX({ ...x, name: e.target.value })} /></Field>
        <Field label="Scope"><Select value={x.scope} onChange={(e) => setX({ ...x, scope: e.target.value as UpdateSchedule['scope'], scope_id: 0 })}>
          {provider && <option value="all">All hosts</option>}<option value="tenant">Tenant</option><option value="group">Group</option><option value="host">Host</option>
        </Select></Field>
        {x.scope === 'group' && <Field label="Group"><Select value={x.scope_id} onChange={(e) => setX({ ...x, scope_id: Number(e.target.value) })}><option value={0}>Choose…</option>{groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</Select></Field>}
        {x.scope === 'host' && <Field label="Host"><Select value={x.scope_id} onChange={(e) => setX({ ...x, scope_id: Number(e.target.value) })}><option value={0}>Choose…</option>{hosts.map((h) => <option key={h.id} value={h.id}>{h.name}</option>)}</Select></Field>}
        <Field label="When" info="Maintenance windows queues the upgrade on each host and holds it until that host's next maintenance window opens."><Segmented value={x.frequency} onChange={(v) => setX({ ...x, frequency: v })}
          options={[{ id: 'daily', label: 'Daily' }, { id: 'weekly', label: 'Weekly' }, { id: 'monthly', label: 'Monthly' }, { id: 'maintenance', label: 'Maintenance windows' }]} /></Field>
        {x.frequency === 'weekly' && <Field label="Day"><Select value={x.weekday} onChange={(e) => setX({ ...x, weekday: Number(e.target.value) })}>{weekdays.map((d, i) => <option key={d} value={i}>{d}</option>)}</Select></Field>}
        {x.frequency === 'monthly' && <Field label="Day of month" info="1 to 28, so every month has the day."><Input type="number" min={1} max={28} value={x.month_day} onChange={(e) => setX({ ...x, month_day: Number(e.target.value) })} /></Field>}
        {x.frequency !== 'maintenance' && <Field label="Time (server timezone)"><Input type="time" value={x.time_of_day} onChange={(e) => setX({ ...x, time_of_day: e.target.value })} /></Field>}
        <Field label="Install" info="Hosts with remote upgrades off, read-only agents and operator-signed agents are skipped. Schedules never reboot hosts.">
          <Select value={x.security_only ? 'security' : 'all'} onChange={(e) => setX({ ...x, security_only: e.target.value === 'security' })}>
            <option value="security">Security updates only (recommended)</option><option value="all">All updates</option>
          </Select>
        </Field>
        <Toggle checked={x.enabled} onChange={(v) => setX({ ...x, enabled: v })} label="Enabled" />
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
