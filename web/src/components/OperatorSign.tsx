import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import type { HostDetail, Job } from '@/lib/types'
import { Badge, Button, CopyButton, ErrorBox, Note, Spinner, Textarea, useToast } from '@/components/ui'

interface SpecInfo {
  job_id: string
  status: string
  spec: string
  operator_key_ids: string[]
  expires_at: number
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return <div><div className="mb-1.5 text-xs font-medium text-muted">{label}</div>{children}</div>
}

function Block({ text }: { text: string }) {
  return (
    <div className="flex items-start gap-2">
      <pre className="mono max-h-24 min-w-0 flex-1 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-panel-2 p-2.5 text-xs">{text}</pre>
      <CopyButton text={text} label="Copy" />
    </div>
  )
}

/**
 * Shown while a job waits for an operator signature. Signing happens offline with
 * `sentrel-sign job`; the private key never enters a page this server serves.
 */
export function OperatorSignPanel({ job, onSigned }: { job: Job; onSigned?: () => void }) {
  const toast = useToast()
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const [manual, setManual] = useState('')
  const spec = useQuery({ queryKey: ['job-spec', job.id], queryFn: () => get<SpecInfo>(`/jobs/${job.id}/spec`) })

  const sendManual = async () => {
    setBusy(true)
    setErr(null)
    try {
      await post(`/jobs/${job.id}/operator-signature`, { signature: manual.trim(), key_id: '' })
      toast('Signature accepted: the job is queued')
      setManual('')
      qc.invalidateQueries({ queryKey: ['job', job.id] })
      qc.invalidateQueries({ queryKey: ['jobs'] })
      onSigned?.()
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const cmd = spec.data ? `sentrel-sign job -key operator.key -spec ${spec.data.spec}` : ''
  const params = Object.entries(job.params || {})

  return (
    <div className="space-y-3 rounded-xl border border-warn/30 bg-warn/10 p-4">
      <div className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4 text-warn" />Awaiting operator signature</div>
      <p className="text-sm text-muted">
        This host only runs jobs that an operator has signed. The job will not be sent until a valid signature is posted.
        {spec.data?.operator_key_ids?.length ? <> Accepted key(s): {spec.data.operator_key_ids.map((k) => <Badge key={k} className="mx-0.5 mono">{k}</Badge>)}</> : null}
      </p>
      <Section label="What this server says the job is (unverified: sentrel-sign shows what is actually in the bytes)">
        <div className="mono rounded-lg bg-panel-2 p-2.5 text-xs">
          <div>job id: {job.id}</div>
          <div>type: {job.type}{job.dry_run ? ' (dry run)' : ''}</div>
          {params.map(([k, v]) => <div key={k} className="break-all">{k} = {v}</div>)}
          {job.expires_at ? <div>expires: {new Date(job.expires_at * 1000).toLocaleString()}</div> : null}
        </div>
      </Section>
      {spec.isLoading ? <Spinner /> : spec.error ? <ErrorBox error={spec.error} /> : (
        <>
          <Section label="1. Job spec to sign (base64)"><Block text={spec.data?.spec || ''} /></Section>
          <Section label="2. On your own machine, run this. It decodes the spec, shows every field and asks you to type yes">
            <Block text={cmd} />
          </Section>
          <Section label="3. Paste the printed signature">
            <div className="flex gap-2">
              <Textarea className="mono min-h-10 flex-1 text-xs" rows={2} value={manual} onChange={(e) => setManual(e.target.value)} spellCheck={false} placeholder="base64 signature" />
              <Button disabled={!manual.trim()} loading={busy} onClick={sendManual}>Submit</Button>
            </div>
          </Section>
        </>
      )}
      <p className="text-xs text-muted">Signing happens offline, so this server never sees your key. Do not upload or paste the private key here.</p>
      <ErrorBox error={err} />
    </div>
  )
}

/** Badge for the host header: how this host's agent treats write jobs. */
export function WritePolicyBadge({ host }: { host: Pick<HostDetail, 'write_policy' | 'operator_key_ids'> }) {
  if (host.write_policy === 'none') return <Badge tone="info" title="This agent refuses every job that changes the host.">Read-only agent</Badge>
  if (host.write_policy === 'operator') {
    return <Badge tone="info" title={`Jobs that change the host need an operator signature. Key(s): ${(host.operator_key_ids || []).join(', ') || 'none reported'}`}>Operator-signed jobs</Badge>
  }
  return null
}

/** Explains why write actions are unavailable or need a signature. */
export function WritePolicyNote({ host }: { host: Pick<HostDetail, 'write_policy'> }) {
  if (host.write_policy === 'none') return <Note tone="warn">This host's agent is read-only: it was installed to refuse every action that changes the host, so this cannot run. Reinstall the agent with a different write policy to change that.</Note>
  if (host.write_policy === 'operator') return <Note>This host requires operator-signed jobs. After you start this action it will wait for your signature (signed offline with the command line tool) before it is sent.</Note>
  return null
}
