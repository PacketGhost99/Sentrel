import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Eraser, FlaskConical, HardDrive, ScanSearch } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, bytes } from '@/lib/format'
import type { Alert, CleanupTask, DiskUsageReport, HostDetail, Job } from '@/lib/types'
import { OperatorSignPanel, WritePolicyNote } from '@/components/OperatorSign'
import { InheritableField, useHostSetting } from '@/components/InheritableField'
import { Badge, Button, Card, Checkbox, ErrorBox, Field, Input, Modal, Note, Select, Spinner, useToast } from '@/components/ui'

export const DISK_ALERT_METRICS = ['disk', 'inodes', 'disk_fill_hours']

const FINISHED = ['succeeded', 'failed', 'cancelled', 'expired', 'rejected']

/** Mount points known for a host, with a sensible default when the agent has not reported disks yet. */
export function hostMounts(host: HostDetail): string[] {
  const m = (host.state?.disks || []).map((d) => d.mountpoint).filter(Boolean)
  const uniq = Array.from(new Set(m))
  if (uniq.length) return uniq
  return [host.host_info?.os === 'windows' ? 'C:\\' : '/']
}

/** Firing disk-related alerts: the label holds the mount point. */
export function diskAlerts(alerts: Alert[]): Alert[] {
  return alerts.filter((a) => a.status !== 'resolved' && DISK_ALERT_METRICS.includes(a.metric))
}

function parseFreed(output?: string): number | null {
  const m = /freed_bytes=(\d+)/.exec(output || '')
  return m ? Number(m[1]) : null
}

// ---------- Report view ----------

