import { useEffect, useState } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router'
import { Fingerprint, KeyRound, LogIn } from 'lucide-react'
import { post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { getAssertion, webauthnSupported } from '@/lib/webauthn'
import { Logo } from '@/components/Layout'
import { Button, Card, ErrorBox, Field, Input, Note } from '@/components/ui'
import { PasskeyRegister, TotpSetup } from '@/components/TwoFactor'

export function AuthShell({ children, wide }: { children: React.ReactNode; wide?: boolean }) {
  const { info, me } = useAuth()
  const b = me?.branding || info?.branding
  return (
    <div className="flex min-h-full items-center justify-center bg-bg p-4">
      <div className={wide ? 'w-full max-w-2xl' : 'w-full max-w-sm'}>
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          {b?.logo_data_url ? <img src={b.logo_data_url} alt="" className="h-12 max-w-[220px] object-contain" /> : <Logo className="size-11" />}
          <h1 className="text-xl font-semibold tracking-tight">{b?.product_name || 'Sentrel'}</h1>
          {info?.tenant_name && <div className="text-sm text-muted">{info.tenant_name}</div>}
        </div>
        {children}
        {b?.footer_text && <p className="mt-6 text-center text-xs text-muted">{b.footer_text}</p>}
      </div>
    </div>
  )
}

export function Login() {
  const { me, info, refresh, loading } = useAuth()
  const nav = useNavigate()
  const [params] = useSearchParams()
  const next = params.get('next') || '/'
  const ssoError = params.get('sso_error')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const mfa = me?.mfa_pending
  const methods = me?.mfa_methods || []

  useEffect(() => {
    if (me && !me.mfa_pending) nav(next, { replace: true })
  }, [me, nav, next])

  if (!loading && info?.setup_required) return <Navigate to="/setup" replace />

  const finish = async () => {
    const m = await refresh()
    if (m && !m.mfa_pending) nav(next, { replace: true })
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await post('/auth/login', { username, password })
      await finish()
    } catch (x) {
      setErr(x)
    } finally {
      setBusy(false)
    }
  }

  const submitCode = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await post('/auth/mfa/totp', { code })
      await finish()
    } catch (x) {
      setErr(x)
    } finally {
      setBusy(false)
    }
  }

  const passkeyMfa = async () => {
    setBusy(true)
    setErr(null)
    try {
      const b = await post<{ challenge_id: string; options: any }>('/auth/mfa/passkey/begin')
      const a = await getAssertion(b.options)
      await post('/auth/mfa/passkey/finish', a, { challenge_id: b.challenge_id })
      await finish()
    } catch (x) {
      setErr(x instanceof DOMException ? new Error('Passkey cancelled') : x)
    } finally {
      setBusy(false)
    }
  }

  const passkeyLogin = async () => {
    setBusy(true)
    setErr(null)
    try {
      const b = await post<{ challenge_id: string; options: any }>('/auth/passkey/begin')
      const a = await getAssertion(b.options)
      await post('/auth/passkey/finish', a, { challenge_id: b.challenge_id })
      await finish()
    } catch (x) {
      setErr(x instanceof DOMException ? new Error('Passkey cancelled') : x)
    } finally {
      setBusy(false)
    }
  }

  const b = info?.branding
  return (
    <AuthShell>
      <Card>
        {!mfa ? (
          <form onSubmit={submit} className="space-y-4">
            {b?.login_text && <p className="whitespace-pre-line text-sm text-muted">{b.login_text}</p>}
            {ssoError && <ErrorBox error={new Error(ssoError)} />}
            {info?.oidc_enabled && (
              <>
                <a href="/api/v1/auth/oidc/start" className="inline-flex h-9 w-full items-center justify-center gap-2 rounded-md bg-accent px-3 text-sm font-medium text-white hover:opacity-90">
                  <KeyRound className="size-4" />Sign in with {info.oidc_label || 'SSO'}
                </a>
                <div className="flex items-center gap-3 text-xs text-muted"><span className="h-px flex-1 bg-border" />or<span className="h-px flex-1 bg-border" /></div>
              </>
            )}
            <Field label="Username">
              <Input autoFocus autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
            </Field>
            <Field label="Password">
              <Input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
            {(info?.ldap || info?.radius) && <p className="text-xs text-muted">Directory accounts ({[info.ldap && 'LDAP', info.radius && 'RADIUS'].filter(Boolean).join(', ')}) can sign in here too.</p>}
            <ErrorBox error={err} />
            <Button type="submit" variant="primary" loading={busy} className="w-full" icon={<LogIn className="size-4" />}>Sign in</Button>
            {webauthnSupported() && window.isSecureContext && (
              <Button type="button" variant="outline" className="w-full" onClick={passkeyLogin} disabled={busy} icon={<Fingerprint className="size-4" />}>Sign in with a passkey</Button>
            )}
          </form>
        ) : (
          <div className="space-y-4">
            <div>
              <h2 className="font-semibold">Two-factor authentication</h2>
              <p className="text-sm text-muted">Signed in as {me?.user.username}. Confirm it's you.</p>
            </div>
            {methods.includes('passkey') && (
              <Button variant="primary" className="w-full" onClick={passkeyMfa} loading={busy} icon={<KeyRound className="size-4" />}>Use passkey / security key</Button>
            )}
            {methods.includes('totp') && (
              <form onSubmit={submitCode} className="space-y-3">
                <Field label="Code from your authenticator app (or a recovery code)">
                  <Input autoFocus={!methods.includes('passkey')} inputMode="text" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="123456" />
                </Field>
                <Button type="submit" variant={methods.includes('passkey') ? 'secondary' : 'primary'} loading={busy} className="w-full">Verify</Button>
              </form>
            )}
            <ErrorBox error={err} />
            <Button variant="ghost" className="w-full" onClick={async () => { await post('/auth/logout'); location.href = '/login' }}>Use a different account</Button>
          </div>
        )}
      </Card>
    </AuthShell>
  )
}

