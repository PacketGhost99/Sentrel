import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Save, Trash2 } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, dateTime } from '@/lib/format'
import { PasskeyRegister, TotpSetup } from '@/components/TwoFactor'
import { Badge, Button, Card, ErrorBox, Field, Input, KV, Note, PageHeader, Table, Td, Th, useConfirm, useToast } from '@/components/ui'
import { roleInfo } from './Users'

export function Profile() {
  const { me, refresh } = useAuth()
  const toast = useToast()
  const confirm = useConfirm()
  const qc = useQueryClient()
  const u = me!.user
  const [prof, setProf] = useState({ display_name: u.display_name, email: u.email, theme: u.theme })
  const [pw, setPw] = useState({ current_password: '', new_password: '', confirm: '' })
  const [pwErr, setPwErr] = useState<unknown>(null)
  const [totp, setTotp] = useState(false)
  const [disable, setDisable] = useState('')
  const passkeys = useQuery({ queryKey: ['passkeys'], queryFn: () => get<any[]>('/auth/passkeys') })
  const sessions = useQuery({ queryKey: ['sessions'], queryFn: () => get<any[]>('/auth/sessions') })
  return (
    <div>
      <PageHeader title="Profile & security" />
      {u.must_change_password && <Note tone="warn" className="mb-4">An administrator asked you to change your password.</Note>}
      <div className="grid gap-4 xl:grid-cols-2">
        <Card title="Account">
          <KV className="mb-4" items={[['Username', u.username], ['Role', roleInfo[u.role]?.label || u.role], ['Tenant', me!.tenant?.name], ['Sign-in', u.auth_source], ['Member since', dateTime(u.created_at)]]} />
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Display name"><Input value={prof.display_name} onChange={(e) => setProf({ ...prof, display_name: e.target.value })} /></Field>
            <Field label="Email"><Input type="email" value={prof.email} onChange={(e) => setProf({ ...prof, email: e.target.value })} /></Field>
          </div>
          <Button className="mt-3" variant="primary" icon={<Save className="size-4" />} onClick={async () => { try { await put('/auth/me', prof); toast('Profile saved'); refresh() } catch (e) { toast(errorMessage(e), 'crit') } }}>Save</Button>
        </Card>
        {u.auth_source === 'local' && (
          <Card title="Password">
            <div className="space-y-3">
              <Field label="Current password"><Input type="password" autoComplete="current-password" value={pw.current_password} onChange={(e) => setPw({ ...pw, current_password: e.target.value })} /></Field>
              <Field label="New password"><Input type="password" autoComplete="new-password" value={pw.new_password} onChange={(e) => setPw({ ...pw, new_password: e.target.value })} /></Field>
              <Field label="Confirm" error={pw.confirm && pw.confirm !== pw.new_password ? 'Does not match' : undefined}><Input type="password" autoComplete="new-password" value={pw.confirm} onChange={(e) => setPw({ ...pw, confirm: e.target.value })} /></Field>
              <ErrorBox error={pwErr} />
              <Button variant="primary" disabled={!pw.new_password || pw.new_password !== pw.confirm} onClick={async () => {
                setPwErr(null)
                try { await post('/auth/password', pw); toast('Password changed; other sessions were signed out'); setPw({ current_password: '', new_password: '', confirm: '' }); refresh(); qc.invalidateQueries({ queryKey: ['sessions'] }) } catch (e) { setPwErr(e) }
              }}>Change password</Button>
            </div>
          </Card>
        )}
        <Card title="Authenticator app (TOTP)">
          {u.totp_enabled ? (
            <div className="space-y-3">
              <p className="text-sm"><Badge tone="ok">enabled</Badge> Codes from your authenticator app are required at sign-in.</p>
              <div className="flex flex-wrap items-end gap-2">
                <Field label={u.auth_source === 'local' ? 'Password or current code to disable' : 'Current code to disable'}><Input type="password" value={disable} onChange={(e) => setDisable(e.target.value)} /></Field>
                <Button onClick={async () => {
                  try { await post('/auth/totp/disable', /^\d{6}$/.test(disable) ? { code: disable } : { password: disable }); toast('Authenticator removed'); setDisable(''); refresh() } catch (e) { toast(errorMessage(e), 'crit') }
                }}>Disable</Button>
              </div>
            </div>
          ) : totp ? <TotpSetup onDone={() => { setTotp(false); refresh() }} /> : (
            <div className="space-y-3"><p className="text-sm text-muted">Add a second factor with any TOTP app.</p><Button variant="primary" onClick={() => setTotp(true)}>Set up</Button></div>
          )}
        </Card>
        <Card title="Passkeys & security keys" subtitle="Phishing-resistant second factor; also allows passwordless sign-in.">
          {!!passkeys.data?.length && (
            <Table className="mb-3">
              <thead><tr><Th>Name</Th><Th>Added</Th><Th>Last used</Th><Th /></tr></thead>
              <tbody>{passkeys.data.map((k) => (
                <tr key={k.id}><Td>{k.name}</Td><Td className="text-xs">{dateTime(k.created_at)}</Td><Td className="text-xs">{ago(k.last_used_at)}</Td>
                  <Td className="text-right"><Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                    if (!(await confirm({ title: 'Remove passkey', danger: true, confirmLabel: 'Remove', body: <p>Remove <b>{k.name}</b>?</p> }))) return
                    await del(`/auth/passkeys/${k.id}`); qc.invalidateQueries({ queryKey: ['passkeys'] }); refresh()
                  }} /></Td></tr>
              ))}</tbody>
            </Table>
          )}
          <PasskeyRegister onDone={() => { toast('Passkey added'); qc.invalidateQueries({ queryKey: ['passkeys'] }); refresh() }} />
        </Card>
        <Card title="Active sessions" className="xl:col-span-2" bodyClass="p-0"
          actions={<Button size="sm" onClick={async () => { await post('/auth/sessions/revoke-others'); toast('Other sessions signed out'); qc.invalidateQueries({ queryKey: ['sessions'] }) }}>Sign out other sessions</Button>}>
          <Table>
            <thead><tr><Th>Device</Th><Th>IP</Th><Th>Signed in</Th><Th>Last active</Th></tr></thead>
            <tbody>{(sessions.data || []).map((s, i) => (
              <tr key={i}><Td className="max-w-md truncate text-xs">{s.user_agent} {s.current && <Badge tone="ok">this session</Badge>}</Td><Td className="mono text-xs">{s.ip}</Td><Td className="text-xs">{dateTime(s.created_at)}</Td><Td className="text-xs">{ago(s.last_seen_at)}</Td></tr>
            ))}</tbody>
          </Table>
        </Card>
      </div>
    </div>
  )
}