export function StorageReport({ host, mount }: { host: HostDetail; mount: string }) {
  const { can } = useAuth()
  const toast = useToast()
  const qc = useQueryClient()
  const [cleanup, setCleanup] = useState(false)
  const saveSetting = useHostSetting(host.id)
  const canEdit = can('host_settings') || can('hosts')
  const key = ['disk-usage', host.id, mount]
  const report = useQuery({
    queryKey: key,
    queryFn: () => get<DiskUsageReport>(`/hosts/${host.id}/disk-usage`, { mount }),
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 3000 : false),
  })
  const [starting, setStarting] = useState(false)
  const r = report.data
  const running = r?.status === 'running' || starting
  const analyse = async () => {
    setStarting(true)
    try {
      await post(`/hosts/${host.id}/disk-usage`, { mount })
      toast('Analysis started')
      await qc.invalidateQueries({ queryKey: key })
    } catch (e) {
      toast(errorMessage(e), 'crit')
    } finally {
      setStarting(false)
    }
  }
  const entries = useMemo(() => [...(r?.entries || [])].sort((a, b) => b.bytes - a.bytes).slice(0, 15), [r?.entries])
  const files = useMemo(() => [...(r?.largest_files || [])].sort((a, b) => b.bytes - a.bytes).slice(0, 10), [r?.largest_files])
  const maxBytes = Math.max(1, ...entries.map((e) => e.bytes))
  const used = r?.used_bytes || 0
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="min-w-0 flex-1 text-xs text-muted">
          {r?.status === 'done' && <>Analysed {ago(r.created_at)} · {bytes(r.used_bytes)} used of {bytes(r.total_bytes)}</>}
          {running && <span className="flex items-center gap-2"><Spinner className="size-3.5" />Analysing {mount}. Large volumes can take a few minutes.</span>}
          {r?.status === 'none' && !running && 'No analysis has been run for this mount yet.'}
        </div>
        {can('jobs') && <Button size="sm" icon={<ScanSearch className="size-3.5" />} loading={running} onClick={analyse}>{r?.status === 'done' ? 'Analyse again' : 'Analyse'}</Button>}
        {can('jobs') && <Button size="sm" icon={<Eraser className="size-3.5" />} onClick={() => setCleanup(true)}>Clean up…</Button>}
      </div>
      {host.settings && (
        <div className="rounded-xl border border-line/70 px-4">
          <InheritableField label="Allow disk cleanup" description="Lets administrators remove caches, old logs and temporary files with Clean up. Every run needs a dry run and confirmation first." kind="bool" setting={host.settings.allow_cleanup} disabled={!canEdit} onSave={saveSetting('allow_cleanup')} />
        </div>
      )}
      {report.error ? <ErrorBox error={report.error} /> : null}
      {r?.status === 'failed' && <ErrorBox error={r.error || 'The analysis failed.'} />}
      {r?.status === 'done' && (
        <div className="grid gap-5 lg:grid-cols-2">
          <div>
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted">Largest directories</h4>
            {entries.length === 0 ? <p className="text-sm text-muted">Nothing to show.</p> : (
              <ul className="space-y-2.5">
                {entries.map((e) => {
                  const share = used > 0 ? (e.bytes * 100) / used : 0
                  return (
                    <li key={e.path} style={{ paddingLeft: Math.max(0, (e.depth || 1) - 1) * 12 }}>
                      <div className="flex items-baseline justify-between gap-3 text-sm">
                        <span className="mono min-w-0 truncate text-xs" title={e.path}>{e.path}</span>
                        <span className="shrink-0 tabular-nums text-xs">{bytes(e.bytes)} <span className="text-muted">{used > 0 ? `· ${share.toFixed(share < 10 ? 1 : 0)}%` : ''}</span></span>
                      </div>
                      <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-panel-2">
                        <div className="h-full rounded-full bg-accent/80" style={{ width: `${Math.max(1.5, (e.bytes * 100) / maxBytes)}%` }} />
                      </div>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
          <div>
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted">Largest files</h4>
            {files.length === 0 ? <p className="text-sm text-muted">Nothing to show.</p> : (
              <ul className="divide-y divide-line/60">
                {files.map((f) => (
                  <li key={f.path} className="flex items-baseline justify-between gap-3 py-1.5">
                    <span className="mono min-w-0 truncate text-xs" title={f.path}>{f.path}</span>
                    <span className="shrink-0 text-xs tabular-nums">{bytes(f.bytes)}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
      {cleanup && <CleanupDialog host={host} onClose={() => { setCleanup(false); qc.invalidateQueries({ queryKey: key }) }} />}
    </div>
  )
}

// ---------- Overview panel ----------

/** Storage breakdown card for the host overview. Opens by default while a disk alert is firing. */
export function StoragePanel({ host, alerts }: { host: HostDetail; alerts: Alert[] }) {
  const mounts = hostMounts(host)
  const firing = diskAlerts(alerts)
  const alertMount = firing.map((a) => a.labels).find((l) => mounts.includes(l))
  const [mount, setMount] = useState(alertMount || mounts[0])
  const [open, setOpen] = useState(firing.length > 0)
  useEffect(() => {
    if (firing.length > 0) setOpen(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [firing.length])
  if (host.kind !== 'agent') return null
  return (
    <Card title={<span className="flex items-center gap-2"><HardDrive className="size-4 text-muted" />Storage breakdown{firing.length > 0 && <Badge tone="warn">disk alert</Badge>}</span>}
      subtitle="Find what is using space on a volume"
      actions={<>
        {open && mounts.length > 1 && <div className="w-40"><Select value={mount} onChange={(e) => setMount(e.target.value)}>{mounts.map((m) => <option key={m} value={m}>{m}</option>)}</Select></div>}
        <Button size="sm" variant="ghost" onClick={() => setOpen(!open)}>{open ? 'Hide' : 'Show'}</Button>
      </>}
      bodyClass={open ? 'p-5' : 'hidden'}>
      {open && <StorageReport host={host} mount={mount} />}
    </Card>
  )
}

/** Embedded breakdown for the alert detail page (the alert label is the mount point). */
export function StorageForAlert({ hostId, mount }: { hostId: number; mount: string }) {
  const host = useQuery({ queryKey: ['host', String(hostId)], queryFn: () => get<HostDetail>(`/hosts/${hostId}`) })
  if (!host.data || host.data.kind !== 'agent') return null
  return (
    <Card className="mt-4" title={<span className="flex items-center gap-2"><HardDrive className="size-4 text-muted" />Storage breakdown for {mount}</span>}
      subtitle="Where the space is going on this volume">
      <StorageReport host={host.data} mount={mount} />
    </Card>
  )
}

// ---------- Cleanup dialog ----------

export function CleanupDialog({ host, onClose }: { host: HostDetail; onClose: () => void }) {
  const toast = useToast()
  const tasks = useQuery({ queryKey: ['cleanup-tasks', host.id], queryFn: () => get<CleanupTask[]>(`/hosts/${host.id}/disk-cleanup/tasks`) })
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const [run, setRun] = useState<{ id: string; dry: boolean; tasks: string } | null>(null)
  const [dryDone, setDryDone] = useState<string | null>(null)
  const list = tasks.data || []
  const chosen = list.filter((t) => sel.has(t.id)).map((t) => t.id).join(',')
  const job = useQuery({
    queryKey: ['job', run?.id],
    queryFn: () => get<Job>(`/jobs/${run!.id}`),
    enabled: !!run,
    refetchInterval: (q) => (FINISHED.includes(q.state.data?.status || '') ? false : 2000),
  })
  const j = job.data
  const finished = !!j && FINISHED.includes(j.status)
  useEffect(() => {
    if (run && run.dry && j && j.status === 'succeeded') setDryDone(run.tasks)
  }, [run, j])
  const start = async (dry: boolean) => {
    setBusy(true)
    setErr(null)
    try {
      const created = await post<Job>('/jobs', { host_id: host.id, type: 'disk_cleanup', params: { tasks: chosen }, dry_run: dry, confirm: !dry, schedule: 'now' })
      setRun({ id: created.id, dry, tasks: chosen })
      if (!dry) toast('Cleanup started')
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const freed = parseFreed(j?.output)
  const dryOk = dryDone === chosen && chosen !== ''
  const running = !!run && !finished
  return (
    <Modal open onClose={onClose} title={`Clean up disk space on ${host.name}`} size="lg"
      footer={<>
        <Button onClick={onClose}>Close</Button>
        <Button icon={<FlaskConical className="size-4" />} loading={busy || (running && !!run?.dry)} disabled={!host.allow_cleanup || host.write_policy === 'none' || !chosen || running} onClick={() => start(true)}>Dry run</Button>
        <Button variant="danger" loading={busy || (running && !run?.dry)} disabled={!host.allow_cleanup || host.write_policy === 'none' || !dryOk || typed !== host.name || running} onClick={() => start(false)}>Run cleanup</Button>
      </>}>
      <div className="space-y-4">
        <WritePolicyNote host={host} />
        {!host.allow_cleanup && (
          <Note tone="warn">Disk cleanup is turned off for this host. Turn on <b>Allow disk cleanup</b> in the Storage breakdown on the Overview tab, then come back here.</Note>
        )}
        <div>
          <div className="mb-2 flex items-center justify-between gap-2">
            <div className="text-xs font-medium text-muted">Choose what to clean</div>
            {list.length > 0 && (
              <div className="flex gap-1">
                <Button size="sm" variant="ghost" onClick={() => { setSel(new Set(list.map((t) => t.id))); setDryDone(null) }}>Select all</Button>
                <Button size="sm" variant="ghost" disabled={sel.size === 0} onClick={() => { setSel(new Set()); setDryDone(null) }}>Clear</Button>
              </div>
            )}
          </div>
          {tasks.isLoading ? <Spinner /> : list.length === 0 ? <p className="text-sm text-muted">No cleanup tasks are available for this host.</p> : (
            <ul className="divide-y divide-line/60 rounded-xl border border-line/70">
              {list.map((t) => (
                <li key={t.id} className="flex items-start gap-3 px-4 py-3">
                  <Checkbox checked={sel.has(t.id)} onChange={(v) => { const s = new Set(sel); if (v) s.add(t.id); else s.delete(t.id); setSel(s); setDryDone(null) }} />
                  <div className="min-w-0">
                    <div className="text-sm font-medium">{t.name}</div>
                    <div className="text-xs text-muted">{t.description}</div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
        {j?.status === 'awaiting_signature' && <OperatorSignPanel job={j} />}
        {run && j?.status !== 'awaiting_signature' && (
          <div>
            <div className="mb-1.5 flex items-center gap-2 text-xs font-medium text-muted">
              {run.dry ? 'Dry run' : 'Cleanup'} {finished ? (j!.status === 'succeeded' ? 'finished' : j!.status) : <><Spinner className="size-3.5" />running</>}
              {freed !== null && <Badge tone={run.dry ? 'info' : 'ok'}>{run.dry ? 'would free' : 'freed'} {bytes(freed)}</Badge>}
            </div>
            <pre className="mono max-h-60 min-h-16 overflow-auto whitespace-pre-wrap break-all rounded-xl bg-[#0b0f17] p-3 text-xs text-[#d6deeb]">{j?.output || (finished ? '(no output)' : '…')}</pre>
            {j?.error && <ErrorBox error={j.error} className="mt-2" />}
          </div>
        )}
        {host.allow_cleanup && (
          <Field label={<>Type the host name <b className="mono text-fg">{host.name}</b> to enable the cleanup button</>}
            hint={dryOk ? undefined : 'Run a dry run for your selection first to see what would be removed.'}>
            <Input value={typed} onChange={(e) => setTyped(e.target.value)} />
          </Field>
        )}
        <Note tone="warn">Cleanup permanently deletes files such as caches, old logs and temporary data. Every run is recorded in the job log and audit trail.</Note>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
