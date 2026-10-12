import { useState } from 'react'
import { KeyRound, ShieldCheck } from 'lucide-react'
import { errorMessage, post } from '@/lib/api'
import { createCredential, webauthnSupported } from '@/lib/webauthn'
import { Button, CodeBlock, ErrorBox, Field, Input, Note } from './ui'

/** TOTP enrolment: QR code, confirmation code, recovery codes. */
export function TotpSetup({ onDone }: { onDone: () => void }) {
  const [setup, setSetup] = useState<{ secret: string; otpauth_url: string; qr_data_url: string } | null>(null)
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[] | null>(null)
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  if (codes) {
    return (
      <div className="space-y-3">
        <Note tone="ok">Authenticator app enabled. Store these one-time recovery codes somewhere safe; each works once if you lose your device.</Note>
        <CodeBlock code={codes.join('\n')} />
        <Button variant="primary" onClick={onDone}>Done</Button>
      </div>
    )
  }
  if (!setup) {
    return (
      <div className="space-y-3">
        <p className="text-sm text-muted">Use an authenticator app such as Aegis, 1Password, Google Authenticator or Microsoft Authenticator.</p>
        <ErrorBox error={err} />
        <Button variant="primary" loading={busy} icon={<ShieldCheck className="size-4" />} onClick={async () => {
          setBusy(true); setErr(null)
          try { setSetup(await post('/auth/totp/setup')) } catch (e) { setErr(e) } finally { setBusy(false) }
        }}>Set up authenticator app</Button>
      </div>
    )
  }
  return (
    <form className="space-y-3" onSubmit={async (e) => {
      e.preventDefault(); setBusy(true); setErr(null)
      try { const r = await post<{ recovery_codes: string[] }>('/auth/totp/enable', { code }); setCodes(r.recovery_codes) } catch (x) { setErr(x) } finally { setBusy(false) }
    }}>
      <div className="flex flex-wrap items-start gap-4">
        <img src={setup.qr_data_url} alt="TOTP QR code" className="size-44 rounded-md bg-white p-2" />
        <div className="min-w-0 flex-1 space-y-2 text-sm">
          <p>Scan the QR code, or enter this key manually:</p>
          <CodeBlock code={setup.secret} />
          <Field label="6-digit code from the app">
            <Input autoFocus inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="123456" />
          </Field>
        </div>
      </div>
      <ErrorBox error={err} />
      <Button type="submit" variant="primary" loading={busy} disabled={code.length < 6}>Verify and enable</Button>
    </form>
  )
}

/** Registers a passkey / security key for the current user. */
export function PasskeyRegister({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  if (!webauthnSupported()) return <Note tone="warn">This browser does not support passkeys.</Note>
  if (!window.isSecureContext) return <Note tone="warn">Passkeys need HTTPS (or localhost). Switch the server to TLS in Settings → TLS.</Note>
  return (
    <div className="flex flex-wrap items-end gap-2">
      <Field label="Name for this passkey" className="min-w-48 flex-1">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. YubiKey, MacBook Touch ID" />
      </Field>
      <Button variant="primary" loading={busy} icon={<KeyRound className="size-4" />} onClick={async () => {
        setBusy(true); setErr(null)
        try {
          const begin = await post<{ challenge_id: string; options: any }>('/auth/passkeys/register/begin')
          const cred = await createCredential(begin.options)
          await post('/auth/passkeys/register/finish', cred, { challenge_id: begin.challenge_id, name: name || 'Passkey' })
          onDone()
        } catch (e) { setErr(e instanceof DOMException ? new Error('Passkey registration was cancelled or failed: ' + e.message) : e) } finally { setBusy(false) }
      }}>Add passkey</Button>
      {err ? <ErrorBox error={errorMessage(err)} className="w-full" /> : null}
    </div>
  )
}
