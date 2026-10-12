import { useEffect, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, Plus, RefreshCw, Save, Send, X } from 'lucide-react'
import { errorMessage, get, post, put } from '@/lib/api'
import { applyBranding, useAuth } from '@/lib/auth'
import { bytes, dateTime, duration, timezones } from '@/lib/format'
import type { Branding, Tenant } from '@/lib/types'
import { Badge, Button, Card, Checkbox, CodeBlock, CopyButton, ErrorBox, Field, Input, KV, Loading, Note, PageHeader, Select, TagInput, Tabs, Textarea, Toggle, useToast } from '@/components/ui'
import { roleInfo } from './Users'

type Section = 'general' | 'tls' | 'smtp' | 'directory' | 'sso' | 'security' | 'retention' | 'agent_defaults' | 'alerting' | 'vuln' | 'snmp' | 'server'

export function Settings() {
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Section) || 'general'
  const s = useQuery({ queryKey: ['settings'], queryFn: () => get<Record<string, any>>('/settings') })
  return (
    <div>
      <PageHeader title="Settings" subtitle="Server-wide configuration. Changes apply immediately and are recorded in the audit log." />
      <Tabs<Section> value={tab} onChange={(t) => setParams({ tab: t }, { replace: true })} className="mb-4" tabs={[
        { id: 'general', label: 'General' }, { id: 'tls', label: 'TLS & access' }, { id: 'smtp', label: 'Email' }, { id: 'directory', label: 'LDAP & RADIUS' }, { id: 'sso', label: 'Single sign-on' },
        { id: 'security', label: 'Security' }, { id: 'retention', label: 'Data retention' }, { id: 'agent_defaults', label: 'Agents' }, { id: 'alerting', label: 'Alerting' },
        { id: 'vuln', label: 'Vulnerabilities' }, { id: 'snmp', label: 'SNMP profiles' }, { id: 'server', label: 'Server & backup' },
      ]} />
      {s.isLoading ? <Loading /> : s.error ? <ErrorBox error={s.error} /> : (
        <>
          {tab === 'general' && <General data={s.data!.general} />}
          {tab === 'tls' && <TLS data={s.data!.tls} active={s.data!.active_tls_mode} fp={s.data!.ca_fingerprint} />}
          {tab === 'smtp' && <SMTP data={s.data!.smtp} />}
          {tab === 'directory' && <Directory ldap={s.data!.ldap} radius={s.data!.radius} />}
          {tab === 'sso' && <SSO oidc={s.data!.oidc} proxy={s.data!.proxy_auth} serverUrl={s.data!.general?.server_url} />}
          {tab === 'security' && <Security data={s.data!.security} />}
          {tab === 'retention' && <Retention data={s.data!.retention} />}
          {tab === 'agent_defaults' && <Agents data={s.data!.agent_defaults} />}
          {tab === 'alerting' && <Alerting data={s.data!.alerting} />}
          {tab === 'vuln' && <Vuln data={s.data!.vuln} />}
          {tab === 'snmp' && <SnmpProfiles />}
          {tab === 'server' && <Server />}
        </>
      )}
    </div>
  )
}

