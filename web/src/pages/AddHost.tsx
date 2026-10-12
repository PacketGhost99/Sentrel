import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, ArrowRight, Router, Server, Target } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import type { Host, HostGroup, Tenant } from '@/lib/types'
import { SnmpForm, defaultSnmp, type SnmpConfig } from '@/components/SnmpForm'
import { useTenantFilter } from '@/components/Layout'
import { Button, Card, ErrorBox, Field, Input, Note, PageHeader, Select, TagInput, Toggle, cx, useToast } from '@/components/ui'

type Method = 'snmp' | 'target'

export interface Placement { tenant_id: number; group_id: number; tags: string[]; name: string }

export function PlacementFields({ p, onChange, showName = true }: { p: Placement; onChange: (p: Placement) => void; showName?: boolean }) {
  const { me } = useAuth()
  const provider = me?.user.tenant_id === 0
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants'), enabled: provider })
  const groups = useQuery({ queryKey: ['groups', p.tenant_id], queryFn: () => get<HostGroup[]>('/groups', { tenant_id: provider ? p.tenant_id : undefined }) })
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {showName && <Field label="Display name" hint="Optional; defaults to the hostname"><Input value={p.name} onChange={(e) => onChange({ ...p, name: e.target.value })} /></Field>}
      {provider && (tenants.data?.length ?? 0) > 1 && (
        <Field label="Tenant">
          <Select value={p.tenant_id} onChange={(e) => onChange({ ...p, tenant_id: Number(e.target.value), group_id: 0 })}>
            {tenants.data!.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </Select>
        </Field>
      )}
      <Field label="Group">
        <Select value={p.group_id} onChange={(e) => onChange({ ...p, group_id: Number(e.target.value) })}>
          <option value={0}>No group</option>
          {groups.data?.filter((g) => g.tenant_id === p.tenant_id || !provider).map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
        </Select>
      </Field>
      <Field label="Tags"><TagInput value={p.tags} onChange={(tags) => onChange({ ...p, tags })} placeholder="env:prod" /></Field>
    </div>
  )
}

export function AddHost() {
  const [method, setMethod] = useState<Method>('snmp')
  const { tenant } = useTenantFilter()
  const [p, setP] = useState<Placement>({ tenant_id: tenant ?? 0, group_id: 0, tags: [], name: '' })
  const options: { id: Method; icon: React.ReactNode; title: string; desc: string }[] = [
    { id: 'snmp', icon: <Router />, title: 'SNMP device', desc: 'Switches, routers, firewalls, NAS, printers. v2c or v3, polled by the server.' },
    { id: 'target', icon: <Target />, title: 'Agentless target', desc: 'Anything reachable: ping, TCP, HTTP, certificate and DNS checks only.' },
  ]
  return (
    <div>
      <PageHeader back={<Link to="/hosts" className="mb-1 inline-flex items-center gap-1 text-xs text-muted hover:text-fg"><ArrowLeft className="size-3" />Hosts</Link>} title="Add host" />
      <Link to="/agents" className="group mb-4 flex items-center gap-4 rounded-2xl border border-accent/40 bg-accent/8 p-5 shadow-card hover:border-accent">
        <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-accent/15 text-accent"><Server className="size-6" /></span>
        <span className="min-w-0 flex-1">
          <span className="block font-semibold">Install the Sentrel agent</span>
          <span className="mt-0.5 block text-sm text-muted">Linux and Windows. A one-line command or a .deb, .rpm or .msi package from this server, with full metrics, inventory, CVEs and remote actions.</span>
        </span>
        <span className="hidden items-center gap-1 text-sm font-medium text-accent sm:inline-flex">Open the Agents page<ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" /></span>
      </Link>
      <div className="mb-4 grid gap-3 sm:grid-cols-2">
        {options.map((o) => (
          <button key={o.id} onClick={() => setMethod(o.id)}
            className={cx('flex items-start gap-3 rounded-xl border p-4 text-left [&_svg]:size-5', method === o.id ? 'border-accent bg-accent/8' : 'border-line bg-panel hover:border-muted')}>
            <span className={method === o.id ? 'text-accent' : 'text-muted'}>{o.icon}</span>
            <span><span className="block font-medium">{o.title}</span><span className="mt-0.5 block text-xs text-muted">{o.desc}</span></span>
          </button>
        ))}
      </div>
      {method === 'snmp' && <AddSnmp p={p} setP={setP} />}
      {method === 'target' && <AddTarget p={p} setP={setP} />}
    </div>
  )
}

function AddSnmp({ p, setP }: { p: Placement; setP: (p: Placement) => void }) {
  const [address, setAddress] = useState('')
  const [cfg, setCfg] = useState<SnmpConfig>(defaultSnmp)
  const [test, setTest] = useState<any>(null)
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const nav = useNavigate()
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card title="SNMP device">
        <div className="space-y-3">
          <Field label="Address (IP or hostname)" required><Input value={address} onChange={(e) => setAddress(e.target.value)} /></Field>
          <SnmpForm value={cfg} onChange={setCfg} />
          <PlacementFields p={p} onChange={setP} />
          <ErrorBox error={err} />
          <div className="flex gap-2">
            <Button loading={busy} disabled={!address} onClick={async () => {
              setBusy(true); setErr(null)
              try { setTest(await post('/snmp/test', { address, snmp: cfg })) } catch (e) { setErr(e) } finally { setBusy(false) }
            }}>Test</Button>
            <Button variant="primary" loading={busy} disabled={!address} onClick={async () => {
              setBusy(true); setErr(null)
              try {
                const h = await post<Host>('/hosts/snmp', { name: p.name, address, tenant_id: p.tenant_id, group_id: p.group_id || null, tags: p.tags, snmp: cfg })
                toast('Device added'); nav(`/hosts/${h.id}`)
              } catch (e) { setErr(errorMessage(e)) } finally { setBusy(false) }
            }}>Add device</Button>
          </div>
        </div>
      </Card>
      <Card title="Test result">
        {!test ? <p className="text-sm text-muted">Test reads the system group, interfaces, profile metrics and LLDP once.</p> : !test.ok ? <ErrorBox error={test.error} /> : (
          <div className="space-y-3 text-sm">
            <Note tone="ok">Reached <b>{test.system?.name || address}</b> in {test.duration_ms?.toFixed(0)} ms using profile <b>{test.profile}</b>.</Note>
            <p className="text-muted">{test.system?.descr}</p>
            <p>{test.interfaces} interfaces · {test.neighbors} LLDP neighbours</p>
            <pre className="mono max-h-64 overflow-auto rounded-xl bg-panel-2 p-3 text-xs">{Object.entries(test.metrics || {}).map(([k, v]) => `${k} = ${(v as number).toFixed(2)}`).join('\n') || 'no profile metrics'}</pre>
            {test.errors?.length > 0 && <Note tone="warn">{test.errors.join('; ')}</Note>}
          </div>
        )}
      </Card>
    </div>
  )
}

function AddTarget({ p, setP }: { p: Placement; setP: (p: Placement) => void }) {
  const [address, setAddress] = useState('')
  const [ping, setPing] = useState(true)
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const nav = useNavigate()
  return (
    <Card title="Agentless target" className="max-w-2xl">
      <div className="space-y-3">
        <Field label="Address" hint="Hostname or IP; add TCP, HTTP, certificate and DNS checks afterwards"><Input value={address} onChange={(e) => setAddress(e.target.value)} /></Field>
        <PlacementFields p={p} onChange={setP} />
        <Toggle checked={ping} onChange={setPing} label="Add a ping check" />
        <ErrorBox error={err} />
        <Button variant="primary" loading={busy} disabled={!address && !p.name} onClick={async () => {
          setBusy(true); setErr(null)
          try {
            const h = await post<Host>('/hosts/target', { name: p.name, address, tenant_id: p.tenant_id, group_id: p.group_id || null, tags: p.tags, add_ping_check: ping })
            nav(`/hosts/${h.id}?tab=checks`)
          } catch (e) { setErr(e) } finally { setBusy(false) }
        }}>Add target</Button>
      </div>
    </Card>
  )
}
