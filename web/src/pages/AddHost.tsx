import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, CheckCircle2, KeyRound, Network, Router, Server, Target, Terminal } from 'lucide-react'
import { del, errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, until } from '@/lib/format'
import type { Host, HostGroup, PushFingerprint, Tenant } from '@/lib/types'
import { SnmpForm, defaultSnmp, type SnmpConfig } from '@/components/SnmpForm'
import { useTenantFilter } from '@/components/Layout'
import { Badge, Button, Card, CodeBlock, ErrorBox, Field, Input, Note, PageHeader, Segmented, Select, Spinner, Table, TagInput, Td, Textarea, Th, Toggle, cx, useToast } from '@/components/ui'

type Method = 'agent' | 'push' | 'snmp' | 'target'

interface Placement { tenant_id: number; group_id: number; tags: string[]; name: string }

function PlacementFields({ p, onChange, showName = true }: { p: Placement; onChange: (p: Placement) => void; showName?: boolean }) {
  const { me } = useAuth()
  const provider = me?.user.tenant_id === 0
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants'), enabled: provider })
  const groups = useQuery({ queryKey: ['groups', p.tenant_id], queryFn: () => get<HostGroup[]>('/groups', { tenant_id: provider ? p.tenant_id : undefined }) })
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {showName && <Field label="Display name" hint="Optional; defaults to the hostname"><Input value={p.name} onChange={(e) => onChange({ ...p, name: e.target.value })} /></Field>}
      {provider && (tenants.data?.length ?? 0) > 1 && (
        <Field label="Tenant">
          <Select value={p.tenant_id} onChange={(e) => onChange({ ...p, tenant_id: Number(e.target.value), group_id: 0 })}>
            {tenants.data!.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </Select>
        </Field>
      )}
      <Field label="Group">
        <Select value={p.group_id} onChange={(e) => onChange({ ...p, group_id: Number(e.target.value) })}>
          <option value={0}>No group</option>
          {groups.data?.filter((g) => g.tenant_id === p.tenant_id || !provider).map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
        </Select>
      </Field>
      <Field label="Tags"><TagInput value={p.tags} onChange={(tags) => onChange({ ...p, tags })} placeholder="env:prod" /></Field>
    </div>
  )
}

export function AddHost() {
  const [method, setMethod] = useState<Method>('agent')
  const { tenant } = useTenantFilter()
  const [p, setP] = useState<Placement>({ tenant_id: tenant ?? 0, group_id: 0, tags: [], name: '' })
  const options: { id: Method; icon: React.ReactNode; title: string; desc: string }[] = [
    { id: 'agent', icon: <Server />, title: 'Install agent', desc: 'One command on Linux or Windows. Full metrics, inventory, CVEs and remote actions.' },
    { id: 'push', icon: <Terminal />, title: 'Push install', desc: 'Install the agent for you over SSH (Linux) or WinRM (Windows Server).' },
    { id: 'snmp', icon: <Router />, title: 'SNMP device', desc: 'Switches, routers, firewalls, NAS, printers. v2c or v3, polled by the server.' },
    { id: 'target', icon: <Target />, title: 'Agentless target', desc: 'Anything reachable: ping, TCP, HTTP, certificate and DNS checks only.' },
  ]
  return (
    <div>
      <PageHeader back={<Link to="/hosts" className="mb-1 inline-flex items-center gap-1 text-xs text-muted hover:text-fg"><ArrowLeft className="size-3" />Hosts</Link>} title="Add host" />
      <div className="mb-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {options.map((o) => (
          <button key={o.id} onClick={() => setMethod(o.id)}
            className={cx('flex items-start gap-3 rounded-xl border p-4 text-left [&_svg]:size-5', method === o.id ? 'border-accent bg-accent/8' : 'border-line bg-panel hover:border-muted')}>
            <span className={method === o.id ? 'text-accent' : 'text-muted'}>{o.icon}</span>
            <span><span className="block font-medium">{o.title}</span><span className="mt-0.5 block text-xs text-muted">{o.desc}</span></span>
          </button>
        ))}
      </div>
      {method === 'agent' && <AgentInstall p={p} setP={setP} />}
      {method === 'push' && <PushInstall p={p} setP={setP} />}
      {method === 'snmp' && <AddSnmp p={p} setP={setP} />}
      {method === 'target' && <AddTarget p={p} setP={setP} />}
    </div>
  )
}

