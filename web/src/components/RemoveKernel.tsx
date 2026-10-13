import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { FlaskConical, Trash2 } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { bytes } from '@/lib/format'
import { packagesMatchAbi, parseKernelDryRun, removalBlocker } from '@/lib/kernel'
import type { FleetKernelHost, HostDetail, Job } from '@/lib/types'
import { OperatorSignPanel, WritePolicyNote } from '@/components/OperatorSign'
import { Badge, Button, ErrorBox, Field, Input, Loading, Modal, Note, Spinner, useToast } from '@/components/ui'

const terminal = (s: string) => ['succeeded', 'failed', 'rejected', 'cancelled', 'expired'].includes(s)

/** Polls one job until it finishes. */
function useJob(id: string | null) {
  return useQuery({
    queryKey: ['job', id],
    queryFn: () => get<Job>(`/jobs/${id}`),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data && terminal(q.state.data.status) ? false : 2000),
  })
}

/**
 * Removes one old kernel from a host. A dry run lists the exact packages first;
 * only then can the host name be typed to confirm.
 */
export function RemoveKernelDialog({ host, abi, onClose }: { host: HostDetail; abi: string; onClose: () => void }) {
  const toast = useToast()
  const nav = useNavigate()
  const [dryId, setDryId] = useState<string | null>(null)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const dry = useJob(dryId)
  const job = dry.data
  const result = parseKernelDryRun(job?.result_json)
  const dryOk = job?.status === 'succeeded' && !!result && packagesMatchAbi(result.packages, abi)
  const readOnly = host.write_policy === 'none'

  const start = async (isDry: boolean) => {
    setBusy(true)
    setErr(null)
    try {
      const j = await post<Job>('/jobs', { host_id: host.id, type: 'remove_kernel', params: { abi }, dry_run: isDry, confirm: !isDry, schedule: 'now' })
      if (isDry) {
        setDryId(j.id)
        toast(j.status === 'awaiting_signature' ? 'Waiting for your operator signature' : 'Dry run started')
      } else {
        toast(j.status === 'awaiting_signature' ? 'Waiting for your operator signature' : `Removing kernel ${abi}`)
        onClose()
        nav(`/jobs/${j.id}`)
      }
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open onClose={onClose} title={`Remove kernel ${abi} from ${host.name}`} size="lg"
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button icon={<FlaskConical className="size-4" />} loading={busy && !dryId} disabled={readOnly || (!!dryId && !terminal(job?.status || ''))} onClick={() => start(true)}>{dryId ? 'Run dry run again' : 'Dry run'}</Button>
        <Button variant="danger" icon={<Trash2 className="size-4" />} loading={busy && !!dryId} disabled={!host.allow_upgrades || readOnly || !dryOk || typed !== host.name} onClick={() => start(false)}>Remove kernel {abi}</Button>
      </>}>
      <div className="space-y-4">
        <WritePolicyNote host={host} />
        <p className="text-sm text-muted">Run the dry run first to see the exact packages. The running and newest kernels are always kept.</p>
        {job?.status === 'awaiting_signature' && <OperatorSignPanel job={job} />}
        {dryId && !job && <Loading />}
        {job && job.status !== 'awaiting_signature' && !terminal(job.status) && <div className="flex items-center gap-2 text-sm text-muted"><Spinner />Dry run {job.status}…</div>}
        {job && terminal(job.status) && job.status !== 'succeeded' && (
          <Note tone="warn">Dry run {job.status}{job.error ? `: ${job.error}` : ''}.{job.output ? <pre className="mono mt-2 max-h-40 overflow-auto whitespace-pre-wrap text-xs">{job.output}</pre> : null}</Note>
        )}
        {job?.status === 'succeeded' && result && (
          <div>
            <div className="mb-1 text-sm font-medium">{result.packages.length} packages would be purged{result.bytes ? ` (about ${bytes(result.bytes)})` : ''}</div>
            <ul className="mono max-h-48 overflow-auto rounded-lg bg-panel-2 p-2.5 text-xs">{result.packages.map((p) => <li key={p}>{p}</li>)}</ul>
            {!packagesMatchAbi(result.packages, abi) && <Note tone="warn" className="mt-2">The package list does not match kernel {abi}; nothing will be removed from here.</Note>}
          </div>
        )}
        {job?.status === 'succeeded' && !result && <Note tone="warn">The dry run finished without a package list. Check the job output before removing anything.</Note>}
        {host.allow_upgrades ? (
          <Field label={<>After a successful dry run, type the host name <b className="mono text-fg">{host.name}</b> to enable removal</>}>
            <Input value={typed} onChange={(e) => setTyped(e.target.value)} disabled={!dryOk} />
          </Field>
        ) : <Note>Remote upgrades are disabled for this host (turn on Allow remote upgrades on the Packages tab). Only a dry run is possible.</Note>}
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}

interface BulkTarget { host: FleetKernelHost; abi: string; dryJob?: string }

/** Removes the old kernels of several hosts: dry runs first, then one confirmation. */
export function BulkRemoveKernelsDialog({ hosts, onClose }: { hosts: FleetKernelHost[]; onClose: () => void }) {
  const toast = useToast()
  const [targets, setTargets] = useState<BulkTarget[]>([])
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const [done, setDone] = useState<{ queued: number; waiting: number; failed: string[] } | null>(null)
  const eligible = hosts.filter((h) => !removalBlocker(h))
  const skipped = hosts.filter((h) => removalBlocker(h))
  const wanted = eligible.flatMap((h) => h.removable.map((r) => ({ host: h, abi: r.abi })))
  const ids = targets.map((t) => t.dryJob).filter(Boolean) as string[]
  const jobs = useQuery({
    queryKey: ['bulk-kernel-dry', ids],
    queryFn: () => Promise.all(ids.map((id) => get<Job>(`/jobs/${id}`))),
    enabled: ids.length > 0,
    refetchInterval: (q) => (q.state.data && q.state.data.every((j) => terminal(j.status) || j.status === 'awaiting_signature') ? false : 2500),
  })
  const byId = new Map((jobs.data || []).map((j) => [j.id, j]))
  const allDry = ids.length > 0 && ids.length === targets.length && (jobs.data || []).length === ids.length && (jobs.data || []).every((j) => terminal(j.status) || j.status === 'awaiting_signature')
  const okTargets = targets.filter((t) => {
    const j = t.dryJob ? byId.get(t.dryJob) : undefined
    const r = parseKernelDryRun(j?.result_json)
    if (t.host.write_policy === 'operator' && j?.status === 'awaiting_signature') return true // cannot be verified without a signature; the real job waits for one too
    return j?.status === 'succeeded' && !!r && packagesMatchAbi(r.packages, t.abi)
  })

  // One bulk request per ABI: a bulk job carries the same params for every host.
  const run = async (dry: boolean, list: { host: FleetKernelHost; abi: string }[]) => {
    const byAbi = new Map<string, FleetKernelHost[]>()
    for (const t of list) byAbi.set(t.abi, [...(byAbi.get(t.abi) || []), t.host])
    const out: BulkTarget[] = []
    const failed: string[] = []
    for (const [abi, hs] of byAbi) {
      const res = await post<{ host_id: number; job_id?: string; status?: string; error?: string }[]>('/jobs/bulk', {
        host_ids: hs.map((h) => h.host_id), type: 'remove_kernel', params: { abi }, dry_run: dry, confirm: !dry, schedule: 'now',
      })
      for (const r of res) {
        const host = hs.find((h) => h.host_id === r.host_id)!
        if (r.error) failed.push(`${host.host_name}: ${r.error}`)
        else out.push({ host, abi, dryJob: r.job_id })
      }
    }
    return { out, failed }
  }

  const startDry = async () => {
    setBusy(true)
    setErr(null)
    try {
      const { out, failed } = await run(true, wanted)
      setTargets(out)
      if (failed.length) setErr(failed.join('; '))
      toast('Dry runs started')
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const startReal = async () => {
    setBusy(true)
    setErr(null)
    try {
      const { out, failed } = await run(false, okTargets)
      const waiting = out.filter((t) => t.host.write_policy === 'operator').length
      setDone({ queued: out.length, waiting, failed })
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const needle = `${okTargets.length}`

  return (
    <Modal open onClose={onClose} title="Remove old kernels on all eligible hosts" size="lg"
      footer={done ? <Button variant="primary" onClick={onClose}>Close</Button> : <>
        <Button onClick={onClose}>Cancel</Button>
        <Button icon={<FlaskConical className="size-4" />} loading={busy && !targets.length} disabled={!wanted.length || (targets.length > 0 && !allDry)} onClick={startDry}>{targets.length ? 'Run dry runs again' : 'Dry run'}</Button>
        <Button variant="danger" icon={<Trash2 className="size-4" />} loading={busy && targets.length > 0} disabled={!allDry || !okTargets.length || typed !== needle} onClick={startReal}>Remove {okTargets.length} kernel{okTargets.length === 1 ? '' : 's'}</Button>
      </>}>
      <div className="space-y-4">
        <p className="text-sm text-muted">The running and newest kernels are kept; each host re-checks this. Hosts that need operator-signed jobs wait for your signature on the Jobs page.</p>
        {done ? (
          <div className="space-y-2 text-sm">
            <p>{done.queued} removal job{done.queued === 1 ? '' : 's'} created{done.waiting ? `, ${done.waiting} waiting for a signature` : ''}. <Link to="/jobs" className="text-accent hover:underline">Open Jobs</Link></p>
            {done.failed.map((f) => <p key={f} className="text-crit">{f}</p>)}
          </div>
        ) : (
          <>
            <ul className="divide-y divide-line/60 rounded-lg border border-line/70 text-sm">
              {eligible.map((h) => h.removable.map((r) => {
                const t = targets.find((x) => x.host.host_id === h.host_id && x.abi === r.abi)
                const j = t?.dryJob ? byId.get(t.dryJob) : undefined
                const res = parseKernelDryRun(j?.result_json)
                return (
                  <li key={h.host_id + r.abi} className="flex flex-wrap items-center gap-2 px-3 py-2">
                    <span className="font-medium">{h.host_name}</span><span className="mono text-xs">{r.abi}</span>
                    <span className="text-xs text-muted">{r.cve_count} CVEs</span>
                    <span className="ml-auto text-xs">
                      {!t ? <span className="text-muted">not checked</span>
                        : !j ? <Spinner />
                        : j.status === 'succeeded' ? <Badge tone="ok">{res ? `${res.packages.length} packages` : 'ok'}</Badge>
                        : j.status === 'awaiting_signature' ? <Badge tone="info">awaiting signature</Badge>
                        : terminal(j.status) ? <Badge tone="crit" title={j.error}>{j.status}</Badge>
                        : <Badge tone="muted">{j.status}</Badge>}
                    </span>
                  </li>
                )
              }))}
            </ul>
            {skipped.length > 0 && <Note tone="warn">Skipped: {skipped.map((h) => `${h.host_name} (${removalBlocker(h)})`).join(', ')}.</Note>}
            {allDry && okTargets.length > 0 && (
              <Field label={<>Type <b className="mono text-fg">{needle}</b> (the number of kernels to remove) to enable removal</>}>
                <Input value={typed} onChange={(e) => setTyped(e.target.value)} />
              </Field>
            )}
          </>
        )}
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
