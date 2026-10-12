import { useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, ShieldCheck } from 'lucide-react'
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

function b64ToBytes(b64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64.replace(/\s+/g, ''))
  const out = new Uint8Array(new ArrayBuffer(bin.length))
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function bytesToB64(b: Uint8Array): string {
  let s = ''
  for (const x of b) s += String.fromCharCode(x)
  return btoa(s)
}

/**
 * Signs the job spec bytes with an Ed25519 operator key (PKCS#8 PEM, as written by
 * `hb-sign keygen-operator`) using WebCrypto. Nothing is sent anywhere.
 */
export async function signSpec(keyText: string, specB64: string): Promise<string> {
  const body = keyText.replace(/-----(BEGIN|END)[^-]*-----/g, '').replace(/\s+/g, '')
  if (!body) throw new Error('That file does not contain a private key.')
  let key: CryptoKey
  try {
    key = await crypto.subtle.importKey('pkcs8', b64ToBytes(body), { name: 'Ed25519' }, false, ['sign'])
  } catch {
    throw new Error('Could not read the key. Use the PKCS#8 PEM file written by "hb-sign keygen-operator". This browser must also support Ed25519.')
  }
  const sig = await crypto.subtle.sign({ name: 'Ed25519' }, key, b64ToBytes(specB64))
  return bytesToB64(new Uint8Array(sig))
}

function readFile(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result || ''))
    r.onerror = () => reject(new Error('Could not read the file.'))
    r.readAsText(f)
  })
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return <div><div className="mb-1.5 text-xs font-medium text-muted">{label}</div>{children}</div>
}

/** Shown while a job waits for an operator signature. */
export function OperatorSignPanel({ job, onSigned }: { job: Job; onSigned?: () => void }) {
  const toast = useToast()
  const qc = useQueryClient()
  const file = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const [manual, setManual] = useState('')
  const spec = useQuery({ queryKey: ['job-spec', job.id], queryFn: () => get<SpecInfo>(`/jobs/${job.id}/spec`) })

  const submit = async (signature: string) => {
    await post(`/jobs/${job.id}/operator-signature`, { signature, key_id: '' })
    toast('Signature accepted: the job is queued')
    qc.invalidateQueries({ queryKey: ['job', job.id] })
    qc.invalidateQueries({ queryKey: ['jobs'] })
    onSigned?.()
  }
  const signInBrowser = async (f: File) => {
    if (!spec.data) return
    setBusy(true)
    setErr(null)
    try {
      const sig = await signSpec(await readFile(f), spec.data.spec)
      await submit(sig)
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
      if (file.current) file.current.value = ''
    }
  }
  const sendManual = async () => {
    setBusy(true)
    setErr(null)
    try {
      await submit(manual.trim())
      setManual('')
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const cmd = spec.data ? `hb-sign sign-job --key operator.key --spec ${spec.data.spec}` : ''

  return (
    <div className="space-y-3 rounded-xl border border-warn/30 bg-warn/10 p-4">
      <div className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4 text-warn" />Awaiting operator signature</div>
      <p className="text-sm text-muted">
        This host only runs jobs that an operator has signed. The job will not be sent until a valid signature is posted.
        {spec.data?.operator_key_ids?.length ? <> Accepted key(s): {spec.data.operator_key_ids.map((k) => <Badge key={k} className="mx-0.5 mono">{k}</Badge>)}</> : null}
      </p>
      {spec.isLoading ? <Spinner /> : spec.error ? <ErrorBox error={spec.error} /> : (
        <>
          <Section label="Option 1: sign on your own machine with the command line">
            <div className="flex items-start gap-2">
              <pre className="mono max-h-24 min-w-0 flex-1 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-panel-2 p-2.5 text-xs">{cmd}</pre>
              <CopyButton text={cmd} label="Copy" />
            </div>
          </Section>
          <Section label="Then paste the printed signature">
            <div className="flex gap-2">
              <Textarea className="mono min-h-10 flex-1 text-xs" rows={2} value={manual} onChange={(e) => setManual(e.target.value)} spellCheck={false} placeholder="base64 signature" />
              <Button disabled={!manual.trim()} loading={busy} onClick={sendManual}>Submit</Button>
            </div>
          </Section>
          <Section label="Option 2: sign in this browser">
            <div className="flex flex-wrap items-center gap-2">
              <input ref={file} type="file" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void signInBrowser(f) }} />
              <Button icon={<ShieldCheck className="size-4" />} loading={busy} onClick={() => file.current?.click()}>Sign in browser…</Button>
              <span className="text-xs text-muted">Choose your operator key file. It is read and used locally to sign the job; only the signature is sent to the server.</span>
            </div>
          </Section>
        </>
      )}
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
  if (host.write_policy === 'operator') return <Note>This host requires operator-signed jobs. After you start this action it will wait for your signature (command line or in the browser) before it is sent.</Note>
  return null
}
