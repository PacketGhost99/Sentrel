import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, ShieldOff } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, dateTime, until } from '@/lib/format'
import type { HostDetail, Series } from '@/lib/types'
import { Chart } from '@/components/Chart'
import { Badge, Button, Card, Empty, Field, Input, Loading, Modal, Note, Segmented, Table, Td, Th, useToast } from '@/components/ui'

interface SecData {
  failed_logins: { ip: string; total: number; last_seen: number; usernames: string[]; blocked: boolean }[]
  total: number
  blocks: { id: number; ip: string; reason: string; expires_at: number; status: string; created_by: string; created_at: number }[]
  fail2ban: { data: any; updated_at: number }
  allow_ip_block: boolean
  auto_block: boolean
  auto_block_threshold: number
  window_minutes: number
}

export function HostSecurity({ host }: { host: HostDetail }) {
  const [hours, setHours] = useState<'1' | '24' | '168'>('24')
  const { can } = useAuth()
  const qc = useQueryClient()
  const toast = useToast()
  const [blockIp, setBlockIp] = useState<string | null>(null)
  const d = useQuery({ queryKey: ['security', host.id, hours], queryFn: () => get<SecData>(`/hosts/${host.id}/security`, { hours }), refetchInterval: 30_000 })
  const chart = useQuery({ queryKey: ['metrics', host.id, 'auth', hours], queryFn: () => get<{ series: Series[] }>(`/hosts/${host.id}/metrics`, { panel: 'auth', start: `${Number(hours) * 3600}s` }) })
  if (d.isLoading) return <Loading />
  const x = d.data!
  const f2b = x.fail2ban?.data
  const unblock = async (ip: string) => {
    try {
      await post(`/hosts/${host.id}/unblock`, { ip })
      toast(`Unblocking ${ip}`)
      qc.invalidateQueries({ queryKey: ['security', host.id] })
    } catch (e) {
      toast(errorMessage(e), 'crit')
    }
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap gap-2 text-sm">
          <Badge tone={x.allow_ip_block ? 'ok' : 'muted'}>IP blocking {x.allow_ip_block ? 'allowed' : 'disabled'}</Badge>
          <Badge tone={x.auto_block ? 'warn' : 'muted'}>auto-block {x.auto_block ? `on (≥${x.auto_block_threshold} failures / ${x.window_minutes}m)` : 'off'}</Badge>
          <Badge>source: {host.agent_config ? 'agent log watcher' : '–'}</Badge>
        </div>
        <Segmented value={hours} onChange={setHours} options={[{ id: '1', label: '1h' }, { id: '24', label: '24h' }, { id: '168', label: '7d' }]} />
      </div>
      <Card title="Failed logins" subtitle="Most failures from a single IP within the detection window">
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
      <Card title="Firewall blocks" bodyClass="p-0" actions={can('jobs') && x.allow_ip_block && <Button size="sm" icon={<Ban className="size-3.5" />} onClick={() => setBlockIp('')}>Block an IP</Button>}>
        {!x.allow_ip_block && <div className="p-3"><Note>IP blocking is off for this host. Enable it in Settings → Remote actions. Blocks use nftables (or iptables / Windows Firewall) and always expire.</Note></div>}
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
        try { await post(`/hosts/${hostId}/block`, { ip: addr, minutes: Number(minutes), reason, confirm: true }); toast(`Blocking ${addr}`); onClose() } catch (e) { toast(errorMessage(e), 'crit') } finally { setBusy(false) }
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
