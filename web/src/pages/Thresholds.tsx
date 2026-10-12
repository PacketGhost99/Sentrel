import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { BellOff, Plus, RotateCcw, Save, X } from 'lucide-react'
import { del, errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import type { HostDetail, HostGroup, MergedRule, MetricDef, Tenant, ThresholdRule } from '@/lib/types'
import { Badge, Button, Card, Input, Loading, Note, PageHeader, Select, Table, Td, Th, Toggle, cx, useToast } from '@/components/ui'

type Scope = { scope: 'global' | 'group' | 'host'; scope_id: number; tenant_id: number }

export function Thresholds() {
  const { me, can } = useAuth()
  const provider = me?.user.tenant_id === 0
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => get<HostGroup[]>('/groups') })
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants'), enabled: provider })
  const [sel, setSel] = useState('global:0:' + (me?.user.tenant_id ?? 0))
  const [scope, id, tenant] = sel.split(':')
  const s: Scope = { scope: scope as Scope['scope'], scope_id: Number(id), tenant_id: Number(tenant) }
  return (
    <div>
      <PageHeader title="Thresholds" subtitle="Thresholds inherit from provider to tenant, group and host. The most specific rule wins, and label rules (for example a single mount) take precedence over wildcard rules at the same level." />
      <Card bodyClass="p-0">
        <div className="flex flex-wrap items-center gap-3 border-b border-line/70 p-4">
          <span className="text-sm text-muted">Editing</span>
          <div className="w-72">
            <Select value={sel} onChange={(e) => setSel(e.target.value)}>
              {provider && <option value="global:0:0">Provider defaults (all tenants)</option>}
              {provider ? tenants.data?.filter((t) => t.id !== 0).map((t) => <option key={t.id} value={`global:0:${t.id}`}>Tenant defaults: {t.name}</option>)
                : <option value={`global:0:${me?.user.tenant_id}`}>Tenant defaults</option>}
              {groups.data?.map((g) => <option key={g.id} value={`group:${g.id}:${g.tenant_id}`}>Group: {g.name}</option>)}
            </Select>
          </div>
          <span className="text-xs text-muted">Per-host overrides are edited on each host's Thresholds tab.</span>
        </div>
        <ThresholdTable scope={s} editable={can('monitoring')} />
      </Card>
    </div>
  )
}

function fmt(v: number | null | undefined) {
  return v === null || v === undefined ? '' : String(v)
}

export function ThresholdTable({ scope, editable, inherited }: { scope: Scope; editable: boolean; inherited?: Record<string, ThresholdRule | undefined> }) {
  const catalog = useQuery({ queryKey: ['catalog'], queryFn: () => get<MetricDef[]>('/thresholds/catalog') })
  const rules = useQuery({ queryKey: ['thresholds', scope], queryFn: () => get<ThresholdRule[]>('/thresholds', { scope: scope.scope, scope_id: scope.scope_id, tenant_id: scope.tenant_id }) })
  const qc = useQueryClient()
  const [adding, setAdding] = useState<string | null>(null)
  if (catalog.isLoading || rules.isLoading) return <Loading />
  const mine = (rules.data || []).filter((r) => r.tenant_id === scope.tenant_id || scope.scope !== 'global')
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['thresholds'] })
    qc.invalidateQueries({ queryKey: ['host-thresholds'] })
  }
  return (
    <Table>
      <thead><tr><Th>Metric</Th><Th className="w-40">Label match</Th><Th className="w-28">Warning</Th><Th className="w-28">Critical</Th><Th className="w-24" title="Consecutive evaluations before firing">For</Th><Th className="w-20">Enabled</Th><Th className="w-24" title="Send emails and webhooks when this rule fires. Alerts always show in Sentrel.">Notify</Th><Th className="w-40" /></tr></thead>
      <tbody>
        {catalog.data!.map((m) => {
          const base = mine.find((r) => r.metric === m.key && !r.label_match)
          const labelled = mine.filter((r) => r.metric === m.key && r.label_match)
          return [
            <RuleRow key={m.key} metric={m} scope={scope} rule={base} inherited={inherited?.[m.key]} editable={editable} onSaved={refresh}
              onAddLabel={m.label_key && editable ? () => setAdding(m.key) : undefined} />,
            ...labelled.map((r) => <RuleRow key={r.id} metric={m} scope={scope} rule={r} label editable={editable} onSaved={refresh} />),
            adding === m.key ? <RuleRow key={m.key + '-new'} metric={m} scope={scope} label isNew editable onSaved={() => { setAdding(null); refresh() }} onCancel={() => setAdding(null)} /> : null,
          ]
        })}
      </tbody>
    </Table>
  )
}

