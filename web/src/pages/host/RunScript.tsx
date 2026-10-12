import { useState } from 'react'
import { useNavigate } from 'react-router'
import { errorMessage, post } from '@/lib/api'
import type { HostDetail, Job } from '@/lib/types'
import { Button, ErrorBox, Field, Input, Modal, Note, Select, Textarea } from '@/components/ui'

export function RunScript({ host, onClose }: { host: HostDetail; onClose: () => void }) {
  const win = host.host_info?.os === 'windows'
  const [interp, setInterp] = useState(win ? 'powershell' : 'sh')
  const [script, setScript] = useState(win ? 'Get-Service | Where-Object Status -eq Running | Select-Object -First 10' : 'uptime\ndf -h /')
  const [timeout, setTimeoutS] = useState('300')
  const [typed, setTyped] = useState('')
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const nav = useNavigate()
  return (
    <Modal open onClose={onClose} title={`Run a script on ${host.name}`} size="lg"
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="danger" loading={busy} disabled={typed !== host.name || !script.trim()} onClick={async () => {
        setBusy(true); setErr(null)
        try {
          const j = await post<Job>('/jobs', { host_id: host.id, type: 'script', confirm: true, timeout_seconds: Number(timeout), params: { interpreter: interp, script } })
          onClose(); nav(`/jobs/${j.id}`)
        } catch (e) { setErr(errorMessage(e)) } finally { setBusy(false) }
      }}>Run script</Button></>}>
      <div className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Interpreter">
            <Select value={interp} onChange={(e) => setInterp(e.target.value)}>
              {win ? <><option value="powershell">PowerShell</option><option value="cmd">cmd.exe</option></> : <><option value="sh">/bin/sh</option><option value="bash">bash</option><option value="python3">python3</option></>}
            </Select>
          </Field>
          <Field label="Timeout (seconds)"><Input type="number" min={10} max={14400} value={timeout} onChange={(e) => setTimeoutS(e.target.value)} /></Field>
        </div>
        <Field label="Script"><Textarea className="mono min-h-48 text-xs" value={script} onChange={(e) => setScript(e.target.value)} spellCheck={false} /></Field>
        <Note tone="warn">{win ? 'Runs as LocalSystem.' : 'Runs as the unprivileged heartbeat-agent user.'} The job is signed by the server, verified by the agent, and the script and its output are kept in the job log and audit trail.</Note>
        <Field label={<>Type <b className="mono text-fg">{host.name}</b> to confirm</>}><Input value={typed} onChange={(e) => setTyped(e.target.value)} /></Field>
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
