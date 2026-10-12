import { useQuery } from '@tanstack/react-query'
import { get } from '@/lib/api'
import { Field, Input, Select, Toggle } from './ui'

export interface SnmpConfig {
  version: '2c' | '3'
  community?: string
  community_set?: boolean
  port: number
  timeout_seconds: number
  retries: number
  username?: string
  security_level?: string
  auth_protocol?: string
  auth_password?: string
  auth_password_set?: boolean
  priv_protocol?: string
  priv_password?: string
  priv_password_set?: boolean
  context_name?: string
  profile: string
  interval_seconds: number
  poll_lldp?: boolean
}

export const defaultSnmp: SnmpConfig = { version: '2c', community: '', port: 161, timeout_seconds: 5, retries: 1, profile: 'auto', interval_seconds: 60, poll_lldp: true, security_level: 'authPriv', auth_protocol: 'SHA256', priv_protocol: 'AES' }

export function SnmpForm({ value, onChange }: { value: SnmpConfig; onChange: (v: SnmpConfig) => void }) {
  const profiles = useQuery({ queryKey: ['snmp-profiles'], queryFn: () => get<{ profiles: { name: string; description: string }[] }>('/snmp/profiles') })
  const set = (patch: Partial<SnmpConfig>) => onChange({ ...value, ...patch })
  const v3 = value.version === '3'
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="SNMP version">
        <Select value={value.version} onChange={(e) => set({ version: e.target.value as '2c' | '3' })}>
          <option value="2c">v2c (community)</option>
          <option value="3">v3 (user-based security)</option>
        </Select>
      </Field>
      <Field label="Device profile" hint="Auto-detect uses sysObjectID / sysDescr">
        <Select value={value.profile} onChange={(e) => set({ profile: e.target.value })}>
          <option value="auto">Auto-detect</option>
          {profiles.data?.profiles.map((p) => <option key={p.name} value={p.name}>{p.name} — {p.description}</option>)}
        </Select>
      </Field>
      {!v3 ? (
        <Field label="Community" hint={value.community_set ? 'Stored; leave blank to keep it' : undefined}>
          <Input type="password" autoComplete="off" value={value.community || ''} onChange={(e) => set({ community: e.target.value })} placeholder={value.community_set ? '••••••' : 'public'} />
        </Field>
      ) : (
        <>
          <Field label="Username"><Input value={value.username || ''} onChange={(e) => set({ username: e.target.value })} /></Field>
          <Field label="Security level">
            <Select value={value.security_level} onChange={(e) => set({ security_level: e.target.value })}>
              <option value="noAuthNoPriv">noAuthNoPriv</option>
              <option value="authNoPriv">authNoPriv</option>
              <option value="authPriv">authPriv</option>
            </Select>
          </Field>
          {value.security_level !== 'noAuthNoPriv' && (
            <>
              <Field label="Auth protocol">
                <Select value={value.auth_protocol} onChange={(e) => set({ auth_protocol: e.target.value })}>
                  {['MD5', 'SHA', 'SHA224', 'SHA256', 'SHA384', 'SHA512'].map((x) => <option key={x}>{x}</option>)}
                </Select>
              </Field>
              <Field label="Auth password" hint={value.auth_password_set ? 'Stored; leave blank to keep it' : undefined}>
                <Input type="password" autoComplete="off" value={value.auth_password || ''} onChange={(e) => set({ auth_password: e.target.value })} />
              </Field>
            </>
          )}
          {value.security_level === 'authPriv' && (
            <>
              <Field label="Privacy protocol">
                <Select value={value.priv_protocol} onChange={(e) => set({ priv_protocol: e.target.value })}>
                  {['DES', 'AES', 'AES192', 'AES256', 'AES192C', 'AES256C'].map((x) => <option key={x}>{x}</option>)}
                </Select>
              </Field>
              <Field label="Privacy password" hint={value.priv_password_set ? 'Stored; leave blank to keep it' : undefined}>
                <Input type="password" autoComplete="off" value={value.priv_password || ''} onChange={(e) => set({ priv_password: e.target.value })} />
              </Field>
            </>
          )}
          <Field label="Context name (optional)"><Input value={value.context_name || ''} onChange={(e) => set({ context_name: e.target.value })} /></Field>
        </>
      )}
      <Field label="Port"><Input type="number" value={value.port} onChange={(e) => set({ port: Number(e.target.value) })} /></Field>
      <Field label="Poll interval (seconds)"><Input type="number" min={15} value={value.interval_seconds} onChange={(e) => set({ interval_seconds: Number(e.target.value) })} /></Field>
      <Field label="Timeout (seconds)"><Input type="number" min={1} value={value.timeout_seconds} onChange={(e) => set({ timeout_seconds: Number(e.target.value) })} /></Field>
      <div className="sm:col-span-2"><Toggle checked={value.poll_lldp !== false} onChange={(b) => set({ poll_lldp: b })} label="Collect LLDP neighbours" /></div>
    </div>
  )
}
