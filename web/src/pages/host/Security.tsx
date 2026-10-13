import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, Settings2, ShieldOff } from 'lucide-react'
import { errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, dateTime, until } from '@/lib/format'
import type { HostDetail, Series } from '@/lib/types'
import { Chart } from '@/components/Chart'
import { InheritableField, useHostSetting } from '@/components/InheritableField'
import { Badge, Button, Card, Drawer, Empty, Field, Input, Loading, Modal, Note, Segmented, SettingRow, SettingsGroup, Stat, Table, Td, Th, Toggle, useToast } from '@/components/ui'

interface SecData {
  failed_logins: { ip: string; total: number; last_seen: number; usernames: string[]; blocked: boolean }[]
  total: number
  blocks: { id: number; ip: string; reason: string; expires_at: number; status: string; created_by: string; created_at: number }[]
  fail2ban: { data: any; updated_at: number }
  allow_ip_block: boolean
  auto_block: boolean
  auto_block_threshold: number
  window_minutes: number
  notify_failed_logins?: boolean
  notify_portscan?: boolean
  portscans?: { ip: string; first_seen: number; last_seen: number; reports: number; blocked: boolean }[]
  portscan_status?: { known: boolean; supported: boolean; active: boolean; reason: string; updated_at: number }
}

const periodLabel = { '1': 'last hour', '24': 'last 24 hours', '168': 'last 7 days' } as const