function RuleRow({ metric, scope, rule, inherited, label, isNew, editable, onSaved, onAddLabel, onCancel }: {
  metric: MetricDef; scope: Scope; rule?: ThresholdRule; inherited?: ThresholdRule; label?: boolean; isNew?: boolean; editable: boolean
  onSaved: () => void; onAddLabel?: () => void; onCancel?: () => void
}) {
  const toast = useToast()
  const src = rule || inherited
  const [warn, setWarn] = useState(fmt(src?.warn))
  const [crit, setCrit] = useState(fmt(src?.crit))
  const [forC, setForC] = useState(String(src?.for_count ?? 2))
  const [enabled, setEnabled] = useState(src?.enabled ?? true)
  const [notify, setNotify] = useState(src?.notify ?? true)
  const [lm, setLm] = useState(rule?.label_match || '')
  const [busy, setBusy] = useState(false)
  const dirty = isNew || fmt(rule?.warn) !== warn || fmt(rule?.crit) !== crit || String(rule?.for_count ?? 2) !== forC || (rule?.enabled ?? true) !== enabled || (rule?.notify ?? true) !== notify || !rule
  const changed = isNew || !rule ? warn !== fmt(inherited?.warn) || crit !== fmt(inherited?.crit) || forC !== String(inherited?.for_count ?? 2) || enabled !== (inherited?.enabled ?? true) || notify !== (inherited?.notify ?? true) : dirty
  const save = async () => {
    setBusy(true)
    try {
      await post('/thresholds', { ...scope, metric: metric.key, label_match: label ? lm : '', warn: warn === '' ? null : Number(warn), crit: crit === '' ? null : Number(crit), for_count: Number(forC) || 1, enabled, notify })
      toast(`${metric.name} saved`)
      onSaved()
    } catch (e) {
      toast(errorMessage(e), 'crit')
    } finally {
      setBusy(false)
    }
  }
  const remove = async () => {
    if (!rule) return
    try { await del(`/thresholds/${rule.id}`); toast('Override removed'); onSaved() } catch (e) { toast(errorMessage(e), 'crit') }
  }
  const unit = metric.unit ? <span className="text-xs text-muted">{metric.unit}</span> : null
  const dis = !editable
  return (
    <tr className={cx(label && 'bg-panel-2/40', !rule && !isNew && inherited && 'text-muted')}>
      <Td>
        {label ? <span className="pl-4 text-xs text-muted">↳ {metric.label_key} override</span> : (
          <div>
            <span className="font-medium text-fg">{metric.name}</span>
            {metric.lower_is_worse && <Badge className="ml-1">lower is worse</Badge>}
            {!rule && inherited && <Badge className="ml-1">inherited</Badge>}
            {metric.description && <div className="text-xs text-muted">{metric.description}</div>}
          </div>
        )}
      </Td>
      <Td>{label ? <Input className="h-7 mono text-xs" disabled={dis || !isNew} value={lm} onChange={(e) => setLm(e.target.value)} placeholder={metric.label_key === 'mount' ? '/var or /mnt/*' : metric.label_key} /> : <span className="text-xs text-muted">{metric.label_key ? `all ${metric.label_key}s` : '–'}</span>}</Td>
      <Td><div className="flex items-center gap-1"><Input className="h-7 w-20 text-xs" disabled={dis} value={warn} onChange={(e) => setWarn(e.target.value)} placeholder="off" />{unit}</div></Td>
      <Td><div className="flex items-center gap-1"><Input className="h-7 w-20 text-xs" disabled={dis} value={crit} onChange={(e) => setCrit(e.target.value)} placeholder="off" />{unit}</div></Td>
      <Td><Input className="h-7 w-16 text-xs" type="number" min={1} disabled={dis} value={forC} onChange={(e) => setForC(e.target.value)} /></Td>
      <Td><Toggle checked={enabled} onChange={setEnabled} disabled={dis} /></Td>
      <Td><div className="flex items-center gap-1.5" title={notify ? 'Notifications on' : 'Muted: alerts show in Sentrel but no emails or webhooks are sent'}><Toggle checked={notify} onChange={setNotify} disabled={dis} />{!notify && <BellOff className="size-3.5 text-muted" />}</div></Td>
      <Td className="whitespace-nowrap text-right">
        {editable && changed && <Button size="sm" variant="primary" loading={busy} icon={<Save className="size-3.5" />} onClick={save} disabled={label && !lm}>Save</Button>}
        {editable && rule && !(scope.scope === 'global' && scope.tenant_id === 0 && !label) && <Button size="sm" variant="ghost" title="Remove override" icon={label ? <X className="size-3.5" /> : <RotateCcw className="size-3.5" />} onClick={remove} />}
        {onCancel && <Button size="sm" variant="ghost" icon={<X className="size-3.5" />} onClick={onCancel} />}
        {onAddLabel && <Button size="sm" variant="ghost" title={`Add a rule for one ${metric.label_key}`} icon={<Plus className="size-3.5" />} onClick={onAddLabel} />}
      </Td>
    </tr>
  )
}

