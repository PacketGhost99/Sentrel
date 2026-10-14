import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, ArrowRight, Router, Server, Target } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import type { Host, HostGroup, Tenant } from '@/lib/types'
import { SnmpForm, defaultSnmp, type SnmpConfig } from '@/components/SnmpForm'
import { useTenantFilter } from '@/components/Layout'
import { Button, Card, ErrorBox, Field, Input, Note, PageHeader, Select, TagInput, useToast } from '@/components/ui'

export interface Placement { tenant_id: number; group_id: number; tags: string[]; name: string }

export function PlacementFields({ p, onChange, showName = true }: { p: Placement; onChange: (p: Placement) => void; showName?: boolean }) {
  const { me } = useAuth()
  const provider = me?.user.tenant_id === 0
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants'), enabled: provider })
  const groups = useQuery({ queryKey: ['groups', p.tenant_id], queryFn: () => get<HostGroup[]>('/groups', { tenant_id: provider ? p.tenant_id : undefined }) })
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {showName && <Field label="Display name" hint="Optional"><Input value={p.name} onChange={(e) => onChange({ ...p, name: e.target.value })} /></Field>}
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
  const { tenant } = useTenantFilter()
  const [p, setP] = useState<Placement>({ tenant_id: tenant ?? 0, group_id: 0, tags: [], name: '' })
  return (
    <div>
      <PageHeader back={<Link to="/hosts" className="mb-1 inline-flex items-center gap-1 text-xs text-muted hover:text-fg"><ArrowLeft className="size-3" />Hosts</Link>} title="Add host" />
      <Link to="/agents" className="group mb-4 flex items-center gap-4 rounded-2xl border border-accent/40 bg-accent/8 p-5 shadow-card hover:border-accent">
        <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-accent/15 text-accent"><Server className="size-6" /></span>
        <span className="min-w-0 flex-1">
          <span className="block font-semibold">Install the Sentrel agent</span>
          <span className="mt-0.5 block text-sm text-muted">Linux and Windows. One-line command or .deb, .rpm, .msi package, with full metrics and remote actions.</span>
        </span>
        <span className="hidden items-center gap-1 text-sm font-medium text-accent sm:inline-flex">Open the Agents page<ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" /></span>
      </Link>
      <div className="mb-4 grid gap-3 sm:grid-cols-2">
        <div className="flex items-start gap-3 rounded-xl border border-accent bg-accent/8 p-4">
          <Router className="size-5 shrink-0 text-accent" />
          <span><span className="block font-medium">SNMP device</span><span className="mt-0.5 block text-xs text-muted">Switches, routers, firewalls and NAS. Configure SNMP below.</span></span>
        </div>
        <Link to="/checks?new=1" className="group flex items-start gap-3 rounded-xl border border-line bg-panel p-4 hover:border-accent">
          <Target className="size-5 shrink-0 text-accent" />
          <span className="flex-1"><span className="block font-medium">Agentless checks</span><span className="mt-0.5 block text-xs text-muted">Monitor a website, IP, port, certificate or DNS record using the full check editor.</span><span className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-accent">Add a check<ArrowRight className="size-3.5" /></span></span>
        </Link>
      </div>
      <AddSnmp p={p} setP={setP} />

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
      <Card title="SNMP device" className="h-full">
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
      <Card title="Test result" className="h-full" info="Reads the system group, interfaces, profile metrics and LLDP once.">
        {!test ? <p className="text-sm text-muted">Run a test to see the result here.</p> : !test.ok ? <ErrorBox error={test.error} /> : (
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
