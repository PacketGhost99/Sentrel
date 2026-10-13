import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Plus, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post } from '@/lib/api'
import { ago, dateTime } from '@/lib/format'
import type { APIKey } from '@/lib/types'
import { Badge, Button, Card, Checkbox, CodeBlock, Empty, ErrorBox, Field, Input, Loading, Modal, Note, PageHeader, Select, Table, Td, Th, useConfirm, useToast } from '@/components/ui'

const scopes = [
  ['read', 'Read hosts, metrics, alerts, checks'],
  ['write', 'Acknowledge alerts, manage checks, thresholds, maintenance and host settings'],
  ['jobs', 'Run upgrades, IP blocks and vulnerability scans'],
  ['admin', 'Everything your account can do'],
]

export function ApiKeys() {
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const [open, setOpen] = useState(false)
  const keys = useQuery({ queryKey: ['api-keys'], queryFn: () => get<APIKey[]>('/api-keys') })
  return (
    <div>
      <PageHeader title="API keys" info={<>Send <code className="mono">Authorization: Bearer sentrel_…</code>. A key never has more access than its owner.</>} subtitle={<a href="/api-docs" className="text-accent hover:underline">API reference</a>}
        actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setOpen(true)}>Create key</Button>} />
      <Card bodyClass="p-0">
        {keys.isLoading ? <Loading /> : !keys.data?.length ? <Empty title="No API keys" icon={<KeyRound className="size-8" />} /> : (
          <Table>
            <thead><tr><Th>Name</Th><Th>Prefix</Th><Th>Scopes</Th><Th className="hidden md:table-cell">Owner</Th><Th className="hidden md:table-cell">Last used</Th><Th className="hidden lg:table-cell">Expires</Th><Th /></tr></thead>
            <tbody>{keys.data.map((k) => (
              <tr key={k.id}>
                <Td className="font-medium">{k.name}</Td>
                <Td className="mono text-xs">sentrel_{k.prefix}_…</Td>
                <Td>{k.scopes.map((s) => <Badge key={s} className="mr-1">{s}</Badge>)}</Td>
                <Td className="hidden text-xs md:table-cell">{k.username}</Td>
                <Td className="hidden text-xs text-muted md:table-cell">{ago(k.last_used_at)}</Td>
                <Td className="hidden text-xs text-muted lg:table-cell">{k.expires_at ? (k.expires_at < Date.now() / 1000 ? <Badge tone="crit">expired</Badge> : dateTime(k.expires_at)) : 'never'}</Td>
                <Td className="text-right"><Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                  if (!(await confirm({ title: 'Revoke key', danger: true, confirmLabel: 'Revoke', body: <p>Revoke <b>{k.name}</b>? Anything using it stops working immediately.</p> }))) return
                  try { await del(`/api-keys/${k.id}`); toast('Key revoked'); qc.invalidateQueries({ queryKey: ['api-keys'] }) } catch (e) { toast(errorMessage(e), 'crit') }
                }} /></Td>
              </tr>
            ))}</tbody>
          </Table>
        )}
      </Card>
      {open && <CreateKey onClose={() => { setOpen(false); qc.invalidateQueries({ queryKey: ['api-keys'] }) }} />}
    </div>
  )
}

function CreateKey({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState('')
  const [sel, setSel] = useState<string[]>(['read'])
  const [exp, setExp] = useState('90')
  const [key, setKey] = useState('')
  const [err, setErr] = useState<unknown>(null)
  if (key) {
    return (
      <Modal open onClose={onClose} title="API key created" footer={<Button variant="primary" onClick={onClose}>Done</Button>}>
        <div className="space-y-3"><Note tone="warn">Copy the key now; it is not shown again.</Note><CodeBlock code={key} /><CodeBlock code={`curl -H "Authorization: Bearer ${key}" ${location.origin}/api/v1/hosts`} /></div>
      </Modal>
    )
  }
  return (
    <Modal open onClose={onClose} title="Create API key" footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={!name || !sel.length} onClick={async () => {
      try { const r = await post<{ key: string }>('/api-keys', { name, scopes: sel, expires_days: Number(exp) }); setKey(r.key) } catch (e) { setErr(e) }
    }}>Create</Button></>}>
      <div className="space-y-3">
        <Field label="Name" required><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Grafana, automation…" /></Field>
        <Field label="Scopes"><div className="space-y-1.5">{scopes.map(([s, d]) => (
          <div key={s}><Checkbox checked={sel.includes(s)} onChange={(v) => setSel(v ? [...sel, s] : sel.filter((x) => x !== s))} label={<span><b>{s}</b> <span className="text-muted">— {d}</span></span>} /></div>
        ))}</div></Field>
        <Field label="Expires"><Select value={exp} onChange={(e) => setExp(e.target.value)}><option value="30">30 days</option><option value="90">90 days</option><option value="365">1 year</option><option value="0">Never</option></Select></Field>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