export function HostSecurity({ host }: { host: HostDetail }) {
  const [hours, setHours] = useState<'1' | '24' | '168'>('24')
  const { can } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const [blockIp, setBlockIp] = useState<string | null>(null)
  const [configOpen, setConfigOpen] = useState(false)
  const save = useHostSetting(host.id)
  const d = useQuery({ queryKey: ['security', host.id, hours], queryFn: () => get<SecData>(`/hosts/${host.id}/security`, { hours }), refetchInterval: 30_000 })
  const chart = useQuery({ queryKey: ['metrics', host.id, 'auth', hours], queryFn: () => get<{ series: Series[] }>(`/hosts/${host.id}/metrics`, { panel: 'auth', start: `${Number(hours) * 3600}s` }) })
  if (d.isLoading) return <Loading />
  const x = d.data!
  const s = host.settings
  const canEdit = can('host_settings') || can('hosts')
  const f2b = x.fail2ban?.data
  const scans = x.portscans || []
  const ps = x.portscan_status
  const activeBlocks = x.blocks.filter((b) => b.status === 'active' || b.status === 'pending').length
  const psLine = !ps?.known ? 'Status not reported yet (the agent may need an update)'
    : !ps.supported ? `Not available on this host${ps.reason ? ': ' + ps.reason : ''}`
    : ps.active ? 'Detection active (kernel-side, nftables)' : `Detection off${ps.reason ? ': ' + ps.reason : ''}`
  const unblock = async (ip: string) => {
    try {
      await post(`/hosts/${host.id}/unblock`, { ip })
      toast(`Unblocking ${ip}`)
      qc.invalidateQueries({ queryKey: ['security', host.id] })
    } catch (e) {
      toast(errorMessage(e), 'crit')
    }
  }
  const setNotify = async (notify: boolean, metric: 'bruteforce' | 'portscan' = 'bruteforce') => {
    const what = metric === 'portscan' ? 'Port-scan' : 'Failed-login'
    try {
      await put(`/hosts/${host.id}/notify`, { metric, notify })
      toast(notify ? `${what} notifications enabled` : `${what} notifications disabled`)
      qc.invalidateQueries({ queryKey: ['security', host.id] })
      qc.invalidateQueries({ queryKey: ['host-thresholds', host.id] })
    } catch (e) {
      toast(errorMessage(e), 'crit')
    }
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Segmented value={hours} onChange={setHours} options={[{ id: '1', label: '1h' }, { id: '24', label: '24h' }, { id: '168', label: '7d' }]} />
        {s && <Button size="sm" icon={<Settings2 className="size-3.5" />} onClick={() => setConfigOpen(true)}>Configure</Button>}
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label={`Failed logins, ${periodLabel[hours]}`} value={x.total} tone={x.total > 0 ? 'warn' : 'muted'} sub={`${x.failed_logins.length} source${x.failed_logins.length === 1 ? '' : 's'}`} />
        <Stat label={`Port scans, ${periodLabel[hours]}`} value={scans.length} tone={scans.length > 0 ? 'warn' : 'muted'} sub={ps?.known && ps.supported ? (ps.active ? 'Detection active' : 'Detection off') : 'Not available'} />
        <Stat label="Active blocks" value={activeBlocks} tone={activeBlocks > 0 ? 'crit' : 'muted'} sub={x.allow_ip_block ? (x.auto_block ? 'Auto-block on' : 'Manual blocking') : 'Blocking off'} />
        <Stat label="fail2ban" value={f2b?.available ? 'Active' : 'None'} tone={f2b?.available ? undefined : 'muted'} sub={f2b?.available ? `${(f2b.jails || []).length} jail${(f2b.jails || []).length === 1 ? '' : 's'}` : 'Not detected'} />
      </div>

      {s && (
        <Drawer open={configOpen} onClose={() => setConfigOpen(false)} title="Security settings" subtitle={`${host.name} · values marked Default come from Settings, Agents`}>
          <SettingsGroup title="Brute-force detection" info="Failed logins are read from the auth log on Linux and the Security event log on Windows.">
            <InheritableField label="Detect failed logins" kind="bool" setting={s.bruteforce_enabled} disabled={!canEdit} onSave={save('bruteforce_enabled')} />
            <InheritableField label="Alert threshold" description="Failed attempts from one address within the window before an alert is raised." kind="number" unit="attempts" setting={s.bruteforce_threshold} disabled={!canEdit} onSave={save('bruteforce_threshold')} />
            <InheritableField label="Detection window" description="Attempts are counted over this period." kind="number" unit="minutes" max={1440} setting={s.bruteforce_window_minutes} disabled={!canEdit} onSave={save('bruteforce_window_minutes')} />
            <SettingRow label="Send notifications" info="When off, failed-login alerts still appear in Sentrel but no emails or webhooks are sent for this host.">
              <Toggle checked={x.notify_failed_logins !== false} onChange={setNotify} disabled={!canEdit} /><span className="w-[4.75rem]" />
            </SettingRow>
          </SettingsGroup>
          <SettingsGroup title="Port-scan detection" info={<>Linux with nftables. The kernel counts connection attempts to closed ports; the agent only reads the result.<span className="mt-1 block text-muted">{psLine}</span></>}>
            <InheritableField label="Detect port scans" kind="bool" setting={s.portscan_enabled} disabled={!canEdit} onSave={save('portscan_enabled')} />
            <InheritableField label="Alert threshold" description="Closed-port connection attempts per minute from one source." kind="number" unit="per min" setting={s.portscan_threshold_per_min} disabled={!canEdit} onSave={save('portscan_threshold_per_min')} />
            <SettingRow label="Send notifications" info="When off, port-scan alerts still appear in Sentrel but no emails or webhooks are sent for this host.">
              <Toggle checked={x.notify_portscan !== false} onChange={(v) => setNotify(v, 'portscan')} disabled={!canEdit} /><span className="w-[4.75rem]" />
            </SettingRow>
          </SettingsGroup>
          <SettingsGroup title="Blocking" info="Blocks use nftables or iptables on Linux and Windows Firewall on Windows. Blocks always expire.">
            <InheritableField label="Allow IP blocking" description="Lets Sentrel add and remove firewall blocks on this host." kind="bool" setting={s.allow_ip_block} disabled={!canEdit} onSave={save('allow_ip_block')} />
            <InheritableField label="Block attackers automatically" description="Blocks sources that reach the brute-force or port-scan thresholds. Needs IP blocking." kind="bool" setting={s.auto_block} disabled={!canEdit} onSave={save('auto_block')} />
            <InheritableField label="Block duration" description="How long automatic blocks last." kind="number" unit="minutes" max={129600} setting={s.auto_block_minutes} disabled={!canEdit} onSave={save('auto_block_minutes')} />
            <InheritableField label="Auto-block threshold" description="Failed logins from one address within the detection window before it is blocked." kind="number" unit="attempts" setting={s.auto_block_threshold} disabled={!canEdit} onSave={save('auto_block_threshold')} />
            <InheritableField label="Never block" description={<>Addresses and CIDR ranges that are never blocked, manually or automatically.{!!host.never_block_system?.length && <> The Sentrel server ({host.never_block_system.join(', ')}) is always protected.</>}</>} kind="list" placeholder="198.51.100.0/24" setting={s.never_block} disabled={!canEdit} onSave={save('never_block')} />
          </SettingsGroup>
        </Drawer>
      )}
      <Card title="Failed logins" info="Highest number of failures from a single address within the detection window.">
        <Chart series={chart.data?.series || []} unit="number" height={160} />
      </Card>
      <Card title={`Sources (${x.failed_logins.length} IPs, ${x.total} failures)`} bodyClass="p-0">
        {!x.failed_logins.length ? <Empty title="No failed logins in this period" /> : (
          <Table>
            <thead><tr><Th>IP address</Th><Th>Failures</Th><Th className="hidden md:table-cell">Usernames tried</Th><Th>Last seen</Th><Th /></tr></thead>
            <tbody>
              {x.failed_logins.map((r) => (
                <tr key={r.ip}>
                  <Td className="mono text-xs">{r.ip} {r.blocked && <Badge tone="crit">blocked</Badge>}</Td>
                  <Td className="tabular-nums">{r.total}</Td>
                  <Td className="hidden max-w-md truncate text-xs text-muted md:table-cell" title={r.usernames.join(', ')}>{r.usernames.join(', ')}</Td>
                  <Td className="text-xs text-muted">{ago(r.last_seen)}</Td>
                  <Td className="text-right">
                    {can('jobs') && x.allow_ip_block && !r.blocked && <Button size="sm" variant="ghost" icon={<Ban className="size-3.5" />} onClick={() => setBlockIp(r.ip)}>Block</Button>}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card title={`Port scans (${scans.length} source${scans.length === 1 ? '' : 's'})`} subtitle={ps?.active ? undefined : psLine} bodyClass="p-0">
        {!scans.length ? <Empty title={ps?.supported ? 'No port scans in this period' : 'Port-scan detection is not available on this host'} /> : (
          <Table>
            <thead><tr><Th>Source IP</Th><Th>First seen</Th><Th>Last seen</Th><Th className="hidden md:table-cell">Host</Th><Th /></tr></thead>
            <tbody>
              {scans.map((r) => (
                <tr key={r.ip}>
                  <Td className="mono text-xs">{r.ip} {r.blocked && <Badge tone="crit">blocked</Badge>}</Td>
                  <Td className="text-xs text-muted" title={dateTime(r.first_seen)}>{ago(r.first_seen)}</Td>
                  <Td className="text-xs text-muted" title={dateTime(r.last_seen)}>{ago(r.last_seen)}</Td>
                  <Td className="hidden text-xs md:table-cell">{host.name}</Td>
                  <Td className="text-right">
                    {can('jobs') && x.allow_ip_block && !r.blocked && <Button size="sm" variant="ghost" icon={<Ban className="size-3.5" />} onClick={() => setBlockIp(r.ip)}>Block</Button>}
                    {can('jobs') && r.blocked && <Button size="sm" variant="ghost" icon={<ShieldOff className="size-3.5" />} onClick={() => unblock(r.ip)}>Unblock</Button>}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card title="Firewall blocks" bodyClass="p-0" actions={can('jobs') && x.allow_ip_block && <Button size="sm" icon={<Ban className="size-3.5" />} onClick={() => setBlockIp('')}>Block an IP</Button>}>
        {!x.allow_ip_block && <div className="p-4"><Note>IP blocking is off for this host.{s && canEdit && <> <button type="button" className="text-accent hover:underline" onClick={() => setConfigOpen(true)}>Turn it on</button></>}</Note></div>}
        {!x.blocks.length ? <Empty title="No blocks" /> : (
          <Table>
            <thead><tr><Th>IP</Th><Th>Status</Th><Th>Expires</Th><Th className="hidden md:table-cell">Reason</Th><Th className="hidden lg:table-cell">By</Th><Th /></tr></thead>
            <tbody>
              {x.blocks.map((b) => (
                <tr key={b.id}>
                  <Td className="mono text-xs">{b.ip}</Td>
                  <Td><Badge tone={b.status === 'active' ? 'crit' : b.status === 'pending' ? 'warn' : 'muted'}>{b.status}</Badge></Td>
                  <Td className="text-xs" title={dateTime(b.expires_at)}>{b.status === 'active' || b.status === 'pending' ? until(b.expires_at) : dateTime(b.expires_at)}</Td>
                  <Td className="hidden max-w-sm truncate text-xs text-muted md:table-cell">{b.reason}</Td>
                  <Td className="hidden text-xs text-muted lg:table-cell">{b.created_by}</Td>
                  <Td className="text-right">{can('jobs') && (b.status === 'active' || b.status === 'pending') && <Button size="sm" variant="ghost" icon={<ShieldOff className="size-3.5" />} onClick={() => unblock(b.ip)}>Unblock</Button>}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      {f2b?.available && (
        <Card title="fail2ban" subtitle={`updated ${ago(x.fail2ban.updated_at)}`} bodyClass="p-0">
          <Table>
            <thead><tr><Th>Jail</Th><Th>Failed (now / total)</Th><Th>Banned (now / total)</Th><Th className="hidden md:table-cell">Banned IPs</Th></tr></thead>
            <tbody>
              {(f2b.jails || []).map((j: any) => (
                <tr key={j.name}>
                  <Td className="font-medium">{j.name}</Td>
                  <Td>{j.currently_failed ?? 0} / {j.total_failed ?? 0}</Td>
                  <Td>{j.currently_banned ?? 0} / {j.total_banned ?? 0}</Td>
                  <Td className="mono hidden max-w-md truncate text-xs md:table-cell">{(j.banned_ips || []).join(', ')}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      {blockIp !== null && <BlockDialog hostId={host.id} hostName={host.name} ip={blockIp} onClose={() => { setBlockIp(null); qc.invalidateQueries({ queryKey: ['security', host.id] }) }} />}
    </div>
  )
}

function BlockDialog({ hostId, hostName, ip, onClose }: { hostId: number; hostName: string; ip: string; onClose: () => void }) {
  const [addr, setAddr] = useState(ip)
  const [minutes, setMinutes] = useState('60')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  return (
    <Modal open onClose={onClose} title={`Block an IP on ${hostName}`} size="sm"
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="danger" loading={busy} onClick={async () => {
        setBusy(true)
        try { const j = await post<{ status?: string }>(`/hosts/${hostId}/block`, { ip: addr, minutes: Number(minutes), reason, confirm: true }); toast(j?.status === 'awaiting_signature' ? `Blocking ${addr} is waiting for your operator signature (see Jobs)` : `Blocking ${addr}`); onClose() } catch (e) { toast(errorMessage(e), 'crit') } finally { setBusy(false) }
      }}>Block</Button></>}>
      <div className="space-y-3">
        <Field label="IP address"><Input className="mono" value={addr} onChange={(e) => setAddr(e.target.value)} /></Field>
        <Field label="Duration (minutes)" hint="Blocks always expire; maximum 90 days."><Input type="number" min={1} value={minutes} onChange={(e) => setMinutes(e.target.value)} /></Field>
        <Field label="Reason"><Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="SSH brute force" /></Field>
        <Note tone="warn">Make sure this is not your own address: blocking it could lock you out of the host.</Note>
      </div>
    </Modal>
  )
}
