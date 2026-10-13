import { Fragment, useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { FileClock } from 'lucide-react'
import { get } from '@/lib/api'
import { dateTime } from '@/lib/format'
import type { AuditEntry } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Empty, Input, Loading, PageHeader, Table, Td, Th } from '@/components/ui'

function tone(action: string): 'crit' | 'warn' | 'ok' | 'info' | 'muted' {
  if (/failed|locked|deleted|revoked|bad_token/.test(action)) return 'crit'
  if (/job\.|block|upgrade|settings/.test(action)) return 'warn'
  if (/login|created|enrolled/.test(action)) return 'ok'
  return 'muted'
}

export function Audit() {
  const { tenant } = useTenantFilter()
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<number | null>(null)
  const a = useInfiniteQuery({
    queryKey: ['audit', tenant, q],
    queryFn: ({ pageParam }) => get<AuditEntry[]>('/audit', { q, tenant_id: tenant ?? undefined, limit: 100, before: pageParam || undefined }),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.length === 100 ? last[last.length - 1].id : undefined),
  })
  const rows = a.data?.pages.flat() || []
  return (
    <div>
      <PageHeader title="Audit log" info="Who changed what: logins, settings, upgrades, blocks, acknowledgements and every job." />
      <Card bodyClass="p-0">
        <div className="border-b border-line/70 p-4"><Input placeholder="Search actions, users, targets, details…" value={q} onChange={(e) => setQ(e.target.value)} className="max-w-md" /></div>
        {a.isLoading ? <Loading /> : !rows.length ? <Empty title="No entries" icon={<FileClock className="size-8" />} /> : (
          <Table>
            <thead><tr><Th>Time</Th><Th>Actor</Th><Th>Action</Th><Th className="hidden md:table-cell">Target</Th><Th className="hidden lg:table-cell">IP</Th></tr></thead>
            <tbody>{rows.map((r) => (
              <Fragment key={r.id}>
                <tr className="cursor-pointer" onClick={() => setOpen(open === r.id ? null : r.id)}>
                  <Td className="whitespace-nowrap text-xs">{dateTime(r.created_at)}</Td>
                  <Td className="text-xs">{r.actor}</Td>
                  <Td><Badge tone={tone(r.action)}>{r.action}</Badge></Td>
                  <Td className="hidden text-xs text-muted md:table-cell">{r.target_type}{r.target_id ? ` ${r.target_id}` : ''}</Td>
                  <Td className="mono hidden text-xs text-muted lg:table-cell">{r.ip}</Td>
                </tr>
                {open === r.id && <tr><Td colSpan={5}><pre className="mono overflow-x-auto rounded bg-panel-2 p-2 text-xs">{JSON.stringify(r.details, null, 2)}</pre></Td></tr>}
              </Fragment>
            ))}</tbody>
          </Table>
        )}
        {a.hasNextPage && <div className="p-3 text-center"><Button loading={a.isFetchingNextPage} onClick={() => a.fetchNextPage()}>Load more</Button></div>}
      </Card>
    </div>
  )
}
