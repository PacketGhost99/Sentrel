import { useEffect, useState } from 'react'
import { useParams, useSearchParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, ExternalLink, Globe, Pencil, Plus, Trash2, X } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { applyBranding, applyTheme, getThemePref } from '@/lib/auth'
import { useAuth } from '@/lib/auth'
import type { Branding, Check, Host, StatusPage, StatusPageItem } from '@/lib/types'
import { useTenantFilter, Logo } from '@/components/Layout'
import { Badge, Button, Card, CodeBlock, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, Select, Table, Td, Textarea, Th, Toggle, cx, useConfirm, useToast } from '@/components/ui'
import { UptimeBars } from './Checks'

export function StatusPages() {
  const { can } = useAuth()
  const { tenant } = useTenantFilter()
  const qc = useQueryClient()
  const confirm = useConfirm()
  const [edit, setEdit] = useState<Partial<StatusPage> | null>(null)
  const pages = useQuery({ queryKey: ['status-pages', tenant], queryFn: () => get<StatusPage[]>('/status-pages', { tenant_id: tenant ?? undefined }) })
  const link = (p: StatusPage) => `${location.origin}/status/${p.slug}${p.public ? '' : `?token=${p.access_token}`}`
  return (
    <div>
      <PageHeader title="Status pages" subtitle="Public or token-protected pages showing up/down and 90-day uptime for chosen hosts and checks."
        actions={can('monitoring') && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ tenant_id: tenant ?? 0 })}>New status page</Button>} />
      <Card bodyClass="p-0">
        {pages.isLoading ? <Loading /> : !pages.data?.length ? <Empty title="No status pages" icon={<Globe className="size-8" />} /> : (
          <Table>
            <thead><tr><Th>Page</Th><Th>Access</Th><Th>Items</Th><Th className="hidden md:table-cell">Link</Th><Th /></tr></thead>
            <tbody>{pages.data.map((p) => (
              <tr key={p.id}>
                <Td className="font-medium">{p.title}<div className="mono text-xs text-muted">/status/{p.slug}</div></Td>
                <Td>{p.public ? <Badge tone="ok">public</Badge> : <Badge tone="warn">token</Badge>}</Td>
                <Td>{p.items?.length || 0}</Td>
                <Td className="hidden md:table-cell"><a href={link(p)} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-xs text-accent hover:underline">open <ExternalLink className="size-3" /></a></Td>
                <Td className="whitespace-nowrap text-right">{can('monitoring') && <>
                  <Button size="sm" variant="ghost" icon={<Pencil className="size-3.5" />} onClick={() => setEdit(p)} />
                  <Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                    if (!(await confirm({ title: 'Delete status page', danger: true, confirmLabel: 'Delete', body: <p>Delete <b>{p.title}</b>? The link stops working.</p> }))) return
                    await del(`/status-pages/${p.id}`); qc.invalidateQueries({ queryKey: ['status-pages'] })
                  }} />
                </>}</Td>
              </tr>
            ))}</tbody>
          </Table>
        )}
      </Card>
      {edit && <StatusEditor initial={edit} onClose={() => { setEdit(null); qc.invalidateQueries({ queryKey: ['status-pages'] }) }} />}
    </div>
  )
}

