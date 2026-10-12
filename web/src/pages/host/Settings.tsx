import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Save, Trash2, ShieldX } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import type { AgentOptions, HostDetail, HostGroup, Tenant } from '@/lib/types'
import { SnmpForm, defaultSnmp, type SnmpConfig } from '@/components/SnmpForm'
import { Button, Card, ErrorBox, Field, Input, Note, Select, TagInput, Textarea, Toggle, useConfirm, useToast } from '@/components/ui'

export function HostSettings({ host }: { host: HostDetail }) {
  const { can, me } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const nav = useNavigate()
  const groups = useQuery({ queryKey: ['groups', host.tenant_id], queryFn: () => get<HostGroup[]>('/groups', { tenant_id: host.tenant_id }) })
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants'), enabled: me?.user.tenant_id === 0 })
  const [f, setF] = useState({
    name: host.name, address: host.address, group_id: host.group_id ?? 0, tags: host.tags || [], notes: host.notes,
    interval_seconds: host.interval_seconds ?? '', inventory_interval: host.inventory_interval ?? '', retention_days: host.retention_days ?? '',
    allow_upgrades: host.allow_upgrades, allow_cleanup: !!host.allow_cleanup, auto_remove_old_kernels: !!host.auto_remove_old_kernels, allow_ip_block: host.allow_ip_block, allow_scripts: host.allow_scripts, auto_block: host.auto_block,
    tenant_id: host.tenant_id,
  })
  const [opts, setOpts] = useState<AgentOptions>(host.agent_options || {})
  const [snmp, setSnmp] = useState<SnmpConfig>({ ...defaultSnmp, ...(host.snmp_config as any) })
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof f, v: any) => setF({ ...f, [k]: v })
  const eff = (host.agent_config || {}) as Record<string, any>

  const save = async () => {
    setBusy(true)
    setErr(null)
    try {
      const body: Record<string, unknown> = {
        name: f.name, address: f.address, group_id: Number(f.group_id), tags: f.tags, notes: f.notes,
        allow_upgrades: f.allow_upgrades, allow_cleanup: f.allow_cleanup, allow_ip_block: f.allow_ip_block, auto_block: f.auto_block, auto_remove_old_kernels: f.auto_remove_old_kernels,
      }
      if (can('scripts')) body.allow_scripts = f.allow_scripts
      if (f.interval_seconds === '' || f.interval_seconds === null) body.clear_interval = true
      else body.interval_seconds = Number(f.interval_seconds)
      if (f.inventory_interval !== '' && f.inventory_interval !== null) body.inventory_interval = Number(f.inventory_interval)
      if (f.retention_days === '' || f.retention_days === null) body.clear_retention = true
      else body.retention_days = Number(f.retention_days)
      if (host.kind === 'agent') body.agent_options = opts
      if (me?.user.tenant_id === 0 && f.tenant_id !== host.tenant_id) body.tenant_id = Number(f.tenant_id)
      await put(`/hosts/${host.id}`, body)
      if (host.kind === 'snmp') await put(`/hosts/${host.id}/snmp`, { address: f.address, snmp })
      toast('Host settings saved')
      qc.invalidateQueries({ queryKey: ['host', String(host.id)] })
      qc.invalidateQueries({ queryKey: ['hosts'] })
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }

  const opt = (k: keyof AgentOptions) => (opts[k] === undefined ? (eff[k] as boolean | undefined) ?? false : (opts[k] as boolean))
  const setOpt = (k: keyof AgentOptions, v: unknown) => setOpts({ ...opts, [k]: v })

  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card title="General">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Display name"><Input value={f.name} onChange={(e) => set('name', e.target.value)} /></Field>
          <Field label="Address"><Input value={f.address} onChange={(e) => set('address', e.target.value)} /></Field>
          <Field label="Group" hint="Thresholds and notification routes can target groups">
            <Select value={f.group_id} onChange={(e) => set('group_id', Number(e.target.value))}>
              <option value={0}>No group</option>
              {groups.data?.filter((g) => g.tenant_id === host.tenant_id).map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </Select>
          </Field>
          {me?.user.tenant_id === 0 && (tenants.data?.length ?? 0) > 1 && (
            <Field label="Tenant" hint="Moving a host moves its history with it">
              <Select value={f.tenant_id} onChange={(e) => set('tenant_id', Number(e.target.value))}>
                {tenants.data!.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
              </Select>
            </Field>
          )}
          <Field label="Tags" className="sm:col-span-2" hint="For example env:prod or role:db. Press Enter after each tag."><TagInput value={f.tags} onChange={(v) => set('tags', v)} /></Field>
          <Field label="Notes" className="sm:col-span-2"><Textarea value={f.notes} onChange={(e) => set('notes', e.target.value)} /></Field>
        </div>
      </Card>

      {host.kind === 'agent' && (
        <Card title="Collection">
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Interval (s)" hint={`Default ${eff.interval_seconds ?? 30}s`}><Input type="number" min={5} placeholder="inherit" value={f.interval_seconds as any} onChange={(e) => set('interval_seconds', e.target.value)} /></Field>
            <Field label="Inventory (s)" hint="Package scan; default 1h"><Input type="number" min={300} placeholder="inherit" value={f.inventory_interval as any} onChange={(e) => set('inventory_interval', e.target.value)} /></Field>
            <Field label="Retention (days)" hint="Blank = global"><Input type="number" min={1} placeholder="inherit" value={f.retention_days as any} onChange={(e) => set('retention_days', e.target.value)} /></Field>
          </div>
          <div className="mt-4 grid gap-3 sm:grid-cols-2">
            <Toggle checked={opt('collect_services')} onChange={(v) => setOpt('collect_services', v)} label="All services" hint="Otherwise only watched services" />
            <Toggle checked={opt('collect_ports')} onChange={(v) => setOpt('collect_ports', v)} label="Listening ports" />
            <Toggle checked={opt('collect_docker')} onChange={(v) => setOpt('collect_docker', v)} label="Docker containers" />
            <Toggle checked={opt('collect_temperatures')} onChange={(v) => setOpt('collect_temperatures', v)} label="Temperatures" />
            <Toggle checked={opt('bruteforce_enabled')} onChange={(v) => setOpt('bruteforce_enabled', v)} label="Brute-force detection" hint="Watch auth logs / Security log" />
            <Toggle checked={opt('fail2ban_enabled')} onChange={(v) => setOpt('fail2ban_enabled', v)} label="Read fail2ban status" />
            <Toggle checked={opt('portscan_enabled')} onChange={(v) => setOpt('portscan_enabled', v)} label="Port-scan detection" hint="Linux with nftables; the kernel counts, the agent only polls" />
            <Toggle checked={opt('refresh_package_index')} onChange={(v) => setOpt('refresh_package_index', v)} label="Refresh package index" hint="apt-get update / dnf makecache before inventory" />
          </div>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <Field label="Top processes reported"><Input type="number" min={0} max={50} value={opts.top_processes ?? (eff.top_processes as number) ?? 10} onChange={(e) => setOpt('top_processes', Number(e.target.value))} /></Field>
            <Field label="Watched services" hint="Always reported (in addition to service checks)"><TagInput value={opts.watched_services || []} onChange={(v) => setOpt('watched_services', v)} placeholder="nginx, postgresql" /></Field>
          </div>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <Field label="Port-scan threshold (closed-port SYNs per minute per source)" hint={`Default ${(eff.portscan_threshold_per_min as number) ?? 20}`}><Input type="number" min={1} placeholder="inherit" value={(opts.portscan_threshold_per_min ?? '') as any} onChange={(e) => setOpt('portscan_threshold_per_min', e.target.value === '' ? undefined : Number(e.target.value))} /></Field>
            <Field label="Port-scan allowlist" hint="IPs or CIDRs never flagged on this host (added to the global list)"><TagInput value={opts.portscan_allowlist || []} onChange={(v) => setOpt('portscan_allowlist', v)} placeholder="198.51.100.0/24" /></Field>
          </div>
        </Card>
      )}

      {host.kind === 'agent' && (
        <Card title="Remote actions" subtitle="Turned on per host. Every job is signed, confirmed and audited.">
          <div className="space-y-3">
            <Toggle checked={f.allow_upgrades} onChange={(v) => set('allow_upgrades', v)} label="Allow remote package upgrades" hint="Upgrades still need explicit confirmation or a maintenance-window schedule" />
            <Toggle checked={f.auto_remove_old_kernels && f.allow_upgrades && host.write_policy !== 'operator' && host.write_policy !== 'none'} onChange={(v) => set('auto_remove_old_kernels', v)} disabled={!f.allow_upgrades || host.write_policy === 'operator' || host.write_policy === 'none'} label="Automatically remove old kernels" hint={host.write_policy === 'operator' || host.write_policy === 'none' ? 'Not available: this agent is read-only or needs operator-signed jobs, so a signature would be required each time' : 'Needs remote upgrades. Removes every kernel except the running and the newest during the next maintenance window that allows jobs, at most once a day'} />
            <Toggle checked={f.allow_cleanup} onChange={(v) => set('allow_cleanup', v)} label="Allow disk cleanup" hint="Lets administrators remove caches, old logs and temporary files from the Storage breakdown on the Overview tab" />
            <Toggle checked={f.allow_ip_block} onChange={(v) => set('allow_ip_block', v)} label="Allow firewall IP blocking" hint="nftables/iptables on Linux, Windows Firewall on Windows; blocks always expire" />
            <Toggle checked={f.auto_block} onChange={(v) => set('auto_block', v)} disabled={!f.allow_ip_block} label="Automatically block brute-force sources" hint="Uses the threshold and duration from Settings → Agents" />
            <Toggle checked={f.allow_scripts} onChange={(v) => set('allow_scripts', v)} disabled={!can('scripts')} label="Allow administrators to run scripts" hint="Scripts run as the unprivileged agent user on Linux (SYSTEM on Windows)" />
          </div>
        </Card>
      )}

      {host.kind === 'snmp' && (
        <Card title="SNMP">
          <SnmpForm value={snmp} onChange={setSnmp} />
          <Button className="mt-3" size="sm" onClick={async () => {
            try {
              const r = await post<any>('/snmp/test', { address: f.address, snmp, host_id: host.id })
              toast(r.ok ? `OK: ${r.system?.name || r.system?.descr} · profile ${r.profile} · ${r.interfaces} interfaces` : `Failed: ${r.error}`, r.ok ? 'ok' : 'crit')
            } catch (e) { toast(errorMessage(e), 'crit') }
          }}>Test connection</Button>
        </Card>
      )}

      <div className="xl:col-span-2">
        <ErrorBox error={err} className="mb-3" />
        <Button variant="primary" loading={busy} icon={<Save className="size-4" />} onClick={save}>Save changes</Button>
      </div>

      {can('hosts') && (
        <Card title="Danger zone" className="border-crit/40 xl:col-span-2">
          <div className="flex flex-wrap gap-3">
            {host.kind === 'agent' && !host.revoked && (
              <Button icon={<ShieldX className="size-4" />} onClick={async () => {
                if (!(await confirm({ title: 'Revoke agent', danger: true, confirmLabel: 'Revoke', body: <p>Revoke the agent certificate for <b>{host.name}</b>? The agent is disconnected and can never reconnect with this identity; history is kept.</p> }))) return
                try { await post(`/hosts/${host.id}/revoke`); toast('Agent revoked'); qc.invalidateQueries({ queryKey: ['host', String(host.id)] }) } catch (e) { toast(errorMessage(e), 'crit') }
              }}>Revoke agent</Button>
            )}
            <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={async () => {
              if (!(await confirm({ title: 'Delete host', danger: true, confirmLabel: 'Delete', typeToConfirm: host.name, body: <p>Delete <b>{host.name}</b> and all of its metrics, checks, alerts, jobs and findings? This cannot be undone. Agents are revoked.</p> }))) return
              try { await del(`/hosts/${host.id}`); toast('Host deleted'); qc.invalidateQueries({ queryKey: ['hosts'] }); nav('/hosts') } catch (e) { toast(errorMessage(e), 'crit') }
            }}>Delete host</Button>
          </div>
          {host.kind === 'agent' && <Note className="mt-3">To remove the agent software too, run <code className="mono">sudo sh install-agent.sh --uninstall</code> on Linux or <code className="mono">install-agent.ps1 -Uninstall</code> on Windows.</Note>}
        </Card>
      )}
    </div>
  )
}
