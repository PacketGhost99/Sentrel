import { useQuery } from '@tanstack/react-query'
import { get } from '@/lib/api'
import type { Check, DepParent, Host } from '@/lib/types'
import { Field, Select } from '@/components/ui'

/** Value used by the picker: "" for none, else "host:12" / "check:7". */
export type DepValue = string

export const depValueOf = (p?: DepParent | null): DepValue => (p ? `${p.type}:${p.id}` : '')

/** Fields to merge into a host or check update body. */
export function depBody(v: DepValue): { parent_type: string; parent_id: number } {
  const [t, id] = v.split(':')
  return v ? { parent_type: t, parent_id: Number(id) } : { parent_type: '', parent_id: 0 }
}

/**
 * "Depends on" picker: a host or check in the same tenant. While the parent is
 * down, alerts for this item are still raised but no notifications are sent.
 */
export function DependsOn({ value, onChange, tenantId, self, className }: {
  value: DepValue
  onChange: (v: DepValue) => void
  tenantId: number
  self: { type: 'host' | 'check'; id?: number }
  className?: string
}) {
  const hosts = useQuery({ queryKey: ['hosts', 'all'], queryFn: () => get<Host[]>('/hosts') })
  const checks = useQuery({ queryKey: ['checks', 'all', undefined], queryFn: () => get<Check[]>('/checks') })
  const hs = (hosts.data || []).filter((h) => h.tenant_id === tenantId && !(self.type === 'host' && h.id === self.id))
  const cs = (checks.data || []).filter((c) => c.tenant_id === tenantId && !(self.type === 'check' && c.id === self.id))
  return (
    <Field label="Depends on" className={className}
      info="While the parent is down, alerts for this item are still raised and shown as suppressed, but no notifications are sent. Parents must be in the same tenant; loops are rejected.">
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">Nothing</option>
        {hs.length > 0 && <optgroup label="Hosts">{hs.map((h) => <option key={`h${h.id}`} value={`host:${h.id}`}>{h.name}</option>)}</optgroup>}
        {cs.length > 0 && <optgroup label="Checks">{cs.map((c) => <option key={`c${c.id}`} value={`check:${c.id}`}>{c.name}</option>)}</optgroup>}
      </Select>
    </Field>
  )
}
