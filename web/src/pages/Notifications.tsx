import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Radar, Send, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago } from '@/lib/format'
import type { Channel, Host, HostGroup } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Checkbox, Empty, ErrorBox, Field, InfoTip, Input, Loading, Modal, PageHeader, Select, Table, TagInput, Td, Textarea, Th, Toggle, useConfirm, useToast } from '@/components/ui'

const types: { id: string; label: string; help: string }[] = [
  { id: 'discord', label: 'Discord', help: 'Server settings → Integrations → Webhooks → copy URL' },
  { id: 'slack', label: 'Slack', help: 'Create an Incoming Webhook in a Slack app' },
  { id: 'teams', label: 'Microsoft Teams', help: 'Workflows → “Post to a channel when a webhook request is received”' },
  { id: 'telegram', label: 'Telegram', help: 'Create a bot with @BotFather; chat ID from @userinfobot or a group' },
  { id: 'ntfy', label: 'ntfy', help: 'Topic on ntfy.sh or your own server' },
  { id: 'gotify', label: 'Gotify', help: 'Application token from your Gotify server' },
  { id: 'pushover', label: 'Pushover', help: 'Application API token and user/group key' },
  { id: 'email', label: 'Email', help: 'Uses the SMTP relay from Settings → Email' },
  { id: 'webhook', label: 'Generic webhook', help: 'POST JSON (or a custom template) to any URL' },
]

const defaultTemplate = `{
  "text": "{{.Subject}}",
  "alerts": [{{range $i, $a := .Items}}{{if $i}},{{end}}
    {"status": "{{$a.Status}}", "severity": "{{$a.Severity}}", "title": {{json $a.Title}}, "host": {{json $a.Host}}, "url": {{json $a.URL}}}{{end}}
  ]
}`

