import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, Ban, PlayCircle } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, dateTime, duration } from '@/lib/format'
import type { HostDetail, Job } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Empty, ErrorBox, KV, Loading, Note, PageHeader, Select, Spinner, Table, Tabs, Td, Th, statusTone, useConfirm, useToast } from '@/components/ui'
import { UpgradeDialog } from './host/Packages'
import { OperatorSignPanel } from '@/components/OperatorSign'
import { ScanSchedules, UpdateSchedules } from '@/components/Schedules'

const jobLabel: Record<string, string> = {
  upgrade: 'Package upgrade', refresh_inventory: 'Refresh inventory', block_ip: 'Block IP', unblock_ip: 'Unblock IP', self_update: 'Agent update', script: 'script (removed)', collect_now: 'Collect now', disk_usage: 'Disk usage analysis', disk_cleanup: 'Disk cleanup',
}

export function statusLabel(s: string): string {
  return s === 'awaiting_signature' ? 'Awaiting operator signature' : s
}

export function jobSummary(j: Job): string {
  const p = j.params || {}
  switch (j.type) {
    case 'upgrade':
      return p.packages ? `packages: ${p.packages}` : p.security_only === 'true' ? 'security updates' : 'all updates'
    case 'block_ip':
      return `${p.ip} for ${p.minutes}m`
    case 'unblock_ip':
      return p.ip
    case 'self_update':
      return p.version ? `to ${p.version}` : ''
    case 'disk_usage':
      return p.path || ''
    case 'disk_cleanup':
      return p.tasks ? `tasks: ${p.tasks}` : ''
  }
  return ''
}

export function Jobs() {
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') === 'schedules' ? 'schedules' : 'history'
  const setTab = (t: 'history' | 'schedules') => setParams(t === 'history' ? {} : { tab: t }, { replace: true })
  return (
    <div>
      <PageHeader title="Jobs" info="Remote actions sent to agents: upgrades, disk cleanup, firewall blocks and agent updates. Every job is signed by the server and verified by the agent." />
      <Tabs value={tab} onChange={setTab} className="mb-4" tabs={[{ id: 'history', label: 'History' }, { id: 'schedules', label: 'Schedules' }]} />
      {tab === 'history' ? <JobsTable /> : (
        <div className="space-y-4">
          <UpdateSchedules />
          <ScanSchedules />
        </div>
      )}
    </div>
  )
}

