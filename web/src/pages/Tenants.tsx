import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Building2, Pencil, Plus, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { dateTime } from '@/lib/format'
import type { Tenant } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, Table, Td, Textarea, Th, Toggle, useConfirm, useToast } from '@/components/ui'

export function Tenants() {
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const nav = useNavigate()
  const { setTenant } = useTenantFilter()
  const [edit, setEdit] = useState<Partial<Tenant> | null>(null)
  const t = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants') })
  return (
    <div>
      <PageHeader title="Tenants" subtitle="Customers or business units. Each tenant's users see only their own hosts, checks, alerts and branding. Portal: /login?tenant=<slug> or a custom hostname."
        actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({})}>New tenant</Button>} />
      <Card bodyClass="p-0">
        {t.isLoading ? <Loading /> : !t.data?.length ? <Empty title="No tenants" icon={<Building2 className="size-8" />} /> : (
          <Table>
            <thead><tr><Th>Tenant</Th><Th>Portal</Th><Th className="hidden md:table-cell">Created</Th><Th /></tr></thead>
            <tbody>{t.data.map((x) => (
              <tr key={x.id} className={x.disabled ? 'opacity-50' : ''}>
                <Td><div className="font-medium">{x.name} {x.id === 0 && <Badge tone="accent">provider</Badge>} {x.disabled && <Badge tone="crit">disabled</Badge>}</div><div className="mono text-xs text-muted">{x.slug}</div></Td>
                <Td className="text-xs">{x.id === 0 ? '–' : <><span className="mono">{location.origin}/login?tenant={x.slug}</span>{x.hostname && <div className="mono text-muted">{x.hostname}</div>}</>}</Td>
                <Td className="hidden text-xs text-muted md:table-cell">{dateTime(x.created_at)}</Td>
                <Td className="whitespace-nowrap text-right">
                  {x.id !== 0 && <Button size="sm" variant="ghost" onClick={() => { setTenant(x.id); nav('/hosts') }}>View hosts</Button>}
                  <Button size="sm" variant="ghost" icon={<Pencil className="size-3.5" />} onClick={() => setEdit(x)} />
                  {x.id !== 0 && <Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                    if (!(await confirm({ title: 'Delete tenant', danger: true, confirmLabel: 'Delete tenant', typeToConfirm: x.slug, body: <p>Delete <b>{x.name}</b> with <b>all</b> its hosts, metrics, users, checks and history? Agents are revoked. This cannot be undone.</p> }))) return
                    try { await del(`/tenants/${x.id}`); toast('Tenant deleted'); qc.invalidateQueries({ queryKey: ['tenants'] }) } catch (e) { toast(errorMessage(e), 'crit') }
                  }} />}
                </Td>
              </tr>
            ))}</tbody>
          </Table>
        )}
      </Card>
      {edit && <TenantEditor initial={edit} onClose={() => { setEdit(null); qc.invalidateQueries({ queryKey: ['tenants'] }) }} />}
    </div>
  )
}

function TenantEditor({ initial, onClose }: { initial: Partial<Tenant>; onClose: () => void }) {
  const [x, setX] = useState<Partial<Tenant>>({ name: '', slug: '', hostname: '', notes: '', ...initial })
  const [err, setErr] = useState<unknown>(null)
  return (
    <Modal open onClose={onClose} title={x.id !== undefined ? `Edit ${x.name}` : 'New tenant'}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={async () => {
        try { if (x.id !== undefined) await put(`/tenants/${x.id}`, x); else await post('/tenants', x); onClose() } catch (e) { setErr(e) }
      }}>Save</Button></>}>
      <div className="space-y-3">
        <Field label="Name" required><Input value={x.name} onChange={(e) => setX({ ...x, name: e.target.value, slug: x.id !== undefined ? x.slug : e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') })} /></Field>
        {x.id !== 0 && <Field label="Slug" hint="Used in the portal link /login?tenant=slug"><Input value={x.slug} onChange={(e) => setX({ ...x, slug: e.target.value })} /></Field>}
        {x.id !== 0 && <Field label="Custom portal hostname (optional)" hint="If this hostname points at the server, its login page shows the tenant's branding"><Input value={x.hostname} onChange={(e) => setX({ ...x, hostname: e.target.value })} placeholder="monitor.customer.com" /></Field>}
        <Field label="Notes"><Textarea value={x.notes} onChange={(e) => setX({ ...x, notes: e.target.value })} /></Field>
        {x.id !== undefined && x.id !== 0 && <Toggle checked={!!x.disabled} onChange={(v) => setX({ ...x, disabled: v })} label="Disabled" hint="Tenant users cannot sign in; monitoring continues" />}
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
