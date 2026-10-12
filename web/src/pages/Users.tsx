import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, ShieldOff, Trash2, Users as UsersIcon } from 'lucide-react'
import { del, errorMessage, get, post, put } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago } from '@/lib/format'
import type { Tenant, User } from '@/lib/types'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, Note, PageHeader, Select, Table, Td, Th, Toggle, useConfirm, useToast } from '@/components/ui'

export const roleInfo: Record<string, { label: string; desc: string }> = {
  admin: { label: 'Administrator', desc: 'Everything, including settings, tenants, users and scripts' },
  operator: { label: 'Operator', desc: 'Manage hosts, checks, alerts and run upgrades/blocks' },
  viewer: { label: 'Viewer', desc: 'Read-only access to all tenants' },
  tenant_admin: { label: 'Tenant admin', desc: 'Manage their tenant: users, checks, alerts, upgrades, branding' },
  tenant_viewer: { label: 'Tenant viewer', desc: 'Read-only access to their tenant' },
}

export function UsersPage() {
  const { me } = useAuth()
  const { tenant } = useTenantFilter()
  const qc = useQueryClient()
  const toast = useToast()
  const confirm = useConfirm()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const users = useQuery({ queryKey: ['users', tenant], queryFn: () => get<User[]>('/users', { tenant_id: tenant ?? undefined }) })
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants') })
  const tname = (id: number) => tenants.data?.find((t) => t.id === id)?.name || `#${id}`
  return (
    <div>
      <PageHeader title="Users" subtitle="Local accounts and directory users (LDAP/RADIUS users are created automatically on first login)."
        actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ tenant_id: me?.user.tenant_id || tenant || 0, role: me?.user.tenant_id ? 'tenant_viewer' : 'viewer', auth_source: 'local' })}>Add user</Button>} />
      <Card bodyClass="p-0">
        {users.isLoading ? <Loading /> : !users.data?.length ? <Empty title="No users" icon={<UsersIcon className="size-8" />} /> : (
          <Table>
            <thead><tr><Th>User</Th><Th>Role</Th><Th className="hidden md:table-cell">Tenant</Th><Th className="hidden sm:table-cell">Sign-in</Th><Th className="hidden lg:table-cell">2FA</Th><Th className="hidden lg:table-cell">Last login</Th><Th /></tr></thead>
            <tbody>{users.data.map((u) => (
              <tr key={u.id} className={u.disabled ? 'opacity-50' : ''}>
                <Td><div className="font-medium">{u.username} {u.id === me?.user.id && <Badge>you</Badge>} {u.disabled && <Badge tone="crit">disabled</Badge>}</div><div className="text-xs text-muted">{u.display_name !== u.username ? u.display_name : ''} {u.email}</div></Td>
                <Td><Badge tone={u.role.includes('admin') ? 'accent' : 'muted'}>{roleInfo[u.role]?.label || u.role}</Badge></Td>
                <Td className="hidden text-xs md:table-cell">{tname(u.tenant_id)}</Td>
                <Td className="hidden text-xs sm:table-cell">{u.auth_source}</Td>
                <Td className="hidden lg:table-cell">{u.totp_enabled && <Badge tone="ok">TOTP</Badge>} {u.passkey_count > 0 && <Badge tone="ok">{u.passkey_count} passkey</Badge>}{!u.totp_enabled && !u.passkey_count && <span className="text-xs text-muted">none</span>}</Td>
                <Td className="hidden text-xs text-muted lg:table-cell">{ago(u.last_login_at)}</Td>
                <Td className="whitespace-nowrap text-right">
                  <Button size="sm" variant="ghost" icon={<Pencil className="size-3.5" />} onClick={() => setEdit(u)} />
                  {(u.totp_enabled || u.passkey_count > 0) && <Button size="sm" variant="ghost" title="Reset 2FA" icon={<ShieldOff className="size-3.5" />} onClick={async () => {
                    if (!(await confirm({ title: 'Reset two-factor', danger: true, confirmLabel: 'Reset', body: <p>Remove all second factors for <b>{u.username}</b> and sign them out?</p> }))) return
                    try { await post(`/users/${u.id}/reset-2fa`); toast('2FA reset'); qc.invalidateQueries({ queryKey: ['users'] }) } catch (e) { toast(errorMessage(e), 'crit') }
                  }} />}
                  {u.id !== me?.user.id && <Button size="sm" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                    if (!(await confirm({ title: 'Delete user', danger: true, confirmLabel: 'Delete', body: <p>Delete <b>{u.username}</b>? Their API keys stop working.</p> }))) return
                    try { await del(`/users/${u.id}`); toast('User deleted'); qc.invalidateQueries({ queryKey: ['users'] }) } catch (e) { toast(errorMessage(e), 'crit') }
                  }} />}
                </Td>
              </tr>
            ))}</tbody>
          </Table>
        )}
      </Card>
      {edit && <UserEditor initial={edit} tenants={tenants.data || []} onClose={() => { setEdit(null); qc.invalidateQueries({ queryKey: ['users'] }) }} />}
    </div>
  )
}

