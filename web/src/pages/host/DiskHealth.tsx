import { useQuery } from '@tanstack/react-query'
import { get } from '@/lib/api'
import { ago } from '@/lib/format'
import type { DiskHealthDevice, DiskHealthResp } from '@/lib/types'
import { Badge, Card, Empty, InfoTip, Table, Td, Th } from '@/components/ui'

const INFO = 'SMART data comes from smartctl (smartmontools), RAID from /proc/mdstat and ZFS from zpool. The agent collects it about every 30 minutes. Disk health is available for Linux agents only.'

function sectors(d: DiskHealthDevice): { text: string; bad: boolean } {
  const parts: string[] = []
  let bad = false
  const add = (label: string, v?: number) => {
    if (v === undefined) return
    if (v > 0) bad = true
    parts.push(`${v} ${label}`)
  }
  add('realloc.', d.reallocated_sectors)
  add('pending', d.pending_sectors)
  add('uncorr.', d.uncorrectable_sectors)
  return { text: parts.length ? parts.join(' · ') : '–', bad }
}

export function DiskHealthCard({ hostId }: { hostId: number }) {
  const q = useQuery({ queryKey: ['disk-health', hostId], queryFn: () => get<DiskHealthResp>(`/hosts/${hostId}/disk-health`), refetchInterval: 5 * 60_000 })
  const r = q.data
  const d = r?.data
  const rows = (d?.devices.length ?? 0) + (d?.raid.length ?? 0) + (d?.pools.length ?? 0)
  return (
    <Card className="xl:col-span-2" title="Disk health" bodyClass="p-0" info={INFO} subtitle={r?.known ? `Updated ${ago(r.updated_at)}` : undefined}>
      {!r ? <Empty title={q.isError ? 'Disk health could not be loaded' : 'Loading disk health'} /> : !r.supported ? (
        <p className="px-4 py-3 text-sm text-muted">Disk health is not available for Windows hosts yet.</p>
      ) : !r.known ? (
        <Empty title="No disk health data yet">The agent reports disk health a couple of minutes after it starts, then every 30 minutes.</Empty>
      ) : (
        <>
          {!d!.smart_available && (
            <p className="flex items-center gap-1.5 px-4 py-3 text-sm text-muted">
              Install smartmontools to see SMART data
              <InfoTip>SMART needs the smartctl command (package smartmontools, version 7.0 or newer). Once installed, the next report includes it. {d!.smart_reason ? `Agent says: ${d!.smart_reason}.` : ''}</InfoTip>
            </p>
          )}
          {rows > 0 && (
            <Table>
              <thead><tr><Th>Device</Th><Th className="hidden md:table-cell">Model</Th><Th>Health</Th><Th>Temp</Th><Th className="hidden sm:table-cell">Power-on</Th><Th>Sectors</Th><Th>Wear</Th></tr></thead>
              <tbody>
                {d!.devices.map((x) => {
                  const s = sectors(x)
                  return (
                    <tr key={x.device} title={x.serial ? `Serial ${x.serial}` : undefined}>
                      <Td className="mono text-xs">{x.device}</Td>
                      <Td className="hidden max-w-56 truncate text-xs text-muted md:table-cell">{x.model || '–'}</Td>
                      <Td>{x.health === 'passed' ? <Badge tone="ok">Passed</Badge> : x.health === 'failed' ? <Badge tone="crit">Failed</Badge> : <Badge tone="muted" title={x.error}>Unknown</Badge>}</Td>
                      <Td className="tabular-nums">{x.temperature_c !== undefined ? `${x.temperature_c} °C` : '–'}</Td>
                      <Td className="hidden text-xs sm:table-cell">{x.power_on_hours !== undefined ? `${x.power_on_hours.toLocaleString()} h` : '–'}</Td>
                      <Td className={`text-xs ${s.bad ? 'font-medium text-warn' : ''}`}>{s.text}</Td>
                      <Td className="text-xs">{x.percentage_used !== undefined ? <span className={x.percentage_used >= 90 ? 'font-medium text-warn' : ''}>{x.percentage_used}% used{x.media_errors ? ` · ${x.media_errors} media errors` : ''}</span> : '–'}</Td>
                    </tr>
                  )
                })}
                {d!.raid.map((a) => (
                  <tr key={a.name}>
                    <Td className="mono text-xs">{a.name}</Td>
                    <Td className="hidden text-xs text-muted md:table-cell">RAID {a.level.replace(/^raid/, '') || '?'}</Td>
                    <Td><Badge tone={a.failed ? 'crit' : a.degraded ? 'warn' : 'ok'}>{a.failed ? 'Failed' : a.degraded ? 'Degraded' : 'Healthy'}</Badge></Td>
                    <Td className="text-xs text-muted" colSpan={4}>
                      {a.working}/{a.members} members working{a.failed_devices ? `, ${a.failed_devices} failed` : ''}
                      {a.sync_action ? ` · ${a.sync_action} ${(a.sync_percent ?? 0).toFixed(1)}%${a.sync_finish ? `, ${a.sync_finish} left` : ''}` : ''}
                    </Td>
                  </tr>
                ))}
                {d!.pools.map((p) => (
                  <tr key={p.name}>
                    <Td className="mono text-xs">{p.name}</Td>
                    <Td className="hidden text-xs text-muted md:table-cell">ZFS pool</Td>
                    <Td><Badge tone={p.health.toUpperCase() === 'ONLINE' ? 'ok' : 'crit'}>{p.health}</Badge></Td>
                    <Td className="text-xs text-muted" colSpan={4}>{p.alloc} used of {p.size}</Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
          {d!.zfs_status && <pre className="mono max-h-40 overflow-auto border-t border-line/70 px-4 py-3 text-xs text-muted">{d!.zfs_status}</pre>}
          {d!.zfs_error && <p className="px-4 py-3 text-xs text-warn">ZFS: {d!.zfs_error}</p>}
          {rows === 0 && d!.smart_available && <Empty title="No disks, RAID arrays or ZFS pools reported" />}
        </>
      )}
    </Card>
  )
}