function StatusEditor({ initial, onClose }: { initial: Partial<StatusPage>; onClose: () => void }) {
  const toast = useToast()
  const hosts = useQuery({ queryKey: ['hosts', 'all'], queryFn: () => get<Host[]>('/hosts') })
  const checks = useQuery({ queryKey: ['checks', 'all'], queryFn: () => get<Check[]>('/checks') })
  const [p, setP] = useState<Partial<StatusPage>>({ slug: '', title: '', description: '', public: true, items: [], show_history: true, announcement: '', ...initial })
  const [pick, setPick] = useState('')
  const [err, setErr] = useState<unknown>(null)
  const items = p.items || []
  const move = (i: number, d: number) => { const a = [...items]; const [x] = a.splice(i, 1); a.splice(i + d, 0, x); setP({ ...p, items: a }) }
  const add = () => {
    if (!pick) return
    const [type, id] = pick.split(':')
    const label = type === 'host' ? hosts.data?.find((h) => h.id === Number(id))?.name : checks.data?.find((c) => c.id === Number(id))?.name
    setP({ ...p, items: [...items, { type: type as StatusPageItem['type'], id: Number(id), label: label || '' }] })
    setPick('')
  }
  const save = async (regen = false) => {
    setErr(null)
    try {
      const body = { ...p, regenerate_token: regen }
      if (p.id) await put(`/status-pages/${p.id}`, body)
      else await post('/status-pages', body)
      toast('Status page saved')
      onClose()
    } catch (e) { setErr(e) }
  }
  return (
    <Modal open onClose={onClose} title={p.id ? 'Edit status page' : 'New status page'} size="lg"
      footer={<>{p.id && !p.public && <Button onClick={() => save(true)}>Regenerate token</Button>}<div className="flex-1" /><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={() => save()}>Save</Button></>}>
      <div className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Title" required><Input value={p.title} onChange={(e) => setP({ ...p, title: e.target.value, slug: p.id ? p.slug : e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') })} /></Field>
          <Field label="URL slug" hint={`${location.origin}/status/${p.slug || '…'}`}><Input value={p.slug} onChange={(e) => setP({ ...p, slug: e.target.value })} /></Field>
          <Field label="Description" className="sm:col-span-2"><Input value={p.description} onChange={(e) => setP({ ...p, description: e.target.value })} /></Field>
          <Field label="Announcement banner (optional)" className="sm:col-span-2"><Textarea value={p.announcement} onChange={(e) => setP({ ...p, announcement: e.target.value })} placeholder="Scheduled maintenance Saturday 02:00–04:00 UTC" /></Field>
        </div>
        <div className="space-y-2">
          <Toggle checked={!!p.public} onChange={(v) => setP({ ...p, public: v })} label="Public" hint="Off: only people with the secret link can view it" />
          <Toggle checked={!!p.show_history} onChange={(v) => setP({ ...p, show_history: v })} label="Show 90-day history" />
        </div>
        {!p.public && p.access_token && <CodeBlock code={`${location.origin}/status/${p.slug}?token=${p.access_token}`} />}
        <Field label="Items">
          <div className="space-y-2">
            {items.map((it, i) => (
              <div key={it.type + it.id} className="flex items-center gap-2">
                <Badge>{it.type}</Badge>
                <Input className="h-8 flex-1" value={it.label} onChange={(e) => { const a = [...items]; a[i] = { ...it, label: e.target.value }; setP({ ...p, items: a }) }} />
                <Button size="sm" variant="ghost" disabled={i === 0} icon={<ArrowUp className="size-3.5" />} onClick={() => move(i, -1)} />
                <Button size="sm" variant="ghost" disabled={i === items.length - 1} icon={<ArrowDown className="size-3.5" />} onClick={() => move(i, 1)} />
                <Button size="sm" variant="ghost" icon={<X className="size-3.5" />} onClick={() => setP({ ...p, items: items.filter((_, j) => j !== i) })} />
              </div>
            ))}
            <div className="flex gap-2">
              <div className="flex-1"><Select value={pick} onChange={(e) => setPick(e.target.value)}>
                <option value="">Add a host or check…</option>
                <optgroup label="Hosts">{hosts.data?.map((h) => <option key={h.id} value={`host:${h.id}`}>{h.name}</option>)}</optgroup>
                <optgroup label="Checks">{checks.data?.map((c) => <option key={c.id} value={`check:${c.id}`}>{c.name}</option>)}</optgroup>
              </Select></div>
              <Button onClick={add} disabled={!pick}>Add</Button>
            </div>
          </div>
        </Field>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}

interface PublicData {
  title: string
  description: string
  announcement: string
  overall: string
  items: { label: string; type: string; status: string; uptime_percent: number; days?: any[] }[]
  branding: Branding
  updated_at: number
}

const statusText: Record<string, string> = { operational: 'Operational', degraded: 'Degraded', down: 'Outage', maintenance: 'Maintenance', unknown: 'Unknown' }
const statusColor: Record<string, string> = { operational: 'bg-ok', degraded: 'bg-warn', down: 'bg-crit', maintenance: 'bg-maint', unknown: 'bg-muted' }

export function PublicStatus() {
  const { slug = '' } = useParams()
  const [params] = useSearchParams()
  const d = useQuery({ queryKey: ['public-status', slug], queryFn: () => get<PublicData>(`/public/status/${slug}`, { token: params.get('token') || '' }), refetchInterval: 60_000 })
  useEffect(() => { applyTheme(getThemePref()) }, [])
  useEffect(() => { if (d.data) { applyBranding(d.data.branding); document.title = `${d.data.title} — status` } }, [d.data])
  if (d.isLoading) return <Loading />
  if (d.error) return <div className="flex min-h-full items-center justify-center p-6"><ErrorBox error={errorMessage(d.error) === 'not found' ? 'Status page not found' : d.error} /></div>
  const x = d.data!
  const b = x.branding
  return (
    <div className="min-h-full bg-bg">
      <div className="mx-auto max-w-3xl px-4 py-10">
        <div className="mb-8 flex items-center gap-3">
          {b.logo_data_url ? <img src={b.logo_data_url} alt="" className="h-9 max-w-[180px] object-contain" /> : <Logo />}
          <div><h1 className="text-xl font-semibold">{x.title}</h1>{x.description && <p className="text-sm text-muted">{x.description}</p>}</div>
        </div>
        {x.announcement && <div className="mb-4 rounded-2xl border border-info/30 bg-info/10 p-4 text-sm whitespace-pre-line">{x.announcement}</div>}
        <div className={cx('mb-6 flex items-center gap-3 rounded-2xl p-5 text-white shadow-card', statusColor[x.overall])}>
          <span className="text-lg font-semibold">{x.overall === 'operational' ? 'All systems operational' : x.overall === 'down' ? 'Some systems are down' : x.overall === 'degraded' ? 'Degraded performance' : 'Maintenance in progress'}</span>
        </div>
        <Card bodyClass="p-0">
          <ul className="divide-y divide-line/60">
            {x.items.map((it, i) => (
              <li key={i} className="p-4">
                <div className="flex items-center justify-between gap-3">
                  <span className="font-medium">{it.label}</span>
                  <span className="flex items-center gap-2 text-sm"><span className={cx('size-2.5 rounded-full', statusColor[it.status])} />{statusText[it.status] || it.status}</span>
                </div>
                {it.days && (
                  <div className="mt-3">
                    <UptimeBars days={it.days} />
                    <div className="mt-1 flex justify-between text-[11px] text-muted"><span>90 days ago</span><span>{it.uptime_percent >= 0 ? `${it.uptime_percent.toFixed(2)}% uptime` : ''}</span><span>Today</span></div>
                  </div>
                )}
              </li>
            ))}
          </ul>
        </Card>
        <p className="mt-6 text-center text-xs text-muted">Updated {new Date(x.updated_at * 1000).toLocaleString()} · {b.footer_text || `Powered by ${b.product_name || 'Heartbeat'}`}</p>
      </div>
    </div>
  )
}