export function JobsTable({ hostId }: { hostId?: number }) {
  const { tenant } = useTenantFilter()
  const [type, setType] = useState('')
  const [status, setStatus] = useState('')
  const jobs = useQuery({ queryKey: ['jobs', hostId, tenant, type, status], queryFn: () => get<Job[]>('/jobs', { host_id: hostId, tenant_id: hostId ? undefined : tenant ?? undefined, type, status, limit: 300 }), refetchInterval: 10_000 })
  return (
    <Card bodyClass="p-0">
      <div className="flex flex-wrap gap-2 border-b border-line/70 p-4">
        <div className="w-44"><Select value={type} onChange={(e) => setType(e.target.value)}><option value="">All types</option>{Object.entries(jobLabel).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</Select></div>
        <div className="w-40"><Select value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="">Any status</option>{['awaiting_signature', 'scheduled', 'queued', 'sent', 'running', 'succeeded', 'failed', 'cancelled', 'expired', 'rejected'].map((s) => <option key={s}>{s}</option>)}
        </Select></div>
      </div>
      {jobs.isLoading ? <Loading /> : !jobs.data?.length ? <Empty title="No jobs" icon={<PlayCircle className="size-8" />} /> : (
        <Table>
          <thead><tr><Th>Job</Th>{!hostId && <Th className="hidden md:table-cell">Host</Th>}<Th>Status</Th><Th className="hidden lg:table-cell">Requested by</Th><Th>Created</Th><Th className="hidden md:table-cell">Duration</Th></tr></thead>
          <tbody>
            {jobs.data.map((j) => (
              <tr key={j.id}>
                <Td><Link to={`/jobs/${j.id}`} className="font-medium hover:underline">{jobLabel[j.type] || j.type}</Link>{j.dry_run && <Badge className="ml-1">dry run</Badge>}<div className="max-w-xs truncate text-xs text-muted">{jobSummary(j)}</div></Td>
                {!hostId && <Td className="hidden text-xs md:table-cell"><Link to={`/hosts/${j.host_id}`} className="hover:underline">{j.host_name}</Link></Td>}
                <Td><Badge tone={statusTone(j.status)}>{statusLabel(j.status)}</Badge>{j.schedule !== 'now' && j.status === 'scheduled' && <div className="text-[11px] text-muted">{j.schedule === 'maintenance' ? 'next maintenance window' : dateTime(j.run_after)}</div>}</Td>
                <Td className="hidden text-xs text-muted lg:table-cell">{j.requested_by}</Td>
                <Td className="text-xs text-muted" title={dateTime(j.created_at)}>{ago(j.created_at)}</Td>
                <Td className="hidden text-xs text-muted md:table-cell">{j.finished_at && j.started_at ? duration(j.finished_at - j.started_at) : j.status === 'running' ? <Spinner className="size-3.5" /> : '–'}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  )
}

export function JobDetail() {
  const { id = '' } = useParams()
  const { can } = useAuth()
  const toast = useToast()
  const confirm = useConfirm()
  const nav = useNavigate()
  const [real, setReal] = useState(false)
  const job = useQuery({ queryKey: ['job', id], queryFn: () => get<Job>(`/jobs/${id}`), refetchInterval: (q) => (['succeeded', 'failed', 'cancelled', 'expired', 'rejected'].includes(q.state.data?.status || '') ? false : 2000) })
  const host = useQuery({ queryKey: ['host', String(job.data?.host_id)], queryFn: () => get<HostDetail>(`/hosts/${job.data!.host_id}`), enabled: !!job.data && real })
  const out = useRef<HTMLPreElement>(null)
  useEffect(() => {
    if (out.current) out.current.scrollTop = out.current.scrollHeight
  }, [job.data?.output])
  if (job.isLoading) return <Loading />
  if (job.error) return <ErrorBox error={job.error} />
  const j = job.data!
  const done = ['succeeded', 'failed', 'cancelled', 'expired', 'rejected'].includes(j.status)
  let changes: { name: string; from: string; to: string }[] = []
  try { changes = j.result_json ? JSON.parse(j.result_json).changes || [] : [] } catch { /* ignore */ }
  return (
    <div>
      <PageHeader back={<Link to="/jobs" className="mb-1 inline-flex items-center gap-1 text-xs text-muted hover:text-fg"><ArrowLeft className="size-3" />Jobs</Link>}
        title={<span className="flex items-center gap-2">{jobLabel[j.type] || j.type}{j.dry_run && <Badge>dry run</Badge>}<Badge tone={statusTone(j.status)}>{statusLabel(j.status)}</Badge></span>}
        subtitle={<>on <Link to={`/hosts/${j.host_id}`} className="text-accent hover:underline">{j.host_name}</Link> · {jobSummary(j)}</>}
        actions={<>
          {can('jobs') && ['queued', 'scheduled', 'awaiting_signature'].includes(j.status) && <Button icon={<Ban className="size-4" />} onClick={async () => {
            if (!(await confirm({ title: 'Cancel job', confirmLabel: 'Cancel job', danger: true, body: <p>Cancel this job before it runs?</p> }))) return
            try { await post(`/jobs/${j.id}/cancel`); toast('Job cancelled'); job.refetch() } catch (e) { toast(errorMessage(e), 'crit') }
          }}>Cancel</Button>}
          {can('jobs') && j.type === 'upgrade' && j.dry_run && j.status === 'succeeded' && <Button variant="danger" onClick={() => setReal(true)}>Run for real…</Button>}
        </>} />
      <div className="grid gap-4 xl:grid-cols-3">
        <div className="space-y-4 xl:col-span-2">
          {j.error && <ErrorBox error={j.error} />}
          {j.status === 'awaiting_signature' && can('jobs') && <OperatorSignPanel job={j} />}
          {j.status === 'awaiting_signature' && !can('jobs') && <Note tone="warn">Awaiting operator signature.</Note>}
          {changes.length > 0 && (
            <Card title={`${changes.length} package(s) would change`} bodyClass="p-0">
              <Table className="max-h-72 overflow-y-auto">
                <thead><tr><Th>Package</Th><Th>From</Th><Th>To</Th></tr></thead>
                <tbody>{changes.map((c) => <tr key={c.name}><Td className="mono text-xs">{c.name}</Td><Td className="mono text-xs text-muted">{c.from}</Td><Td className="mono text-xs text-ok">{c.to}</Td></tr>)}</tbody>
              </Table>
            </Card>
          )}
          <Card title="Output" actions={!done && <span className="flex items-center gap-2 text-xs text-muted"><Spinner className="size-3.5" />{j.status === 'awaiting_signature' ? 'waiting for operator signature' : j.status === 'scheduled' ? 'waiting for schedule' : j.status === 'queued' ? 'waiting for agent' : 'live'}</span>}>
            <pre ref={out} className="mono max-h-[60vh] min-h-48 overflow-auto whitespace-pre-wrap break-all rounded-xl bg-[#0b0f17] p-4 text-xs text-[#d6deeb]">{j.output || (done ? '(no output)' : '…')}</pre>
          </Card>
        </div>
        <Card title="Details" className="h-full">
          <KV items={[
            ['ID', <span key="i" className="mono text-xs">{j.id}</span>],
            ['Status', statusLabel(j.status)],
            ...(j.operator_key_id ? [['Operator key', <span key="ok" className="mono text-xs">{j.operator_key_id}</span>] as [string, React.ReactNode]] : []),
            ['Exit code', j.exit_code ?? '–'],
            ['Requested by', j.requested_by],
            ['Confirmed by', j.confirmed_by || '–'],
            ['Schedule', j.schedule === 'now' ? 'immediately' : j.schedule === 'at' ? dateTime(j.run_after) : 'next maintenance window'],
            ['Created', dateTime(j.created_at)],
            ['Sent', j.sent_at ? dateTime(j.sent_at) : '–'],
            ['Started', j.started_at ? dateTime(j.started_at) : '–'],
            ['Finished', j.finished_at ? dateTime(j.finished_at) : '–'],
            ['Timeout', duration(j.timeout_seconds)],
            ['Expires', dateTime(j.expires_at)],
            ...Object.entries(j.params || {}).filter(([k]) => k !== 'script').map(([k, v]) => [k, <span key={k} className="mono break-all text-xs">{v}</span>] as [string, React.ReactNode]),
          ]} />
        </Card>
      </div>
      {real && host.data && <UpgradeDialog host={host.data} packages={j.params?.packages ? j.params.packages.split(',') : []} onClose={() => { setReal(false); nav('/jobs') }} />}
    </div>
  )
}