/** Shown when security policy requires a second factor and the user has none. */
export function MfaSetupRequired() {
  const { refresh, logout } = useAuth()
  return (
    <AuthShell wide>
      <Card title="Set up two-factor authentication">
        <div className="space-y-5">
          <Note tone="warn">Your organisation requires a second factor. Add an authenticator app or a passkey to continue.</Note>
          <div>
            <h3 className="mb-2 text-sm font-semibold">Authenticator app</h3>
            <TotpSetup onDone={() => refresh()} />
          </div>
          <div className="border-t border-line pt-4">
            <h3 className="mb-2 text-sm font-semibold">Passkey or security key</h3>
            <PasskeyRegister onDone={() => refresh()} />
          </div>
          <Button variant="ghost" onClick={logout}>Log out</Button>
        </div>
      </Card>
    </AuthShell>
  )
}

/** Shown while the account is flagged to change its password: the server allows nothing else. */
export function PasswordChangeRequired() {
  const { me, refresh, logout } = useAuth()
  const [pw, setPw] = useState({ current_password: '', new_password: '', confirm: '' })
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const mismatch = !!pw.confirm && pw.confirm !== pw.new_password
  return (
    <AuthShell>
      <Card title="Change your password">
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault()
            setErr(null)
            setBusy(true)
            try {
              await post('/auth/password', { current_password: pw.current_password, new_password: pw.new_password })
              await refresh()
            } catch (x) {
              setErr(x)
            } finally {
              setBusy(false)
            }
          }}
        >
          <Note tone="warn">
            {me?.user.auth_source === 'local'
              ? 'You must choose a new password before you can continue.'
              : 'Your password is managed elsewhere. Ask an administrator to clear this requirement.'}
          </Note>
          {me?.user.auth_source === 'local' && (
            <>
              <Field label="Current password"><Input type="password" autoComplete="current-password" value={pw.current_password} onChange={(e) => setPw({ ...pw, current_password: e.target.value })} /></Field>
              <Field label="New password"><Input type="password" autoComplete="new-password" value={pw.new_password} onChange={(e) => setPw({ ...pw, new_password: e.target.value })} /></Field>
              <Field label="Confirm" error={mismatch ? 'Does not match' : undefined}><Input type="password" autoComplete="new-password" value={pw.confirm} onChange={(e) => setPw({ ...pw, confirm: e.target.value })} /></Field>
              <ErrorBox error={err} />
              <Button type="submit" variant="primary" disabled={busy || !pw.new_password || mismatch}>Change password</Button>
            </>
          )}
          <Button type="button" variant="ghost" onClick={logout}>Log out</Button>
        </form>
      </Card>
    </AuthShell>
  )
}
