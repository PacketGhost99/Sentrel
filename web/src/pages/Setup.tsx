import { useMemo, useState } from 'react'
import { Navigate, useNavigate } from 'react-router'
import { post } from '@/lib/api'
import { localZone, timezones } from '@/lib/format'
import { useAuth } from '@/lib/auth'
import { Button, Card, ErrorBox, Field, Input, Note, Select, Toggle, cx } from '@/components/ui'
import { AuthShell } from './Login'

const zones = timezones()

export function Setup() {
  const { info, refresh, loading } = useAuth()
  const nav = useNavigate()
  const [step, setStep] = useState(0)
  const [f, setF] = useState({
    token: '', username: 'admin', password: '', confirm: '', email: '',
    server_url: location.origin, tls_mode: 'http', hostname: location.hostname,
    acme_email: '', timezone: localZone(), trust_proxy: false, vuln_enabled: false,
  })
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<string | null>(null)
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value })

  const newURL = useMemo(() => {
    if (f.tls_mode === 'http') return f.server_url
    const port = location.port && f.tls_mode !== 'acme' ? ':' + location.port : ''
    return `https://${f.hostname}${port}`
  }, [f])

  if (!loading && info && !info.setup_required) return <Navigate to="/" replace />

  const pwErr = f.password && f.password.length < 10 ? 'At least 10 characters' : f.confirm && f.confirm !== f.password ? 'Passwords do not match' : ''
  const steps = ['Verify', 'Administrator', 'Server', 'Finish']

  const submit = async () => {
    setBusy(true)
    setErr(null)
    try {
      const server_url = f.tls_mode === 'http' ? f.server_url : newURL
      await post('/setup', { ...f, server_url })
      if (f.tls_mode !== 'http') {
        setDone(newURL)
        setTimeout(() => (location.href = newURL + '/'), 4000)
        return
      }
      await refresh()
      nav('/', { replace: true })
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <AuthShell>
        <Card>
          <Note tone="ok">Setup complete. The server is switching to HTTPS; you will be redirected to <b>{done}</b>.</Note>
          {f.tls_mode === 'selfsigned' && <p className="mt-3 text-sm text-muted">Your browser shows a certificate warning the first time because the certificate is self-signed.</p>}
        </Card>
      </AuthShell>
    )
  }

  return (
    <AuthShell wide>
      <Card>
        <ol className="mb-5 flex items-center gap-2 text-xs">
          {steps.map((s, i) => (
            <li key={s} className="flex items-center gap-2">
              <span className={cx('flex size-6 items-center justify-center rounded-full border text-[11px] font-semibold', i <= step ? 'border-accent bg-accent text-accent-fg' : 'border-line text-muted')}>{i + 1}</span>
              <span className={i === step ? 'font-medium' : 'text-muted'}>{s}</span>
              {i < steps.length - 1 && <span className="mx-1 h-px w-6 bg-line" />}
            </li>
          ))}
        </ol>

        {step === 0 && (
          <div className="space-y-4">
            <p className="text-sm text-muted">Welcome. Enter the one-time setup token printed when the server first started. It is also stored in the data directory as <code className="mono">setup-token</code>.</p>
            <Field label="Setup token">
              <Input autoFocus className="mono uppercase" value={f.token} onChange={set('token')} placeholder="XXXXXXXXXXXXXXXXXXXXXXXX" />
            </Field>
            <div className="flex justify-end"><Button variant="primary" disabled={f.token.length < 16} onClick={() => setStep(1)}>Continue</Button></div>
          </div>
        )}

        {step === 1 && (
          <div className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Admin username" required><Input value={f.username} onChange={set('username')} autoComplete="username" /></Field>
              <Field label="Email"><Input type="email" value={f.email} onChange={set('email')} /></Field>
              <Field label="Password" required error={f.password && f.password.length < 10 ? 'At least 10 characters' : undefined}>
                <Input type="password" value={f.password} onChange={set('password')} autoComplete="new-password" />
              </Field>
              <Field label="Confirm password" required error={f.confirm && f.confirm !== f.password ? 'Passwords do not match' : undefined}>
                <Input type="password" value={f.confirm} onChange={set('confirm')} autoComplete="new-password" />
              </Field>
            </div>
            <div className="flex justify-between">
              <Button onClick={() => setStep(0)}>Back</Button>
              <Button variant="primary" disabled={!f.username || !f.password || !!pwErr || f.password !== f.confirm} onClick={() => setStep(2)}>Continue</Button>
            </div>
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4">
            <Field label="How should the server be reached?">
              <div className="grid gap-2 sm:grid-cols-3">
                {[
                  ['http', 'Plain HTTP', 'Behind a reverse proxy that terminates TLS'],
                  ['selfsigned', 'Self-signed TLS', 'LAN use; certificate from the internal CA, pinned by agents'],
                  ['acme', "Let's Encrypt", 'Public DNS name pointing here, ports 80 and 443 open'],
                ].map(([id, t, d]) => (
                  <button key={id} type="button" onClick={() => setF({ ...f, tls_mode: id })}
                    className={cx('rounded-xl border p-4 text-left transition-colors', f.tls_mode === id ? 'border-accent bg-accent/8' : 'border-line hover:border-muted')}>
                    <div className="text-sm font-medium">{t}</div>
                    <div className="mt-1 text-xs text-muted">{d}</div>
                  </button>
                ))}
              </div>
            </Field>
            {f.tls_mode === 'http' ? (
              <>
                <Field label="Public server URL" hint="e.g. https://monitor.example.com" info="The address agents and links in notifications use.">
                  <Input value={f.server_url} onChange={set('server_url')} />
                </Field>
                <Toggle checked={f.trust_proxy} onChange={(v) => setF({ ...f, trust_proxy: v })} label="Behind a reverse proxy"
                  info="Use X-Forwarded-For and X-Forwarded-Proto for client IPs and HTTPS detection." />
              </>
            ) : (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="Hostname" hint={f.tls_mode === 'acme' ? 'Must resolve to this server' : 'Name or IP the certificate is issued for'}>
                  <Input value={f.hostname} onChange={set('hostname')} />
                </Field>
                {f.tls_mode === 'acme' && <Field label="Contact email (Let's Encrypt)"><Input type="email" value={f.acme_email} onChange={set('acme_email')} /></Field>}
                <div className="sm:col-span-2"><Note>The server will move to <b>{newURL}</b> after setup.</Note></div>
              </div>
            )}
            <Field label="Timezone" info="Used for maintenance windows and schedules.">
              <Select value={f.timezone} onChange={set('timezone')}>
                {zones.map((z) => <option key={z}>{z}</option>)}
              </Select>
            </Field>
            <Toggle checked={f.vuln_enabled} onChange={(v) => setF({ ...f, vuln_enabled: v })} label="Enable vulnerability (CVE) scanning"
              hint="Optional. Needs 2 GB of RAM and a database download of about 3 GB." info="The database downloads in the background. Leave this off on a Raspberry Pi or a 1 GB server; you can turn it on later in Settings." />
            <div className="flex justify-between">
              <Button onClick={() => setStep(1)}>Back</Button>
              <Button variant="primary" onClick={() => setStep(3)}>Continue</Button>
            </div>
          </div>
        )}

        {step === 3 && (
          <div className="space-y-4">
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
              <dt className="text-muted">Administrator</dt><dd>{f.username}</dd>
              <dt className="text-muted">Server URL</dt><dd className="break-all">{f.tls_mode === 'http' ? f.server_url : newURL}</dd>
              <dt className="text-muted">TLS</dt><dd>{f.tls_mode === 'http' ? 'Plain HTTP' : f.tls_mode === 'selfsigned' ? 'Self-signed (internal CA)' : "Let's Encrypt"}</dd>
              <dt className="text-muted">Timezone</dt><dd>{f.timezone}</dd>
              <dt className="text-muted">CVE scanning</dt><dd>{f.vuln_enabled ? 'Enabled (downloads about 3 GB)' : 'Off'}</dd>
            </dl>
            <ErrorBox error={err} />
            <div className="flex justify-between">
              <Button onClick={() => setStep(2)}>Back</Button>
              <Button variant="primary" loading={busy} onClick={submit}>Complete setup</Button>
            </div>
          </div>
        )}
      </Card>
    </AuthShell>
  )
}