export function Notifications() {
  const { can } = useAuth()
  const { tenant } = useTenantFilter()
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const [edit, setEdit] = useState<Partial<Channel> | null>(null)
  const ch = useQuery({ queryKey: ['channels', tenant], queryFn: () => get<Channel[]>('/notification-channels', { tenant_id: tenant ?? undefined }) })
  const log = useQuery({ queryKey: ['notification-log', tenant], queryFn: () => get<any[]>('/notification-log', { tenant_id: tenant ?? undefined }), refetchInterval: 30_000 })
  return (
    <div>
      <PageHeader title="Notifications" info="Where alerts go. Events within the group-wait window are combined into one message per channel; unacknowledged alerts repeat at the channel's interval."
        actions={can('monitoring') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ type: 'discord', tenant_id: tenant ?? 0 })}>Add channel</Button>} />
      <Card bodyClass="p-0">
        {ch.isLoading ? <Loading /> : !ch.data?.length ? <Empty title="No channels yet" icon={<Radar className="size-8" />} /> : (
          <Table>
            <thead><tr><Th>Channel</Th><Th>Type</Th><Th className="hidden md:table-cell">Routing</Th><Th className="hidden lg:table-cell">Last sent</Th><Th>Status</Th><Th /></tr></thead>
            <tbody>
              {ch.data.map((c) => (
                <tr key={c.id} className={c.enabled ? '' : 'opacity-50'}>
                  <Td className="font-medium">{c.name}</Td>
                  <Td><Badge>{types.find((t) => t.id === c.type)?.label || c.type}</Badge></Td>
                  <Td className="hidden text-xs text-muted md:table-cell">
                    {c.min_severity === 'critical' ? 'critical only' : 'warning+'}{c.send_resolved ? ', resolved' : ''}{c.repeat_minutes ? `, repeat ${c.repeat_minutes}m` : ''}
                    {(c.filter_groups?.length || c.filter_tags?.length || c.filter_hosts?.length) ? ' · filtered' : ''}
                  </Td>
                  <Td className="hidden text-xs text-muted lg:table-cell">{ago(c.last_sent_at)}</Td>
                  <Td>{c.last_error ? <Badge tone="crit" title={c.last_error}>error</Badge> : <Badge tone="ok">ok</Badge>}</Td>
                  <Td className="whitespace-nowrap text-right">
                    {can('monitoring') && <>
                      <Button size="sm" variant="ghost" icon={<Send className="size-3.5" />} title="Send test" onClick={async () => {
                        try { const r = await post<any>(`/notification-channels/${c.id}/test`); toast(r.ok ? 'Test sent' : `Failed: ${r.error}`, r.ok ? 'ok' : 'crit'); qc.invalidateQueries({ queryKey: ['channels'] }) } catch (e) { toast(errorMessage(e), 'crit') }
                      }} />
                      <Button size="sm" variant="ghost" icon={<Pencil className="size-3.5" />} onClick={() => setEdit(c)} />
                      <Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                        if (!(await confirm({ title: 'Delete channel', danger: true, confirmLabel: 'Delete', body: <p>Delete <b>{c.name}</b>?</p> }))) return
                        await del(`/notification-channels/${c.id}`); qc.invalidateQueries({ queryKey: ['channels'] })
                      }} />
                    </>}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card title="Recent deliveries" className="mt-4" bodyClass="p-0">
        {!log.data?.length ? <Empty title="Nothing sent yet" /> : (
          <Table>
            <thead><tr><Th>When</Th><Th>Channel</Th><Th>Alerts</Th><Th>Status</Th></tr></thead>
            <tbody>{log.data.slice(0, 50).map((l) => (
              <tr key={l.id}><Td className="text-xs">{ago(l.created_at)}</Td><Td>{l.channel}</Td><Td className="text-xs">{(l.alert_ids || []).length}</Td>
                <Td>{l.status === 'sent' ? <Badge tone="ok">sent</Badge> : <Badge tone="crit" title={l.error}>failed</Badge>}{l.error && <div className="max-w-md truncate text-xs text-crit">{l.error}</div>}</Td></tr>
            ))}</tbody>
          </Table>
        )}
      </Card>
      {edit && <ChannelEditor initial={edit} onClose={() => { setEdit(null); qc.invalidateQueries({ queryKey: ['channels'] }) }} />}
    </div>
  )
}

function ChannelEditor({ initial, onClose }: { initial: Partial<Channel>; onClose: () => void }) {
  const toast = useToast()
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => get<HostGroup[]>('/groups') })
  const hosts = useQuery({ queryKey: ['hosts', 'all'], queryFn: () => get<Host[]>('/hosts') })
  const [c, setC] = useState<Partial<Channel>>({ name: '', min_severity: 'warning', send_resolved: true, repeat_minutes: 240, enabled: true, filter_groups: [], filter_tags: [], filter_hosts: [], config: {}, ...initial })
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const cfg = c.config || {}
  const setCfg = (patch: Record<string, unknown>) => setC({ ...c, config: { ...cfg, ...patch } })
  const t = types.find((x) => x.id === c.type)!
  const secret = (k: string, label: string) => (
    <Field label={label} hint={cfg[k + '_set'] ? 'Stored; leave blank to keep it' : undefined}>
      <Input type="password" autoComplete="off" value={cfg[k] || ''} onChange={(e) => setCfg({ [k]: e.target.value })} />
    </Field>
  )
  const test = async () => {
    setBusy(true)
    try {
      const r = await post<any>('/notification-channels/test', { ...c, channel_id: c.id || 0 })
      toast(r.ok ? 'Test message sent' : `Failed: ${r.error}`, r.ok ? 'ok' : 'crit')
    } catch (e) { toast(errorMessage(e), 'crit') } finally { setBusy(false) }
  }
  return (
    <Modal open onClose={onClose} title={c.id ? `Edit ${c.name}` : 'Add notification channel'} size="lg"
      footer={<>
        <Button icon={<Send className="size-4" />} loading={busy} onClick={test}>Send test</Button>
        <div className="flex-1" />
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" loading={busy} onClick={async () => {
          setBusy(true); setErr(null)
          try { if (c.id) await put(`/notification-channels/${c.id}`, c); else await post('/notification-channels', c); toast('Saved'); onClose() } catch (e) { setErr(e) } finally { setBusy(false) }
        }}>Save</Button>
      </>}>
      <div className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Type" info={t.help}>
            <Select value={c.type} onChange={(e) => setC({ ...c, type: e.target.value, config: {} })} disabled={!!c.id}>
              {types.map((x) => <option key={x.id} value={x.id}>{x.label}</option>)}
            </Select>
          </Field>
          <Field label="Name" required><Input value={c.name} onChange={(e) => setC({ ...c, name: e.target.value })} placeholder={`${t.label} – ops`} /></Field>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          {['discord', 'slack', 'teams', 'webhook'].includes(c.type!) && <Field label="Webhook URL" className="sm:col-span-2" required><Input value={cfg.url || ''} onChange={(e) => setCfg({ url: e.target.value })} /></Field>}
          {c.type === 'discord' && <Field label="Bot display name (optional)"><Input value={cfg.username || ''} onChange={(e) => setCfg({ username: e.target.value })} /></Field>}
          {c.type === 'telegram' && <>{secret('bot_token', 'Bot token')}<Field label="Chat ID" required><Input value={cfg.chat_id || ''} onChange={(e) => setCfg({ chat_id: e.target.value })} /></Field></>}
          {c.type === 'ntfy' && <>
            <Field label="Server"><Input value={cfg.server || ''} onChange={(e) => setCfg({ server: e.target.value })} placeholder="https://ntfy.sh" /></Field>
            <Field label="Topic" required><Input value={cfg.topic || ''} onChange={(e) => setCfg({ topic: e.target.value })} /></Field>
            {secret('token', 'Access token (optional)')}
            <Field label="Priority (1-5, 0 = automatic)"><Input type="number" min={0} max={5} value={cfg.priority || 0} onChange={(e) => setCfg({ priority: Number(e.target.value) })} /></Field>
          </>}
          {c.type === 'gotify' && <>
            <Field label="Server URL" required><Input value={cfg.server || ''} onChange={(e) => setCfg({ server: e.target.value })} /></Field>
            {secret('token', 'Application token')}
            <Field label="Priority (0 = automatic)"><Input type="number" min={0} max={10} value={cfg.priority || 0} onChange={(e) => setCfg({ priority: Number(e.target.value) })} /></Field>
          </>}
          {c.type === 'pushover' && <>{secret('token', 'API token')}{secret('user_key', 'User / group key')}
            <Field label="Priority (-2…2, 0 = automatic)"><Input type="number" min={-2} max={2} value={cfg.priority || 0} onChange={(e) => setCfg({ priority: Number(e.target.value) })} /></Field></>}
          {c.type === 'email' && <Field label="Recipients" className="sm:col-span-2"><TagInput value={cfg.to || []} onChange={(v) => setCfg({ to: v })} placeholder="ops@example.com" /></Field>}
          {c.type === 'webhook' && <>
            <Field label="Method"><Select value={cfg.method || 'POST'} onChange={(e) => setCfg({ method: e.target.value })}><option>POST</option><option>PUT</option></Select></Field>
            <Field label="Content type"><Input value={cfg.content_type || ''} placeholder="application/json" onChange={(e) => setCfg({ content_type: e.target.value })} /></Field>
            <Field label="Basic auth user"><Input value={cfg.username || ''} onChange={(e) => setCfg({ username: e.target.value })} /></Field>
            {secret('password', 'Basic auth password')}
            <Field label="Body template (Go text/template, optional)" className="sm:col-span-2"
              info={<>Leave empty to send the full JSON payload. Fields: <code>.Subject .Status .Severity .URL .Items</code> (each: <code>.Title .Message .Host .Severity .Status .Event .URL</code>); helpers <code>json</code>, <code>upper</code>, <code>time</code>.</>}>
              <Textarea className="mono min-h-36 text-xs" value={cfg.template || ''} onChange={(e) => setCfg({ template: e.target.value })} placeholder={defaultTemplate} />
            </Field>
          </>}
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="Minimum severity"><Select value={c.min_severity} onChange={(e) => setC({ ...c, min_severity: e.target.value as any })}><option value="warning">Warning and critical</option><option value="critical">Critical only</option></Select></Field>
          <Field label="Repeat unacknowledged every (min)" hint="0 = never" info="Re-sends a firing alert at this interval. Repeats stop when the alert is acknowledged, resolved, muted, or suppressed by a maintenance window or a parent that is down."><Input type="number" min={0} value={c.repeat_minutes} onChange={(e) => setC({ ...c, repeat_minutes: Number(e.target.value) })} /></Field>
          <div className="space-y-2 pt-5">
            <Toggle checked={!!c.send_resolved} onChange={(v) => setC({ ...c, send_resolved: v })} label="Send resolved" />
            <Toggle checked={c.enabled !== false} onChange={(v) => setC({ ...c, enabled: v })} label="Enabled" />
          </div>
        </div>
        <details className="rounded-xl border border-line/70 p-4">
          <summary className="flex cursor-pointer items-center gap-1.5 text-sm font-medium">Routing filters (optional)<InfoTip>With no filters the channel receives every alert in its tenant. Filters combine with AND.</InfoTip></summary>
          <div className="mt-3 space-y-3">
            <Field label="Only these groups">
              <div className="flex flex-wrap gap-3">{groups.data?.map((g) => <Checkbox key={g.id} checked={(c.filter_groups || []).includes(g.id)} label={g.name}
                onChange={(v) => setC({ ...c, filter_groups: v ? [...(c.filter_groups || []), g.id] : (c.filter_groups || []).filter((x) => x !== g.id) })} />)}</div>
            </Field>
            <Field label="Only hosts with any of these tags"><TagInput value={c.filter_tags || []} onChange={(v) => setC({ ...c, filter_tags: v })} /></Field>
            <Field label="Only these hosts">
              <div className="grid max-h-40 gap-1 overflow-y-auto sm:grid-cols-2">{hosts.data?.map((h) => <Checkbox key={h.id} checked={(c.filter_hosts || []).includes(h.id)} label={h.name}
                onChange={(v) => setC({ ...c, filter_hosts: v ? [...(c.filter_hosts || []), h.id] : (c.filter_hosts || []).filter((x) => x !== h.id) })} />)}</div>
            </Field>
          </div>
        </details>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