interface TokenResp { id: number; token: string; expires_at: number; server_url: string; ca_fingerprint?: string; commands: Record<string, string> }

function AgentInstall({ p, setP }: { p: Placement; setP: (p: Placement) => void }) {
  const [ttl, setTtl] = useState(15)
  const [tok, setTok] = useState<TokenResp | null>(null)
  const [os, setOs] = useState<'linux' | 'windows' | 'manual'>('linux')
  const [err, setErr] = useState<unknown>(null)
  const [since, setSince] = useState(0)
  const qc = useQueryClient()
  const tokens = useQuery({ queryKey: ['tokens'], queryFn: () => get<any[]>('/enrollment-tokens') })
  const newHosts = useQuery({
    queryKey: ['hosts', 'new-since', since],
    queryFn: () => get<Host[]>('/hosts', { kind: 'agent' }).then((hs) => hs.filter((h) => h.created_at >= since)),
    enabled: !!tok, refetchInterval: 3000,
  })
  const generate = async () => {
    setErr(null)
    try {
      const r = await post<TokenResp>('/enrollment-tokens', { tenant_id: p.tenant_id, group_id: p.group_id || null, tags: p.tags, host_name: p.name, ttl_minutes: ttl })
      setTok(r)
      // Server clock: the token was created ttl minutes before it expires.
      setSince(r.expires_at - ttl * 60 - 5)
      qc.invalidateQueries({ queryKey: ['tokens'] })
    } catch (e) {
      setErr(e)
    }
  }
  return (
    <div className="grid gap-4 xl:grid-cols-3">
      <Card title="1. Placement" className="xl:col-span-1">
        <div className="space-y-3">
          <PlacementFields p={p} onChange={setP} />
          <Field label="Token valid for" hint="Use a longer lifetime for unattended rollouts (one token per host)">
            <Select value={ttl} onChange={(e) => setTtl(Number(e.target.value))}>
              <option value={15}>15 minutes</option><option value={60}>1 hour</option><option value={1440}>1 day</option><option value={10080}>7 days</option>
            </Select>
          </Field>
          <ErrorBox error={err} />
          <Button variant="primary" icon={<KeyRound className="size-4" />} onClick={generate}>Generate install command</Button>
        </div>
      </Card>
      <Card title="2. Run on the host" className="xl:col-span-2">
        {!tok ? <p className="text-sm text-muted">Generate a one-time token to get the install command. The agent creates its own key pair; the token is exchanged once for a client certificate signed by this server's CA.</p> : (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span>Token</span><code className="mono rounded bg-panel-2 px-2 py-0.5 font-semibold">{tok.token}</code>
              <span className="text-muted">expires {until(tok.expires_at)}</span>
            </div>
            <Segmented value={os} onChange={setOs} options={[{ id: 'linux', label: 'Linux' }, { id: 'windows', label: 'Windows (PowerShell, admin)' }, { id: 'manual', label: 'Manual' }]} />
            <CodeBlock code={tok.commands[os]} />
            {os === 'linux' && <p className="text-xs text-muted">Supports Debian/Ubuntu, RHEL/Rocky/Alma/Fedora, SUSE, Arch and Alpine (systemd). Creates a low-privilege <code className="mono">sentrel-agent</code> user with sudo rights limited to its own validated helper. Container monitoring needs <code className="mono">--docker</code> appended to the install command (it adds the agent to the docker group, which is root-equivalent).</p>}
            {os === 'windows' && <p className="text-xs text-muted">Installs to Program Files and registers the <b>SentrelAgent</b> service (LocalSystem, auto-restart).</p>}
            {os === 'manual' && <p className="text-xs text-muted">Download the binary from <code className="mono">{tok.server_url}/download/agent/&lt;os&gt;/&lt;arch&gt;</code>, register with the command above, then run <code className="mono">sentrel-agent run</code> (or <code className="mono">service install</code> on Windows).</p>}
            {tok.ca_fingerprint && <Note>Self-signed mode: the command pins this server's CA <code className="mono break-all">{tok.ca_fingerprint}</code>, so agents only trust this server.</Note>}
            <div className="rounded-xl border border-line/70 p-4">
              {newHosts.data?.length ? (
                <div className="space-y-1">
                  {newHosts.data.map((h) => (
                    <div key={h.id} className="flex items-center gap-2 text-sm"><CheckCircle2 className="size-4 text-ok" />Registered <Link to={`/hosts/${h.id}`} className="font-medium text-accent hover:underline">{h.name}</Link></div>
                  ))}
                </div>
              ) : <div className="flex items-center gap-2 text-sm text-muted"><Spinner className="size-4" />Waiting for the agent to register…</div>}
            </div>
          </div>
        )}
      </Card>
      {!!tokens.data?.length && (
        <Card title="Unused tokens" bodyClass="p-0" className="xl:col-span-3">
          <Table>
            <thead><tr><Th>Created</Th><Th>Expires</Th><Th>Name</Th><Th>Tags</Th><Th /></tr></thead>
            <tbody>
              {tokens.data.map((t) => (
                <tr key={t.id}>
                  <Td className="text-xs">{ago(t.created_at)}</Td><Td className="text-xs">{until(t.expires_at)}</Td>
                  <Td className="text-xs">{t.host_name || '–'}</Td><Td>{(t.tags || []).map((x: string) => <Badge key={x}>{x}</Badge>)}</Td>
                  <Td className="text-right"><Button size="sm" variant="ghost" onClick={async () => { await del(`/enrollment-tokens/${t.id}`); qc.invalidateQueries({ queryKey: ['tokens'] }) }}>Revoke</Button></Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </div>
  )
}

function PushInstall({ p, setP }: { p: Placement; setP: (p: Placement) => void }) {
  const [mode, setMode] = useState<'ssh' | 'winrm'>('ssh')
  const [f, setF] = useState({ host: '', port: '', username: 'root', password: '', private_key: '', key_passphrase: '', sudo_password: '', host_key_fingerprint: '', https: true, insecure: false, ntlm: true })
  const [fp, setFp] = useState<{ fingerprint: string; key_type: string } | null>(null)
  const [fpOK, setFpOK] = useState(false)
  const [fpBusy, setFpBusy] = useState(false)
  const [ack, setAck] = useState(false)
  const insecureWinrm = mode === 'winrm' && (!f.https || f.insecure)
  // A new target invalidates any fingerprint the user already confirmed.
  useEffect(() => { setFp(null); setFpOK(false) }, [f.host, f.port, mode])
  const fetchFp = async () => {
    setFpBusy(true)
    setErr(null)
    try {
      const r = await post<PushFingerprint>('/push/ssh/fingerprint', { host: f.host.trim(), port: Number(f.port) || 0 })
      setFp(r)
      setFpOK(false)
    } catch (e) {
      setErr(e)
    } finally {
      setFpBusy(false)
    }
  }
  const [auth, setAuth] = useState<'password' | 'key'>('password')
  const [pushId, setPushId] = useState<number | null>(null)
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const push = useQuery({ queryKey: ['push', String(pushId)], queryFn: () => get<any>(`/push/${pushId}`), enabled: !!pushId, refetchInterval: (q) => (q.state.data?.status === 'running' ? 1500 : false) })
  const set = (k: keyof typeof f, v: any) => setF({ ...f, [k]: v })
  useEffect(() => setF((x) => ({ ...x, username: mode === 'ssh' ? 'root' : 'Administrator', port: '' })), [mode])
  const start = async () => {
    setBusy(true)
    setErr(null)
    try {
      const common = { tenant_id: p.tenant_id, group_id: p.group_id || null, tags: p.tags, name: p.name }
      const body = mode === 'ssh'
        ? { ...common, host: f.host, port: Number(f.port) || 0, username: f.username, password: auth === 'password' ? f.password : '', private_key: auth === 'key' ? f.private_key : '', key_passphrase: f.key_passphrase, sudo_password: f.sudo_password, host_key: fp?.fingerprint }
        : { ...common, host: f.host, port: Number(f.port) || 0, https: f.https, insecure: f.insecure, allow_insecure: insecureWinrm && ack, username: f.username, password: f.password, ntlm: f.ntlm }
      const r = await post<any>(`/push/${mode}`, body)
      setPushId(r.id)
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card title="Target" actions={<Segmented value={mode} onChange={setMode} options={[{ id: 'ssh', label: 'SSH (Linux)' }, { id: 'winrm', label: 'WinRM (Windows)' }]} />}>
        <div className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Host / IP" className="sm:col-span-2"><Input value={f.host} onChange={(e) => set('host', e.target.value)} /></Field>
            <Field label="Port"><Input placeholder={mode === 'ssh' ? '22' : f.https ? '5986' : '5985'} value={f.port} onChange={(e) => set('port', e.target.value)} /></Field>
            <Field label="Username" className="sm:col-span-3"><Input value={f.username} onChange={(e) => set('username', e.target.value)} /></Field>
          </div>
          {mode === 'ssh' ? (
            <>
              <Segmented value={auth} onChange={setAuth} options={[{ id: 'password', label: 'Password' }, { id: 'key', label: 'Private key' }]} />
              {auth === 'password' ? <Field label="Password"><Input type="password" autoComplete="off" value={f.password} onChange={(e) => set('password', e.target.value)} /></Field> : (
                <>
                  <Field label="Private key (PEM / OpenSSH)"><Textarea className="mono text-xs" value={f.private_key} onChange={(e) => set('private_key', e.target.value)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" /></Field>
                  <Field label="Key passphrase (optional)"><Input type="password" value={f.key_passphrase} onChange={(e) => set('key_passphrase', e.target.value)} /></Field>
                </>
              )}
              <Field label="sudo password" hint="If the user is not root; defaults to the login password"><Input type="password" autoComplete="off" value={f.sudo_password} onChange={(e) => set('sudo_password', e.target.value)} /></Field>
              <Field label="Host key" hint="Fetch the fingerprint, compare it with the one on the machine (ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub), then confirm. The install only connects to a host presenting this key.">
                <div className="space-y-2">
                  <Button loading={fpBusy} disabled={!f.host.trim()} onClick={fetchFp}>Fetch fingerprint</Button>
                  {fp && (
                    <div className="rounded-xl bg-panel-2 p-3 text-xs">
                      <div className="text-muted">{fp.key_type}</div>
                      <div className="mono break-all">{fp.fingerprint}</div>
                      <div className="mt-2"><Toggle checked={fpOK} onChange={setFpOK} label="This fingerprint is correct" /></div>
                    </div>
                  )}
                </div>
              </Field>
            </>
          ) : (
            <>
              <Field label="Password"><Input type="password" autoComplete="off" value={f.password} onChange={(e) => set('password', e.target.value)} /></Field>
              <Toggle checked={f.https} onChange={(v) => set('https', v)} label="Use HTTPS (port 5986)" />
              <Toggle checked={f.insecure} onChange={(v) => set('insecure', v)} label="Accept self-signed WinRM certificate" />
              <Toggle checked={f.ntlm} onChange={(v) => set('ntlm', v)} label="NTLM authentication" hint="Required for domain and most local accounts" />
              {insecureWinrm && (
                <>
                  <Note tone="warn">{f.https ? 'Certificate checks are off, so the connection can be intercepted.' : 'Plain HTTP WinRM sends the administrator password over the network without encryption.'} Prefer HTTPS with a trusted certificate.</Note>
                  <Toggle checked={ack} onChange={setAck} label="I accept this risk" />
                </>
              )}
              <Note>WinRM is enabled by default on Windows Server (2012 R2+) but not on Windows 10/11. For client editions use the PowerShell install command instead.</Note>
            </>
          )}
          <PlacementFields p={p} onChange={setP} />
          <Note>Credentials are used once for this install and are never stored.</Note>
          <ErrorBox error={err} />
          <Button variant="primary" loading={busy} disabled={!f.host || !f.username || (mode === 'ssh' && !(fp && fpOK)) || (insecureWinrm && !ack)} onClick={start} icon={<Network className="size-4" />}>Install agent</Button>
        </div>
      </Card>
      <Card title="Progress" subtitle={push.data ? `${push.data.method} → ${push.data.target}` : undefined}
        actions={push.data && <Badge tone={push.data.status === 'succeeded' ? 'ok' : push.data.status === 'failed' ? 'crit' : 'warn'}>{push.data.status}</Badge>}>
        {!pushId ? <p className="text-sm text-muted">The live install log appears here.</p> : (
          <>
            <pre className="mono max-h-96 overflow-auto whitespace-pre-wrap rounded-xl bg-panel-2 p-3 text-xs">{push.data?.log || 'starting…'}</pre>
            {push.data?.host_id && <Link to={`/hosts/${push.data.host_id}`} className="mt-3 inline-block text-sm text-accent hover:underline">Open the new host →</Link>}
          </>
        )}
      </Card>
    </div>
  )
}

function AddSnmp({ p, setP }: { p: Placement; setP: (p: Placement) => void }) {
  const [address, setAddress] = useState('')
  const [cfg, setCfg] = useState<SnmpConfig>(defaultSnmp)
  const [test, setTest] = useState<any>(null)
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const nav = useNavigate()
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card title="SNMP device">
        <div className="space-y-3">
          <Field label="Address (IP or hostname)" required><Input value={address} onChange={(e) => setAddress(e.target.value)} /></Field>
          <SnmpForm value={cfg} onChange={setCfg} />
          <PlacementFields p={p} onChange={setP} />
          <ErrorBox error={err} />
          <div className="flex gap-2">
            <Button loading={busy} disabled={!address} onClick={async () => {
              setBusy(true); setErr(null)
              try { setTest(await post('/snmp/test', { address, snmp: cfg })) } catch (e) { setErr(e) } finally { setBusy(false) }
            }}>Test</Button>
            <Button variant="primary" loading={busy} disabled={!address} onClick={async () => {
              setBusy(true); setErr(null)
              try {
                const h = await post<Host>('/hosts/snmp', { name: p.name, address, tenant_id: p.tenant_id, group_id: p.group_id || null, tags: p.tags, snmp: cfg })
                toast('Device added'); nav(`/hosts/${h.id}`)
              } catch (e) { setErr(errorMessage(e)) } finally { setBusy(false) }
            }}>Add device</Button>
          </div>
        </div>
      </Card>
      <Card title="Test result">
        {!test ? <p className="text-sm text-muted">Test reads the system group, interfaces, profile metrics and LLDP once.</p> : !test.ok ? <ErrorBox error={test.error} /> : (
          <div className="space-y-3 text-sm">
            <Note tone="ok">Reached <b>{test.system?.name || address}</b> in {test.duration_ms?.toFixed(0)} ms using profile <b>{test.profile}</b>.</Note>
            <p className="text-muted">{test.system?.descr}</p>
            <p>{test.interfaces} interfaces · {test.neighbors} LLDP neighbours</p>
            <pre className="mono max-h-64 overflow-auto rounded-xl bg-panel-2 p-3 text-xs">{Object.entries(test.metrics || {}).map(([k, v]) => `${k} = ${(v as number).toFixed(2)}`).join('\n') || 'no profile metrics'}</pre>
            {test.errors?.length > 0 && <Note tone="warn">{test.errors.join('; ')}</Note>}
          </div>
        )}
      </Card>
    </div>
  )
}

function AddTarget({ p, setP }: { p: Placement; setP: (p: Placement) => void }) {
  const [address, setAddress] = useState('')
  const [ping, setPing] = useState(true)
  const [err, setErr] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const nav = useNavigate()
  return (
    <Card title="Agentless target" className="max-w-2xl">
      <div className="space-y-3">
        <Field label="Address" hint="Hostname or IP; add TCP, HTTP, certificate and DNS checks afterwards"><Input value={address} onChange={(e) => setAddress(e.target.value)} /></Field>
        <PlacementFields p={p} onChange={setP} />
        <Toggle checked={ping} onChange={setPing} label="Add a ping check" />
        <ErrorBox error={err} />
        <Button variant="primary" loading={busy} disabled={!address && !p.name} onClick={async () => {
          setBusy(true); setErr(null)
          try {
            const h = await post<Host>('/hosts/target', { name: p.name, address, tenant_id: p.tenant_id, group_id: p.group_id || null, tags: p.tags, add_ping_check: ping })
            nav(`/hosts/${h.id}?tab=checks`)
          } catch (e) { setErr(e) } finally { setBusy(false) }
        }}>Add target</Button>
      </div>
    </Card>
  )
}