function useSection<T extends Record<string, any>>(section: string, data: T) {
  const [v, setV] = useState<T>(data)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const toast = useToast()
  const qc = useQueryClient()
  useEffect(() => setV(data), [data])
  const set = (patch: Partial<T>) => setV({ ...v, ...patch })
  const save = async (override?: T) => {
    setBusy(true)
    setErr(null)
    try {
      const r = await put<{ warning?: string }>(`/settings/${section}`, override || v)
      toast(r?.warning || 'Settings saved', r?.warning ? 'info' : 'ok')
      qc.invalidateQueries({ queryKey: ['settings'] })
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }
  return { v, set, save, busy, err }
}

function SaveBar({ busy, err, onSave, extra }: { busy: boolean; err: unknown; onSave: () => void; extra?: ReactNode }) {
  return (
    <div className="mt-4 space-y-3">
      <ErrorBox error={err} />
      <div className="flex flex-wrap gap-2"><Button variant="primary" loading={busy} icon={<Save className="size-4" />} onClick={onSave}>Save</Button>{extra}</div>
    </div>
  )
}

const num = (s: string) => (s === '' ? 0 : Number(s))
const secretHint = (set?: boolean) => (set ? 'Stored; leave blank to keep it' : undefined)

function General({ data }: { data: any }) {
  const { v, set, save, busy, err } = useSection('general', data)
  return (
    <Card title="General" className="max-w-3xl">
      <div className="space-y-3">
        <Field label="Public server URL" hint="Used in agent install commands, notification links and passkeys (the RP ID is this hostname). Example: https://monitor.example.com">
          <Input value={v.server_url} onChange={(e) => set({ server_url: e.target.value })} />
        </Field>
        <Field label="Timezone" hint="For maintenance windows and scan schedules">
          <Select value={v.timezone} onChange={(e) => set({ timezone: e.target.value })}>{timezones().map((z) => <option key={z}>{z}</option>)}</Select>
        </Field>
      </div>
      <SaveBar busy={busy} err={err} onSave={() => save()} />
    </Card>
  )
}

function TLS({ data, active, fp }: { data: any; active: string; fp: string }) {
  const { v, set, save, busy, err } = useSection('tls', data)
  return (
    <div className="grid max-w-5xl gap-4 xl:grid-cols-2">
      <Card title="TLS & listener" subtitle={`Currently serving: ${active}`}>
        <div className="space-y-3">
          <Field label="Mode">
            <Select value={v.mode} onChange={(e) => set({ mode: e.target.value })}>
              <option value="http">Plain HTTP (behind a reverse proxy that terminates TLS)</option>
              <option value="selfsigned">Self-signed (internal CA)</option>
              <option value="acme">Let's Encrypt (automatic)</option>
            </Select>
          </Field>
          <Field label="Listen address" hint="host:port; ACME mode always uses :443 (+ :80 for challenges). Overridden by --listen."><Input value={v.listen} onChange={(e) => set({ listen: e.target.value })} /></Field>
          {v.mode !== 'http' && <>
            <Field label="Hostname"><Input value={v.hostname} onChange={(e) => set({ hostname: e.target.value })} /></Field>
            <Field label="Additional names" hint="Extra DNS names / IPs for the certificate"><TagInput value={v.extra_names || []} onChange={(x) => set({ extra_names: x })} /></Field>
          </>}
          {v.mode === 'acme' && <>
            <Field label="Contact email"><Input value={v.acme_email} onChange={(e) => set({ acme_email: e.target.value })} /></Field>
            <Field label="ACME directory (optional)" hint="Leave blank for Let's Encrypt production; use the staging URL to test"><Input value={v.acme_directory} onChange={(e) => set({ acme_directory: e.target.value })} /></Field>
          </>}
          <Toggle checked={!!v.trust_proxy} onChange={(x) => set({ trust_proxy: x })} label="Trust reverse proxy headers" hint="Use X-Forwarded-For and X-Forwarded-Proto for client IPs and HTTPS detection. Only enable when the server is not directly reachable." />
          {v.trust_proxy && <Field label="Trusted proxies" hint="IP addresses or CIDR ranges whose forwarding headers are believed (press Enter to add). Empty means loopback only. X-Forwarded-For is read right to left, skipping these."><TagInput value={v.trusted_proxies || []} onChange={(x) => set({ trusted_proxies: x })} placeholder="127.0.0.1 or 10.0.0.0/8" /></Field>}
          <Note tone="warn">Switching mode restarts the listener. If a mode fails to start, the server falls back to plain HTTP so you are never locked out; start with <code className="mono">--force-http</code> to recover manually.</Note>
        </div>
        <SaveBar busy={busy} err={err} onSave={() => save()} />
      </Card>
      <Card title="Reverse proxy">
        <div className="space-y-3 text-sm">
          <p>In plain HTTP mode a reverse proxy terminates TLS. This Caddy example covers the UI, API and agent gRPC:</p>
          <CodeBlock code={`monitor.example.com {\n  reverse_proxy h2c://127.0.0.1:8080\n}`} />
          <p>Any proxy works if it forwards HTTP/2 (gRPC) to the listen address. Agents authenticate with signed requests, so mTLS is not required through the proxy.</p>
          <KV items={[['Internal CA fingerprint', <span key="f" className="mono break-all text-xs">{fp}</span>]]} />
        </div>
      </Card>
    </div>
  )
}

function SMTP({ data }: { data: any }) {
  const { v, set, save, busy, err } = useSection('smtp', data)
  const [to, setTo] = useState('')
  const toast = useToast()
  return (
    <Card title="Email (SMTP relay)" subtitle="Used by email notification channels. Gmail, Microsoft 365, Resend, SES or your own relay." className="max-w-3xl">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Host"><Input value={v.host} onChange={(e) => set({ host: e.target.value })} placeholder="smtp.gmail.com" /></Field>
        <Field label="Port"><Input type="number" value={v.port} onChange={(e) => set({ port: num(e.target.value) })} /></Field>
        <Field label="Security"><Select value={v.tls_mode} onChange={(e) => set({ tls_mode: e.target.value })}><option value="starttls">STARTTLS (587)</option><option value="tls">Implicit TLS (465)</option><option value="none">None</option></Select></Field>
        <Field label="From address"><Input value={v.from} onChange={(e) => set({ from: e.target.value })} placeholder="Sentrel <alerts@example.com>" /></Field>
        <Field label="Username"><Input value={v.username} onChange={(e) => set({ username: e.target.value })} /></Field>
        <Field label="Password" hint={secretHint(v.password_set)}><Input type="password" autoComplete="new-password" value={v.password || ''} onChange={(e) => set({ password: e.target.value })} /></Field>
        <div className="sm:col-span-2"><Toggle checked={!!v.skip_verify} onChange={(x) => set({ skip_verify: x })} label="Skip certificate verification" /></div>
      </div>
      <SaveBar busy={busy} err={err} onSave={() => save({ ...v, enabled: true })} extra={<>
        <Input value={to} onChange={(e) => setTo(e.target.value)} placeholder="send test to…" className="w-56" />
        <Button icon={<Send className="size-4" />} disabled={!to} onClick={async () => {
          try { const r = await post<any>('/settings/smtp/test', { to }); toast(r.ok ? 'Test email sent' : `Failed: ${r.error}`, r.ok ? 'ok' : 'crit') } catch (e) { toast(errorMessage(e), 'crit') }
        }}>Test (save first)</Button>
      </>} />
    </Card>
  )
}

function Directory({ ldap, radius }: { ldap: any; radius: any }) {
  const L = useSection('ldap', ldap)
  const R = useSection('radius', radius)
  const toast = useToast()
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants') })
  const [tu, setTu] = useState({ username: '', password: '' })
  const [ru, setRu] = useState({ username: '', password: '' })
  const maps: any[] = L.v.role_mapping || []
  const roles = (t: number) => (t === 0 ? ['admin', 'operator', 'viewer'] : ['tenant_admin', 'tenant_viewer'])
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card title="LDAP / Active Directory">
        <div className="space-y-3">
          <Toggle checked={!!L.v.enabled} onChange={(x) => L.set({ enabled: x })} label="Enable LDAP sign-in" />
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="URL" className="sm:col-span-2" hint="ldaps://dc1.example.com:636 or ldap://…:389"><Input value={L.v.url} onChange={(e) => L.set({ url: e.target.value })} /></Field>
            <Field label="Bind DN"><Input value={L.v.bind_dn} onChange={(e) => L.set({ bind_dn: e.target.value })} placeholder="CN=svc-sentrel,OU=Service,DC=example,DC=com" /></Field>
            <Field label="Bind password" hint={secretHint(L.v.bind_password_set)}><Input type="password" autoComplete="new-password" value={L.v.bind_password || ''} onChange={(e) => L.set({ bind_password: e.target.value })} /></Field>
            <Field label="Base DN"><Input value={L.v.base_dn} onChange={(e) => L.set({ base_dn: e.target.value })} placeholder="DC=example,DC=com" /></Field>
            <Field label="User filter" hint="%s is the username. AD: (&(objectClass=user)(sAMAccountName=%s))"><Input value={L.v.user_filter} onChange={(e) => L.set({ user_filter: e.target.value })} /></Field>
            <Field label="Email attribute"><Input value={L.v.email_attr} onChange={(e) => L.set({ email_attr: e.target.value })} /></Field>
            <Field label="Name attribute"><Input value={L.v.name_attr} onChange={(e) => L.set({ name_attr: e.target.value })} /></Field>
            <Field label="Group attribute"><Input value={L.v.group_attr} onChange={(e) => L.set({ group_attr: e.target.value })} /></Field>
            <div className="space-y-2 pt-5"><Toggle checked={!!L.v.start_tls} onChange={(x) => L.set({ start_tls: x })} label="StartTLS" /><Toggle checked={!!L.v.skip_verify} onChange={(x) => L.set({ skip_verify: x })} label="Skip certificate verification" /></div>
          </div>
          <Field label="Group → role mapping" hint="The most privileged matching mapping wins. Users matching no mapping get the default role, or are refused if none.">
            <div className="space-y-2">
              {maps.map((m, i) => (
                <div key={i} className="flex flex-wrap items-center gap-2">
                  <Input className="min-w-56 flex-1" value={m.group} placeholder="CN=Monitoring Admins,OU=Groups,DC=…" onChange={(e) => { const a = [...maps]; a[i] = { ...m, group: e.target.value }; L.set({ role_mapping: a }) }} />
                  <Select value={m.tenant_id} onChange={(e) => { const a = [...maps]; const t = Number(e.target.value); a[i] = { ...m, tenant_id: t, role: roles(t)[0] }; L.set({ role_mapping: a }) }}>
                    {tenants.data?.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
                  </Select>
                  <Select value={m.role} onChange={(e) => { const a = [...maps]; a[i] = { ...m, role: e.target.value }; L.set({ role_mapping: a }) }}>
                    {roles(m.tenant_id).map((r) => <option key={r} value={r}>{roleInfo[r].label}</option>)}
                  </Select>
                  <Button size="sm" variant="ghost" icon={<X className="size-3.5" />} onClick={() => L.set({ role_mapping: maps.filter((_, j) => j !== i) })} />
                </div>
              ))}
              <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => L.set({ role_mapping: [...maps, { group: '', role: 'viewer', tenant_id: 0 }] })}>Add mapping</Button>
            </div>
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Default role (no mapping matched)"><Select value={L.v.default_role} onChange={(e) => L.set({ default_role: e.target.value })}><option value="">Refuse sign-in</option>{roles(L.v.default_tenant || 0).map((r) => <option key={r} value={r}>{roleInfo[r].label}</option>)}</Select></Field>
            <Field label="Default tenant"><Select value={L.v.default_tenant || 0} onChange={(e) => L.set({ default_tenant: Number(e.target.value), default_role: '' })}>{tenants.data?.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</Select></Field>
          </div>
        </div>
        <SaveBar busy={L.busy} err={L.err} onSave={() => L.save()} extra={<>
          <Input className="w-32" placeholder="test user" value={tu.username} onChange={(e) => setTu({ ...tu, username: e.target.value })} />
          <Input className="w-32" type="password" placeholder="password" value={tu.password} onChange={(e) => setTu({ ...tu, password: e.target.value })} />
          <Button onClick={async () => { try { const r = await post<any>('/settings/ldap/test', tu); toast(r.ok ? r.message : r.error, r.ok ? 'ok' : 'crit') } catch (e) { toast(errorMessage(e), 'crit') } }}>Test (save first)</Button>
        </>} />
      </Card>
      <Card title="RADIUS">
        <div className="space-y-3">
          <Toggle checked={!!R.v.enabled} onChange={(x) => R.set({ enabled: x })} label="Enable RADIUS sign-in" />
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Server" hint="host:port (default port 1812)"><Input value={R.v.server} onChange={(e) => R.set({ server: e.target.value })} /></Field>
            <Field label="Shared secret" hint={secretHint(R.v.secret_set)}><Input type="password" autoComplete="new-password" value={R.v.secret || ''} onChange={(e) => R.set({ secret: e.target.value })} /></Field>
            <Field label="NAS identifier"><Input value={R.v.nas_identifier} onChange={(e) => R.set({ nas_identifier: e.target.value })} /></Field>
            <Field label="Timeout (s)"><Input type="number" value={R.v.timeout_seconds} onChange={(e) => R.set({ timeout_seconds: num(e.target.value) })} /></Field>
            <Field label="Role attribute" hint="Filter-Id or Class may carry “sentrel:admin”"><Select value={R.v.role_attribute} onChange={(e) => R.set({ role_attribute: e.target.value })}><option>Filter-Id</option><option>Class</option></Select></Field>
            <Field label="Default tenant"><Select value={R.v.default_tenant || 0} onChange={(e) => R.set({ default_tenant: Number(e.target.value), default_role: '' })}>{tenants.data?.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</Select></Field>
            <Field label="Default role"><Select value={R.v.default_role} onChange={(e) => R.set({ default_role: e.target.value })}><option value="">Refuse unless attribute sets a role</option>{roles(R.v.default_tenant || 0).map((r) => <option key={r} value={r}>{roleInfo[r].label}</option>)}</Select></Field>
          </div>
        </div>
        <SaveBar busy={R.busy} err={R.err} onSave={() => R.save()} extra={<>
          <Input className="w-32" placeholder="test user" value={ru.username} onChange={(e) => setRu({ ...ru, username: e.target.value })} />
          <Input className="w-32" type="password" placeholder="password" value={ru.password} onChange={(e) => setRu({ ...ru, password: e.target.value })} />
          <Button onClick={async () => { try { const r = await post<any>('/settings/radius/test', ru); toast(r.ok ? `${r.message}${r.role_hint ? ` (role hint ${r.role_hint})` : ''}` : r.error, r.ok ? 'ok' : 'crit') } catch (e) { toast(errorMessage(e), 'crit') } }}>Test (save first)</Button>
        </>} />
      </Card>
    </div>
  )
}

function Security({ data }: { data: any }) {
  const { v, set, save, busy, err } = useSection('security', data)
  return (
    <Card title="Security" className="max-w-3xl">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Require two-factor authentication" className="sm:col-span-2">
          <Select value={v.require_2fa} onChange={(e) => set({ require_2fa: e.target.value })}>
            <option value="none">Optional</option><option value="admins">Required for administrators and tenant admins</option><option value="all">Required for everyone</option>
          </Select>
        </Field>
        <Field label="Session length (hours)" hint="Sliding: extends while in use"><Input type="number" value={v.session_hours} onChange={(e) => set({ session_hours: num(e.target.value) })} /></Field>
        <Field label="Minimum password length"><Input type="number" value={v.password_min_length} onChange={(e) => set({ password_min_length: num(e.target.value) })} /></Field>
        <Field label="Lock out after failed logins"><Input type="number" value={v.lockout_attempts} onChange={(e) => set({ lockout_attempts: num(e.target.value) })} /></Field>
        <Field label="Lockout window (minutes)"><Input type="number" value={v.lockout_minutes} onChange={(e) => set({ lockout_minutes: num(e.target.value) })} /></Field>
        <div className="sm:col-span-2"><Toggle checked={!!v.metrics_auth} onChange={(x) => set({ metrics_auth: x })} label="Require an API key for /metrics" hint="Off: anyone who can reach the server can scrape the Prometheus endpoint" /></div>
        <div className="sm:col-span-2"><Toggle checked={!!v.allow_private_targets_for_tenants} onChange={(x) => set({ allow_private_targets_for_tenants: x })} label="Allow tenants to monitor private addresses" hint="Off: tenant checks and notification channels can only reach public addresses (loopback, private ranges and link-local such as cloud metadata are blocked). Provider checks are never restricted." /></div>
      </div>
      <SaveBar busy={busy} err={err} onSave={() => save()} />
    </Card>
  )
}

function Retention({ data }: { data: any }) {
  const { v, set, save, busy, err } = useSection('retention', data)
  return (
    <Card title="Data retention" subtitle="Per-host overrides live in each host's settings." className="max-w-3xl">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Metrics (days)" hint="Time-series data; default 31"><Input type="number" value={v.days} onChange={(e) => set({ days: num(e.target.value) })} /></Field>
        <Field label="Warn when the data disk is above (%)"><Input type="number" value={v.disk_warn_percent} onChange={(e) => set({ disk_warn_percent: num(e.target.value) })} /></Field>
        <Field label="Alert history (days)"><Input type="number" value={v.alert_history_days} onChange={(e) => set({ alert_history_days: num(e.target.value) })} /></Field>
        <Field label="Audit log (days)"><Input type="number" value={v.audit_days} onChange={(e) => set({ audit_days: num(e.target.value) })} /></Field>
        <Field label="Job history (days)"><Input type="number" value={v.job_history_days} onChange={(e) => set({ job_history_days: num(e.target.value) })} /></Field>
        <Field label="Failed-login events (days)"><Input type="number" value={v.security_event_days} onChange={(e) => set({ security_event_days: num(e.target.value) })} /></Field>
      </div>
      <Note className="mt-3">Per-host storage is computed nightly; the “Metric storage per host” threshold raises a warning for hosts above the limit.</Note>
      <SaveBar busy={busy} err={err} onSave={() => save()} />
    </Card>
  )
}

function Agents({ data }: { data: any }) {
  const { v, set, save, busy, err } = useSection('agent_defaults', data)
  return (
    <div className="grid max-w-5xl gap-4 xl:grid-cols-2">
      <Card title="Collection defaults" subtitle="Pushed to every agent immediately; hosts can override.">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Interval (s)"><Input type="number" value={v.interval_seconds} onChange={(e) => set({ interval_seconds: num(e.target.value) })} /></Field>
          <Field label="Package inventory (s)"><Input type="number" value={v.inventory_interval_seconds} onChange={(e) => set({ inventory_interval_seconds: num(e.target.value) })} /></Field>
          <Field label="Top processes"><Input type="number" value={v.top_processes} onChange={(e) => set({ top_processes: num(e.target.value) })} /></Field>
          <Field label="Security report interval (s)"><Input type="number" value={v.security_interval_seconds} onChange={(e) => set({ security_interval_seconds: num(e.target.value) })} /></Field>
          <Toggle checked={!!v.collect_services} onChange={(x) => set({ collect_services: x })} label="All services" />
          <Toggle checked={!!v.collect_ports} onChange={(x) => set({ collect_ports: x })} label="Listening ports" />
          <div className="sm:col-span-2"><Toggle checked={!!v.auto_update} onChange={(x) => set({ auto_update: x })} label="Update agents automatically" hint="When a host's agent is older than the agent bundled with this server, a signed update job is queued (it self-tests and rolls back on failure)" /></div>
          <Toggle checked={!!v.collect_docker} onChange={(x) => set({ collect_docker: x })} label="Docker containers" />
          <Toggle checked={!!v.collect_temperatures} onChange={(x) => set({ collect_temperatures: x })} label="Temperatures" />
          <Toggle checked={!!v.fail2ban_enabled} onChange={(x) => set({ fail2ban_enabled: x })} label="Read fail2ban" />
          <Toggle checked={!!v.refresh_package_index} onChange={(x) => set({ refresh_package_index: x })} label="Refresh package index" />
          <Field label="Always-watched services" className="sm:col-span-2"><TagInput value={v.watched_services || []} onChange={(x) => set({ watched_services: x })} placeholder="sshd" /></Field>
        </div>
      </Card>
      <Card title="Brute-force detection">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2"><Toggle checked={!!v.bruteforce_enabled} onChange={(x) => set({ bruteforce_enabled: x })} label="Watch auth logs and the Windows Security log" /></div>
          <Field label="Detection window (minutes)"><Input type="number" value={v.bruteforce_window_minutes} onChange={(e) => set({ bruteforce_window_minutes: num(e.target.value) })} /></Field>
          <div />
          <Field label="Auto-block threshold (failures)" hint="Only on hosts with auto-block enabled"><Input type="number" value={v.auto_block_threshold} onChange={(e) => set({ auto_block_threshold: num(e.target.value) })} /></Field>
          <Field label="Auto-block duration (minutes)"><Input type="number" value={v.auto_block_minutes} onChange={(e) => set({ auto_block_minutes: num(e.target.value) })} /></Field>
          <Field label="Never block" className="sm:col-span-2" hint="IPs or CIDRs (your office, VPN, monitoring)"><TagInput value={v.auto_block_whitelist || []} onChange={(x) => set({ auto_block_whitelist: x })} placeholder="203.0.113.0/24" /></Field>
        </div>
        <Note className="mt-3">Alert levels for failed logins are set under Thresholds → “Failed logins”.</Note>
      </Card>
      <div className="xl:col-span-2"><SaveBar busy={busy} err={err} onSave={() => save()} /></div>
    </div>
  )
}

function Alerting({ data }: { data: any }) {
  const { v, set, save, busy, err } = useSection('alerting', data)
  return (
    <Card title="Alerting" className="max-w-3xl" subtitle="Hosts are marked down after the “Host offline after” threshold.">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Group wait (seconds)" hint="Combine events arriving within this window into one message"><Input type="number" value={v.group_wait_seconds} onChange={(e) => set({ group_wait_seconds: num(e.target.value) })} /></Field>
        <Field label="Default repeat for new channels (min)"><Input type="number" value={v.default_repeat_minutes} onChange={(e) => set({ default_repeat_minutes: num(e.target.value) })} /></Field>
        <Field label="Flap detection window (min)"><Input type="number" value={v.flap_window_minutes} onChange={(e) => set({ flap_window_minutes: num(e.target.value) })} /></Field>
        <Field label="Flapping after N state changes" hint="0 disables"><Input type="number" value={v.flap_threshold} onChange={(e) => set({ flap_threshold: num(e.target.value) })} /></Field>
        <Field label="Resolve after N good evaluations"><Input type="number" value={v.resolve_count} onChange={(e) => set({ resolve_count: num(e.target.value) })} /></Field>
      </div>
      <SaveBar busy={busy} err={err} onSave={() => save()} />
    </Card>
  )
}

function Vuln({ data }: { data: any }) {
  const { v, set, save, busy, err } = useSection('vuln', data)
  const [deleteDb, setDeleteDb] = useState(false)
  const db = useQuery({ queryKey: ['vulns', 'db'], queryFn: () => get<any>('/vulnerabilities/db'), refetchInterval: (q) => (q.state.data?.db?.updating ? 3000 : 30000) })
  const turningOff = !v.enabled && !!data.enabled
  const dbInfo = db.data?.db
  return (
    <Card title="Vulnerability scanning" className="max-w-3xl">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="sm:col-span-2 space-y-2">
          <Toggle checked={!!v.enabled} onChange={(x) => set({ enabled: x })} label="Enable vulnerability scanning" hint="Off by default to keep the server small. Turning it on downloads the Grype vulnerability database in the background: about 3 GB on disk, several minutes on first download, plus extra memory while scanning. Recommended for servers with 2 GB of RAM or more." />
          {v.enabled && data.enabled && dbInfo && (
            <p className="text-xs text-muted">{dbInfo.available ? `Database ready: ${bytes(dbInfo.size_bytes)}, built ${dateTime(Date.parse(dbInfo.built) / 1000)}.` : dbInfo.updating ? 'Downloading the database…' : `Database not available${dbInfo.last_error ? `: ${dbInfo.last_error}` : ''}.`}</p>
          )}
          {turningOff && (
            <Checkbox checked={deleteDb} onChange={setDeleteDb} label="Also delete the downloaded database to free disk space" />
          )}
          <Toggle checked={!!v.scan_after_inventory} onChange={(x) => set({ scan_after_inventory: x })} label="Rescan a host whenever its packages change" />
          <Toggle checked={!!v.only_fixable} onChange={(x) => set({ only_fixable: x })} label="Alert only on vulnerabilities with a fix available" />
          <Toggle checked={!!v.alert_on_inactive_kernels} onChange={(x) => set({ alert_on_inactive_kernels: x })} label="Alert on vulnerabilities in kernels that are not running" hint="Off by default: an old kernel that is installed but not booted is not exploitable. The findings stay visible in the list either way." />
        </div>
        <Field label="Alert at severity"><Select value={v.alert_severity} onChange={(e) => set({ alert_severity: e.target.value })}><option value="Critical">Critical</option><option value="High">High and above</option><option value="Medium">Medium and above</option><option value="none">Never alert</option></Select></Field>
        <Field label="Daily DB update hour (server time)"><Input type="number" min={0} max={23} value={v.sync_hour} onChange={(e) => set({ sync_hour: num(e.target.value) })} /></Field>
        <Field label="Database URL (air-gapped mirror)" className="sm:col-span-2" hint="Blank uses Anchore's public listing. Mirror https://grype.anchore.io/databases to serve it internally."><Input value={v.db_url} onChange={(e) => set({ db_url: e.target.value })} /></Field>
      </div>
      <SaveBar busy={busy} err={err} onSave={() => save(turningOff && deleteDb ? { ...v, delete_db: true } : undefined)} />
    </Card>
  )
}

function SnmpProfiles() {
  const p = useQuery({ queryKey: ['snmp-profiles'], queryFn: () => get<any>('/snmp/profiles') })
  const qc = useQueryClient()
  const toast = useToast()
  return (
    <Card title="SNMP device profiles" subtitle="Built-in profiles plus YAML files in the custom directory (same name overrides a built-in)."
      actions={<Button size="sm" icon={<RefreshCw className="size-3.5" />} onClick={async () => { try { await post('/snmp/profiles/reload'); toast('Profiles reloaded'); qc.invalidateQueries({ queryKey: ['snmp-profiles'] }) } catch (e) { toast(errorMessage(e), 'crit') } }}>Reload</Button>}>
      {p.data?.error && <ErrorBox error={p.data.error} className="mb-3" />}
      <p className="mb-3 text-sm text-muted">Custom directory: <code className="mono">{p.data?.custom_dir}</code></p>
      <div className="space-y-3">
        {(p.data?.profiles || []).map((x: any) => (
          <details key={x.name} className="rounded-xl border border-line/70 p-4">
            <summary className="cursor-pointer"><b>{x.name}</b> <Badge>{x.source}</Badge> <span className="text-sm text-muted">{x.description}</span></summary>
            <div className="mt-2 text-xs">
              {x.include?.length > 0 && <p className="text-muted">includes: {x.include.join(', ')}</p>}
              {(x.match?.sys_object_id || x.match?.sys_descr) && <p className="text-muted">matches: {[...(x.match.sys_object_id || []), ...(x.match.sys_descr || [])].join(', ')}</p>}
              <ul className="mono mt-2 space-y-1">{(x.metrics || []).map((m: any) => <li key={m.name}>{m.name}: {m.expr || m.oid}{m.label_oid ? ` [per ${m.label_name || 'index'}]` : ''}</li>)}</ul>
            </div>
          </details>
        ))}
      </div>
    </Card>
  )
}

function Server() {
  const s = useQuery({ queryKey: ['server-status'], queryFn: () => get<any>('/server/status'), refetchInterval: 30_000 })
  if (s.isLoading) return <Loading />
  const x = s.data
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card title="Server">
        <KV items={[
          ['Version', `${x.version} (${x.commit}, ${x.build_date})`], ['Go', x.go], ['Uptime', duration(x.uptime_seconds)],
          ['Agents connected', `${x.agents_connected} / ${x.agents_total}`], ['Hosts', x.hosts_total], ['TLS', x.tls_mode],
          ['Memory', bytes(x.memory_bytes)], ['Goroutines', x.goroutines], ['Data directory', <span key="d" className="mono">{x.data_dir}</span>],
          ['Disk', x.disk ? `${bytes(x.disk.used_bytes)} used of ${bytes(x.disk.total_bytes)} (${bytes(x.disk.free_bytes)} free)` : '–'],
          ['CA fingerprint', <span key="f" className="mono break-all text-xs">{x.ca_fingerprint}</span>],
        ]} />
      </Card>
      <Card title="Storage">
        <KV items={[
          ['SQLite', bytes(x.db_bytes)],
          ['Metrics (TSDB)', bytes(x.tsdb?.disk_bytes)], ['Series in memory', x.tsdb?.head_series], ['Blocks', x.tsdb?.blocks],
          ['Bytes per sample', x.tsdb?.bytes_per_sample?.toFixed(2)], ['Oldest data', x.tsdb?.min_time ? dateTime(x.tsdb.min_time / 1000) : '–'],
          ['Retention', `${x.retention_days} days`],
          ['Vulnerability DB', x.vulndb?.available ? `${bytes(x.vulndb.size_bytes)}, built ${dateTime(Date.parse(x.vulndb.built) / 1000)}` : 'not downloaded'],
        ]} />
      </Card>
      <Card title="Agent builds" bodyClass="p-0">
        <table className="w-full text-sm"><tbody>{(x.agent_binaries || []).map((b: any) => (
          <tr key={b.name} className="border-b border-line/60"><td className="px-4 py-2">{b.os}/{b.arch}</td><td className="px-4 py-2">{b.version}</td><td className="px-4 py-2 text-xs text-muted">{bytes(b.size)}</td>
            <td className="px-4 py-2 text-right"><a className="text-xs text-accent hover:underline" href={`/download/agent/${b.os}/${b.arch}`}>download</a></td></tr>
        ))}</tbody></table>
        {!x.agent_binaries?.length && <p className="p-4 text-sm text-muted">No agent binaries embedded in this build.</p>}
      </Card>
      <Card title="Backup">
        <div className="space-y-3 text-sm">
          <p>Download the configuration database, PKI (CA, signing keys) and custom SNMP profiles. Metrics are not included; use the CLI with <code className="mono">--with-tsdb</code> for a full copy.</p>
          <a href="/api/v1/server/backup"><Button variant="primary" icon={<Download className="size-4" />}>Download backup</Button></a>
          <CodeBlock code={`# CLI (works while the server runs)\nsentrel-server backup --data-dir ${x.data_dir} --out backup.tar.gz\n# restore onto a stopped or new server\nsentrel-server restore --data-dir ${x.data_dir} --in backup.tar.gz`} />
          <Note tone="warn">The backup contains the CA private key and job signing key. Store it securely.</Note>
          {x.tls_mode === 'http' && <Note tone="warn">This server is running without HTTPS. The backup contains the CA and job-signing keys; download it only over a trusted network.</Note>}
        </div>
      </Card>
    </div>
  )
}

/** Branding editor (global for provider admins, per-tenant for tenant admins). */
export function BrandingPage() {
  const { me, refresh } = useAuth()
  const toast = useToast()
  const provider = me?.user.tenant_id === 0
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants'), enabled: provider })
  const [tid, setTid] = useState<number>(me?.user.tenant_id ?? 0)
  const b = useQuery({ queryKey: ['branding', tid], queryFn: () => get<Branding>('/branding', { tenant_id: tid, raw: 1 }) })
  const [v, setV] = useState<Branding | null>(null)
  useEffect(() => { if (b.data) setV(b.data) }, [b.data])
  const [err, setErr] = useState<unknown>(null)
  if (!v) return <Loading />
  const file = (k: 'logo_data_url' | 'favicon_data_url') => (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (!f) return
    if (f.size > 500_000) { toast('Image must be under 500 KB', 'crit'); return }
    const r = new FileReader()
    r.onload = () => setV({ ...v, [k]: String(r.result) })
    r.readAsDataURL(f)
  }
  return (
    <div>
      <PageHeader title="Branding" subtitle={tid === 0 ? 'Global look: product name, logo and colours. Tenants can override any field for their portal and status pages.' : 'Overrides for this tenant; empty fields fall back to the global branding.'}
        actions={provider && (tenants.data?.length ?? 0) > 1 && <div className="w-56"><Select value={tid} onChange={(e) => setTid(Number(e.target.value))}>{tenants.data!.map((t) => <option key={t.id} value={t.id}>{t.id === 0 ? 'Global' : t.name}</option>)}</Select></div>} />
      <div className="grid gap-4 xl:grid-cols-2">
        <Card title="Appearance">
          <div className="space-y-3">
            <Field label="Product name"><Input value={v.product_name} onChange={(e) => setV({ ...v, product_name: e.target.value })} placeholder={tid ? 'inherit' : 'Sentrel'} /></Field>
            <Field label="Accent colour"><div className="flex items-center gap-2"><input type="color" value={v.accent_color || '#1f6bf5'} onChange={(e) => setV({ ...v, accent_color: e.target.value })} className="h-9 w-14 rounded border border-line bg-panel" /><Input className="w-32 mono" value={v.accent_color} onChange={(e) => setV({ ...v, accent_color: e.target.value })} /></div></Field>
            <Field label="Logo (PNG/SVG/JPEG, max 500 KB)"><div className="flex items-center gap-2"><input type="file" accept="image/*" onChange={file('logo_data_url')} className="text-sm" />{v.logo_data_url && <Button size="sm" variant="ghost" onClick={() => setV({ ...v, logo_data_url: '' })}>Remove</Button>}</div></Field>
            <Field label="Favicon"><div className="flex items-center gap-2"><input type="file" accept="image/*" onChange={file('favicon_data_url')} className="text-sm" />{v.favicon_data_url && <Button size="sm" variant="ghost" onClick={() => setV({ ...v, favicon_data_url: '' })}>Remove</Button>}</div></Field>
            <Field label="Login page text"><Textarea value={v.login_text} onChange={(e) => setV({ ...v, login_text: e.target.value })} placeholder="Authorised use only." /></Field>
            <Field label="Footer text"><Input value={v.footer_text} onChange={(e) => setV({ ...v, footer_text: e.target.value })} /></Field>
            <ErrorBox error={err} />
            <Button variant="primary" icon={<Save className="size-4" />} onClick={async () => {
              setErr(null)
              try { const eff = await put<Branding>('/branding', v, { tenant_id: tid }); toast('Branding saved'); if (tid === (me?.user.tenant_id ?? 0)) { applyBranding(eff); refresh() } } catch (e) { setErr(e) }
            }}>Save</Button>
          </div>
        </Card>
        <Card title="Preview">
          <div className="rounded-2xl border border-line bg-bg p-6">
            <div className="mx-auto max-w-xs rounded-2xl border border-line bg-panel p-5 text-center">
              {v.logo_data_url ? <img src={v.logo_data_url} alt="" className="mx-auto h-10 max-w-[180px] object-contain" /> : <div className="mx-auto size-10 rounded-lg" style={{ background: v.accent_color || 'var(--accent)' }} />}
              <div className="mt-2 font-semibold">{v.product_name || 'Sentrel'}</div>
              {v.login_text && <p className="mt-2 text-xs text-muted">{v.login_text}</p>}
              <div className="mt-4 rounded-md py-2 text-sm text-white" style={{ background: v.accent_color || 'var(--accent)' }}>Sign in</div>
            </div>
          </div>
        </Card>
      </div>
    </div>
  )
}

function GroupMap({ maps, onChange, tenants, groupPlaceholder }: { maps: any[]; onChange: (m: any[]) => void; tenants?: Tenant[]; groupPlaceholder: string }) {
  const roles = (t: number) => (t === 0 ? ['admin', 'operator', 'viewer'] : ['tenant_admin', 'tenant_viewer'])
  return (
    <div className="space-y-2">
      {maps.map((m, i) => (
        <div key={i} className="flex flex-wrap items-center gap-2">
          <Input className="min-w-40 flex-1" value={m.group} placeholder={groupPlaceholder} onChange={(e) => { const a = [...maps]; a[i] = { ...m, group: e.target.value }; onChange(a) }} />
          <Select value={m.tenant_id} onChange={(e) => { const a = [...maps]; const t = Number(e.target.value); a[i] = { ...m, tenant_id: t, role: roles(t)[0] }; onChange(a) }}>
            {tenants?.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </Select>
          <Select value={m.role} onChange={(e) => { const a = [...maps]; a[i] = { ...m, role: e.target.value }; onChange(a) }}>
            {roles(m.tenant_id).map((r) => <option key={r} value={r}>{roleInfo[r].label}</option>)}
          </Select>
          <Button size="sm" variant="ghost" icon={<X className="size-3.5" />} onClick={() => onChange(maps.filter((_, j) => j !== i))} />
        </div>
      ))}
      <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => onChange([...maps, { group: '', role: 'viewer', tenant_id: 0 }])}>Add mapping</Button>
    </div>
  )
}

function DefaultRole({ v, set, tenants, none }: { v: any; set: (p: any) => void; tenants?: Tenant[]; none: string }) {
  const roles = (t: number) => (t === 0 ? ['admin', 'operator', 'viewer'] : ['tenant_admin', 'tenant_viewer'])
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="Default role (no mapping matched)"><Select value={v.default_role} onChange={(e) => set({ default_role: e.target.value })}><option value="">{none}</option>{roles(v.default_tenant || 0).map((r) => <option key={r} value={r}>{roleInfo[r].label}</option>)}</Select></Field>
      <Field label="Default tenant"><Select value={v.default_tenant || 0} onChange={(e) => set({ default_tenant: Number(e.target.value), default_role: '' })}>{tenants?.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</Select></Field>
    </div>
  )
}

function SSO({ oidc, proxy, serverUrl }: { oidc: any; proxy: any; serverUrl?: string }) {
  const O = useSection('oidc', oidc)
  const P = useSection('proxy_auth', proxy)
  const toast = useToast()
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants') })
  const callback = `${(serverUrl || window.location.origin).replace(/\/$/, '')}/api/v1/auth/oidc/callback`
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card title="OpenID Connect" subtitle="Authentik, Authelia, Keycloak, Google, Microsoft Entra and other OIDC providers.">
        <div className="space-y-3">
          <Toggle checked={!!O.v.enabled} onChange={(x) => O.set({ enabled: x })} label="Enable single sign-on" />
          <Field label="Redirect URI" hint="Register this exact URL with your provider. It is built from the server URL in General settings.">
            <div className="flex items-center gap-2"><Input readOnly value={callback} className="mono" /><CopyButton text={callback} /></div>
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Issuer URL" className="sm:col-span-2" hint="The address that serves /.well-known/openid-configuration"><Input value={O.v.issuer_url} onChange={(e) => O.set({ issuer_url: e.target.value })} placeholder="https://auth.example.com/application/o/sentrel" /></Field>
            <Field label="Client ID"><Input value={O.v.client_id} onChange={(e) => O.set({ client_id: e.target.value })} /></Field>
            <Field label="Client secret" hint={secretHint(O.v.client_secret_set)}><Input type="password" autoComplete="new-password" value={O.v.client_secret || ''} onChange={(e) => O.set({ client_secret: e.target.value })} /></Field>
            <Field label="Scopes"><Input value={O.v.scopes} onChange={(e) => O.set({ scopes: e.target.value })} /></Field>
            <Field label="Button label" hint="Shown as Sign in with …"><Input value={O.v.button_label} onChange={(e) => O.set({ button_label: e.target.value })} /></Field>
            <Field label="Username claim"><Input value={O.v.username_claim} onChange={(e) => O.set({ username_claim: e.target.value })} /></Field>
            <Field label="Groups claim"><Input value={O.v.groups_claim} onChange={(e) => O.set({ groups_claim: e.target.value })} /></Field>
          </div>
          <Field label="Group → role mapping" hint="The most privileged matching mapping wins. Users matching none get the default role, or are refused if none.">
            <GroupMap maps={O.v.group_role_map || []} onChange={(m) => O.set({ group_role_map: m })} tenants={tenants.data} groupPlaceholder="sentrel-admins" />
          </Field>
          <DefaultRole v={O.v} set={O.set} tenants={tenants.data} none="Refuse sign-in" />
          <div className="space-y-2">
            <Toggle checked={!!O.v.allow_provision} onChange={(x) => O.set({ allow_provision: x })} label="Create accounts on first sign-in" />
            <Toggle checked={!!O.v.require_verified_email} onChange={(x) => O.set({ require_verified_email: x })} label="Require a verified email address" />
            <Toggle checked={!!O.v.trust_idp_mfa} onChange={(x) => O.set({ trust_idp_mfa: x })} label="Trust the provider's MFA" hint="A sign-in whose amr claim includes mfa, otp or hwk skips the second factor here. Otherwise the normal 2FA policy applies." />
          </div>
          <Note>Users created through OIDC cannot sign in with a password.</Note>
        </div>
        <SaveBar busy={O.busy} err={O.err} onSave={() => O.save()} extra={
          <Button onClick={async () => { try { const r = await post<any>('/settings/oidc/test'); toast(r.ok ? r.message : r.error, r.ok ? 'ok' : 'crit') } catch (e) { toast(errorMessage(e), 'crit') } }}>Test discovery (save first)</Button>
        } />
      </Card>
      <Card title="Forward-auth headers" subtitle="Authelia, Authentik proxy outposts, oauth2-proxy and similar.">
        <div className="space-y-3">
          <Toggle checked={!!P.v.enabled} onChange={(x) => P.set({ enabled: x })} label="Sign users in from proxy headers" />
          <Note tone="warn">Only requests whose direct connection comes from a trusted proxy are believed (TLS &amp; access: Trust reverse proxy headers and Trusted proxies). Your proxy must strip these headers from client requests and set them itself, or anyone could sign in as anybody.</Note>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Username header"><Input value={P.v.user_header} onChange={(e) => P.set({ user_header: e.target.value })} /></Field>
            <Field label="Email header"><Input value={P.v.email_header} onChange={(e) => P.set({ email_header: e.target.value })} /></Field>
            <Field label="Name header"><Input value={P.v.name_header} onChange={(e) => P.set({ name_header: e.target.value })} /></Field>
            <Field label="Groups header" hint="Comma-separated"><Input value={P.v.groups_header} onChange={(e) => P.set({ groups_header: e.target.value })} /></Field>
          </div>
          <Field label="Group → role mapping">
            <GroupMap maps={P.v.group_role_map || []} onChange={(m) => P.set({ group_role_map: m })} tenants={tenants.data} groupPlaceholder="admins" />
          </Field>
          <DefaultRole v={P.v} set={P.set} tenants={tenants.data} none="Refuse sign-in" />
          <Toggle checked={!!P.v.allow_provision} onChange={(x) => P.set({ allow_provision: x })} label="Create accounts on first sign-in" />
        </div>
        <SaveBar busy={P.busy} err={P.err} onSave={() => P.save()} />
      </Card>
    </div>
  )
}