/** Merged view on a host: shows the effective rule and where it comes from. */
export function HostThresholds({ host }: { host: HostDetail }) {
  const { can } = useAuth()
  const merged = useQuery({ queryKey: ['host-thresholds', host.id], queryFn: () => get<MergedRule[]>(`/hosts/${host.id}/thresholds`) })
  const [edit, setEdit] = useState(false)
  if (merged.isLoading) return <Loading />
  const originTone = { host: 'accent', group: 'info', tenant: 'warn', provider: 'muted' } as const
  const inherited: Record<string, ThresholdRule | undefined> = {}
  merged.data?.forEach((m) => {
    const parent = (m.chain || []).find((c) => c.origin !== 'host' && !c.rule.label_match)
    inherited[m.metric.key] = parent?.rule
  })
  return (
    <div className="space-y-4">
      <Card title="Effective thresholds" subtitle="What applies to this host right now" bodyClass="p-0"
        actions={can('monitoring') && <Button size="sm" onClick={() => setEdit(!edit)}>{edit ? 'Done' : 'Edit host overrides'}</Button>}>
        {edit ? (
          <>
            <div className="p-4"><Note>Values shown come from the inherited rule; saving creates an override for this host only. Remove an override to inherit again.</Note></div>
            <ThresholdTable scope={{ scope: 'host', scope_id: host.id, tenant_id: host.tenant_id }} editable inherited={inherited} />
          </>
        ) : (
          <Table>
            <thead><tr><Th>Metric</Th><Th>Warning</Th><Th>Critical</Th><Th>For</Th><Th>From</Th><Th className="hidden md:table-cell">Label rules</Th></tr></thead>
            <tbody>
              {merged.data!.map((m) => {
                const e = m.effective
                const off = !e || !e.rule.enabled
                return (
                  <tr key={m.metric.key} className={off ? 'opacity-60' : ''}>
                    <Td className="font-medium">{m.metric.name}</Td>
                    <Td className="tabular-nums">{off ? 'off' : e?.rule.warn ?? '–'} {!off && e?.rule.warn != null && <span className="text-xs text-muted">{m.metric.unit}</span>}</Td>
                    <Td className="tabular-nums">{off ? 'off' : e?.rule.crit ?? '–'} {!off && e?.rule.crit != null && <span className="text-xs text-muted">{m.metric.unit}</span>}</Td>
                    <Td className="text-xs">{e ? `${e.rule.for_count}×` : '–'}</Td>
                    <Td>{e && <Badge tone={originTone[e.origin]}>{e.origin}</Badge>}</Td>
                    <Td className="hidden text-xs md:table-cell">{(m.label_overrides || []).map((o) => <Badge key={o.rule.id} className="mr-1" title={`warn ${o.rule.warn ?? '–'} / crit ${o.rule.crit ?? '–'}`}>{o.rule.label_match} ({o.origin})</Badge>)}</Td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  )
}