function UserEditor({ initial, tenants, onClose }: { initial: Partial<User>; tenants: Tenant[]; onClose: () => void }) {
  const { me } = useAuth()
  const toast = useToast()
  const provider = me?.user.tenant_id === 0
  const [u, setU] = useState<Partial<User> & { password?: string }>({ username: '', email: '', display_name: '', password: '', ...initial })
  const [err, setErr] = useState<unknown>(null)
  const t = u.tenant_id ?? 0
  const roles = t === 0 ? ['admin', 'operator', 'viewer'] : ['tenant_admin', 'tenant_viewer']
  return (
    <Modal open onClose={onClose} title={u.id ? `Edit ${u.username}` : 'Add user'}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={async () => {
        setErr(null)
        try {
          const body = { ...u, must_change_password: u.must_change_password }
          if (u.id) await put(`/users/${u.id}`, body); else await post('/users', body)
          toast('User saved'); onClose()
        } catch (e) { setErr(e) }
      }}>Save</Button></>}>
      <div className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Username" required><Input value={u.username} disabled={!!u.id} onChange={(e) => setU({ ...u, username: e.target.value })} /></Field>
          <Field label="Display name"><Input value={u.display_name} onChange={(e) => setU({ ...u, display_name: e.target.value })} /></Field>
          <Field label="Email"><Input type="email" value={u.email} onChange={(e) => setU({ ...u, email: e.target.value })} /></Field>
          {!u.id && <Field label="Sign-in method"><Select value={u.auth_source} onChange={(e) => setU({ ...u, auth_source: e.target.value })}><option value="local">Local password</option><option value="ldap">LDAP</option><option value="radius">RADIUS</option></Select></Field>}
          {provider && !u.id && (
            <Field label="Tenant"><Select value={t} onChange={(e) => { const nt = Number(e.target.value); setU({ ...u, tenant_id: nt, role: nt === 0 ? 'viewer' : 'tenant_viewer' }) }}>
              {tenants.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
            </Select></Field>
          )}
          <Field label="Role" hint={roleInfo[u.role || '']?.desc}><Select value={u.role} onChange={(e) => setU({ ...u, role: e.target.value })}>
            {roles.map((r) => <option key={r} value={r}>{roleInfo[r].label}</option>)}
          </Select></Field>
        </div>
        {(u.auth_source || 'local') === 'local' && (
          <Field label={u.id ? 'New password (leave blank to keep)' : 'Password'} hint="At least 10 characters"><Input type="password" autoComplete="new-password" value={u.password} onChange={(e) => setU({ ...u, password: e.target.value })} /></Field>
        )}
        {u.auth_source !== 'local' && <Note>The password is checked against {u.auth_source?.toUpperCase()} at each login.</Note>}
        <Toggle checked={!!u.must_change_password} onChange={(v) => setU({ ...u, must_change_password: v })} label="Ask to change password at next login" />
        {u.id && <Toggle checked={!!u.disabled} onChange={(v) => setU({ ...u, disabled: v })} label="Disabled" hint="Disabled users are signed out and cannot log in" />}
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
