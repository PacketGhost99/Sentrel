import { useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Boxes, Pencil, Plus, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import type { HostGroup } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, Table, Td, Th, useConfirm, useToast } from '@/components/ui'

export function Groups() {
  const { can } = useAuth()
  const { tenant } = useTenantFilter()
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const [edit, setEdit] = useState<Partial<HostGroup> | null>(null)
  const groups = useQuery({ queryKey: ['groups', tenant], queryFn: () => get<HostGroup[]>('/groups', { tenant_id: tenant ?? undefined }) })
  const tags = useQuery({ queryKey: ['tags', tenant], queryFn: () => get<{ tag: string; count: number }[]>('/tags', { tenant_id: tenant ?? undefined }) })
  return (
    <div>
      <PageHeader title="Groups & tags" info="Groups carry threshold overrides, maintenance windows and notification routes. Tags (e.g. env:prod, role:db) filter views and notifications."
        actions={can('monitoring') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ tenant_id: tenant ?? 0 })}>New group</Button>} />
      <div className="grid gap-4 xl:grid-cols-3">
        <Card title="Groups" className="xl:col-span-2" bodyClass="p-0">
          {groups.isLoading ? <Loading /> : !groups.data?.length ? <Empty title="No groups" icon={<Boxes className="size-8" />} /> : (
            <Table>
              <thead><tr><Th>Group</Th><Th>Hosts</Th><Th className="hidden md:table-cell">Description</Th><Th /></tr></thead>
              <tbody>
                {groups.data.map((g) => (
                  <tr key={g.id}>
                    <Td><span className="inline-flex items-center gap-2 font-medium"><span className="size-2.5 rounded-full" style={{ background: g.color || 'var(--muted)' }} /><Link to={`/hosts?group_id=${g.id}`} className="hover:underline">{g.name}</Link></span></Td>
                    <Td>{g.host_count}</Td>
                    <Td className="hidden text-xs text-muted md:table-cell">{g.description}</Td>
                    <Td className="whitespace-nowrap text-right">{can('monitoring') && <>
                      <Button size="sm" variant="ghost" icon={<Pencil className="size-3.5" />} onClick={() => setEdit(g)} />
                      <Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                        if (!(await confirm({ title: 'Delete group', danger: true, confirmLabel: 'Delete', body: <p>Delete <b>{g.name}</b>? Hosts stay but lose the group and its threshold overrides.</p> }))) return
                        try { await del(`/groups/${g.id}`); qc.invalidateQueries({ queryKey: ['groups'] }); toast('Group deleted') } catch (e) { toast(errorMessage(e), 'crit') }
                      }} />
                    </>}</Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>
        <Card title="Tags in use">
          {!tags.data?.length ? <p className="text-sm text-muted">Add tags in each host's settings.</p> : (
            <div className="flex flex-wrap gap-1.5">
              {tags.data.map((t) => <Link key={t.tag} to={`/hosts?tag=${encodeURIComponent(t.tag)}`}><Badge tone="info">{t.tag} · {t.count}</Badge></Link>)}
            </div>
          )}
        </Card>
      </div>
      {edit && <GroupEditor initial={edit} onClose={() => { setEdit(null); qc.invalidateQueries({ queryKey: ['groups'] }) }} />}
    </div>
  )
}

function GroupEditor({ initial, onClose }: { initial: Partial<HostGroup>; onClose: () => void }) {
  const [g, setG] = useState({ name: '', description: '', color: '#3b82f6', ...initial })
  const [err, setErr] = useState<unknown>(null)
  return (
    <Modal open onClose={onClose} title={g.id ? 'Edit group' : 'New group'} size="sm"
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={async () => {
        try { if (g.id) await put(`/groups/${g.id}`, g); else await post('/groups', g); onClose() } catch (e) { setErr(e) }
      }}>Save</Button></>}>
      <div className="space-y-3">
        <Field label="Name" required><Input autoFocus value={g.name} onChange={(e) => setG({ ...g, name: e.target.value })} /></Field>
        <Field label="Description"><Input value={g.description} onChange={(e) => setG({ ...g, description: e.target.value })} /></Field>
        <Field label="Colour"><input type="color" value={g.color || '#3b82f6'} onChange={(e) => setG({ ...g, color: e.target.value })} className="h-9 w-16 rounded border border-line bg-panel" /></Field>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
