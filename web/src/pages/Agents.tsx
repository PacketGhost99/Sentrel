import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowUpCircle, CheckCircle2, Download, KeyRound, Server, Terminal } from 'lucide-react'
import { del, errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago, bytes, until } from '@/lib/format'
import type { EnrolmentToken, Host, InstallInfo } from '@/lib/types'
import { PlacementFields, type Placement } from '@/pages/AddHost'
import { useTenantFilter } from '@/components/Layout'
import {
  findPackage, fetchCommand, msiCommand, packageCommand, parseOperatorKeys, pinOf, scriptCommand, urlLooksWrong, windowsCommand, TOKEN_PLACEHOLDER,
  type Arch, type Platform, type WritePolicy,
} from '@/lib/installcmd'
import { Badge, Button, Card, CodeBlock, CopyButton, ErrorBox, Field, Input, Loading, Note, PageHeader, Segmented, Select, Spinner, Stat, Table, Td, Th, Toggle, cx, useConfirm, useToast } from '@/components/ui'

const policyLabel: Record<string, string> = { server: 'Standard', operator: 'Operator-signed', none: 'Read-only' }

const platforms: { id: Platform; title: string; sub: string; icon: React.ReactNode }[] = [
  { id: 'deb', title: 'Ubuntu / Debian', sub: '.deb package', icon: <Terminal /> },
  { id: 'rpm', title: 'RHEL / Rocky / Alma / Fedora', sub: '.rpm package', icon: <Terminal /> },
  { id: 'script', title: 'Other Linux', sub: 'Install script', icon: <Terminal /> },
  { id: 'windows', title: 'Windows', sub: '.msi or PowerShell', icon: <Server /> },
]

const policyHelp: Record<WritePolicy, string> = {
  server: 'The server\'s signature is enough for any job. Best for most hosts.',
  operator: 'Jobs that change the host also need a signature from your operator key, so a compromised server cannot change it.',
  none: 'The agent refuses every job that changes the host. Monitoring only.',
}

export function Agents() {
  const { can } = useAuth()
  const info = useQuery({ queryKey: ['install-info'], queryFn: () => get<InstallInfo>('/agents/install-info'), enabled: can('hosts') })
  return (
    <div>
      <PageHeader title="Agents" subtitle="Install the Sentrel agent on Linux and Windows hosts, and keep it up to date." />
      {!can('hosts') && <Note tone="warn">Installing agents needs the hosts permission.</Note>}
      {info.isLoading && <Loading />}
      <ErrorBox error={info.error} />
      {info.data && <AgentsBody info={info.data} />}
    </div>
  )
}

function AgentsBody({ info }: { info: InstallInfo }) {
  const [platform, setPlatform] = useState<Platform>('deb')
  const [arch, setArch] = useState<Arch>('amd64')
  const [policy, setPolicy] = useState<WritePolicy>('server')
  const [opKey, setOpKey] = useState('')
  const [docker, setDocker] = useState(false)
  const [tok, setTok] = useState<EnrolmentToken | null>(null)
  const [ttl, setTtl] = useState(1440)
  const [since, setSince] = useState(0)
  const { tenant } = useTenantFilter()
  const [p, setP] = useState<Placement>({ tenant_id: tenant ?? 0, group_id: 0, tags: [], name: '' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const qc = useQueryClient()

  const keys = parseOperatorKeys(opKey)
  const linux = platform !== 'windows'
  const opts = {
    info, token: tok?.token ?? TOKEN_PLACEHOLDER, policy, operatorKeys: keys.keys, docker: docker && linux,
  }
  const pkg = findPackage(info.packages, platform, arch)

  const create = async () => {
    setBusy(true)
    setErr(null)
    try {
      const r = await post<EnrolmentToken>('/enrollment-tokens', { tenant_id: p.tenant_id, group_id: p.group_id || null, tags: p.tags, host_name: p.name, ttl_minutes: ttl })
      setTok(r)
      // Server clock: the token was created ttl minutes before it expires.
      setSince(r.expires_at - ttl * 60 - 5)
      qc.invalidateQueries({ queryKey: ['tokens'] })
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-5">
      <Summary info={info} />
      <TlsBanner info={info} />
      <UrlWarning info={info} />

      <Card title="1. Enrolment" subtitle="A token is used once: the agent swaps it for a client certificate signed by this server">
        <div className="grid gap-5 lg:grid-cols-2">
          <div className="space-y-3">
            <PlacementFields p={p} onChange={setP} />
            <Field label="Token valid for">
              <Select value={ttl} onChange={(e) => setTtl(Number(e.target.value))}>
                <option value={60}>1 hour</option><option value={1440}>24 hours</option><option value={10080}>7 days</option>
              </Select>
            </Field>
          </div>
          <div className="space-y-3">
            <Field label="Write policy" hint={policyHelp[policy]}>
              <Segmented value={policy} onChange={setPolicy} options={[{ id: 'server', label: 'Standard' }, { id: 'operator', label: 'Operator-signed' }, { id: 'none', label: 'Read-only' }]} />
            </Field>
            {policy === 'operator' && (
              <Field label="Operator public key" hint={<>From <code className="mono">sentrel-sign keygen-operator</code>. Separate several keys with spaces.</>} error={keys.invalid ? 'Keys may only contain letters, digits and + / = _ -' : undefined}>
                <Input className="mono" value={opKey} onChange={(e) => setOpKey(e.target.value)} placeholder="base64 public key" />
              </Field>
            )}
            <Toggle checked={docker && linux} disabled={!linux} onChange={setDocker} label="Monitor Docker containers (Linux)"
              hint="Adds the agent user to the docker group." />
            {docker && linux && <Note tone="warn">Membership of the docker group is root-equivalent on the host. Only enable it on hosts you trust the agent and server on.</Note>}
          </div>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-3 border-t border-line/70 pt-4">
          {tok ? (
            <>
              <KeyRound className="size-4 text-accent" />
              <span className="text-sm">Token</span>
              <code className="mono rounded-lg bg-panel-2 px-2.5 py-1 text-sm font-semibold">{tok.token}</code>
              <CopyButton text={tok.token} />
              <span className="text-xs text-muted">expires {until(tok.expires_at)}. One host per token.</span>
              <Button size="sm" className="ml-auto" loading={busy} onClick={create}>New token</Button>
            </>
          ) : (
            <>
              <Button variant="primary" icon={<KeyRound className="size-4" />} loading={busy} onClick={create}>Create token</Button>
              <span className="text-xs text-muted">The commands below show a placeholder until you create one.</span>
            </>
          )}
        </div>
        <ErrorBox error={err} className="mt-3" />
      </Card>

      <Card title="2. Choose your platform">
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {platforms.map((o) => (
            <button key={o.id} onClick={() => setPlatform(o.id)} aria-pressed={platform === o.id}
              className={cx('flex items-start gap-3 rounded-xl border p-4 text-left [&_svg]:size-5', platform === o.id ? 'border-accent bg-accent/8' : 'border-line bg-panel hover:border-muted')}>
              <span className={platform === o.id ? 'text-accent' : 'text-muted'}>{o.icon}</span>
              <span><span className="block text-sm font-medium">{o.title}</span><span className="mt-0.5 block text-xs text-muted">{o.sub}</span></span>
            </button>
          ))}
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <span className="text-xs font-medium text-muted">Architecture</span>
          <Segmented value={platform === 'windows' ? 'amd64' : arch} onChange={setArch} options={[{ id: 'amd64', label: 'x86-64' }, { id: 'arm64', label: 'ARM64' }]} />
          {platform === 'windows' && <span className="text-xs text-muted">Windows on ARM runs the x64 agent under emulation.</span>}
        </div>
      </Card>

      <Card title={`3. Install on ${platforms.find((x) => x.id === platform)!.title}`}>
        <div className="space-y-6">
          <section className="space-y-2">
            <h4 className="text-sm font-semibold">Quick install</h4>
            <p className="text-xs text-muted">{platform === 'windows' ? 'Run in an elevated Windows PowerShell (Run as administrator).' : 'Run as a user with sudo on the host.'}</p>
            <CodeBlock code={platform === 'windows' ? windowsCommand(opts) : scriptCommand(opts)} />
            {platform === 'windows' && pinOf(info) && <p className="text-xs text-muted">The pinned one-liner uses Windows PowerShell 5.1, the default on Windows. On PowerShell 7 use the MSI below.</p>}
          </section>

          {platform !== 'script' && (
            <section className="space-y-2 border-t border-line/70 pt-5">
              <h4 className="text-sm font-semibold">Or install the package</h4>
              {pkg ? (
                <>
                  <div className="flex flex-wrap items-center gap-3 rounded-xl bg-panel-2 p-3">
                    <a href={pkg.url} download={pkg.file}>
                      <Button variant="primary" icon={<Download className="size-4" />}>Download {pkg.format === 'msi' ? '.msi' : `.${pkg.format}`}</Button>
                    </a>
                    <div className="min-w-0 text-xs">
                      <div className="mono truncate font-medium">{pkg.file}</div>
                      <div className="mt-0.5 flex flex-wrap items-center gap-x-2 text-muted">
                        <span>{bytes(pkg.size)}</span>
                        <span className="flex items-center gap-1">SHA-256 <code className="mono">{pkg.sha256.slice(0, 16)}…</code><CopyButton text={pkg.sha256} /></span>
                      </div>
                    </div>
                  </div>
                  {platform !== 'windows' && (
                    <>
                      <p className="text-xs text-muted">Or fetch it straight to the host:</p>
                      <CodeBlock code={fetchCommand(opts, pkg)} />
                    </>
                  )}
                  <p className="text-xs text-muted">{platform === 'windows' ? 'Then, from an elevated prompt in the download folder:' : 'Then install, register and start:'}</p>
                  <CodeBlock code={platform === 'windows' ? msiCommand(opts, pkg) : packageCommand(opts, pkg)} />
                </>
              ) : (
                <Note>This server has no {platform === 'windows' ? 'MSI' : `${platform === 'deb' ? '.deb' : '.rpm'} ${arch === 'amd64' ? 'x86-64' : 'ARM64'}`} package. Use the quick install above{info.packages.length === 0 ? ' (this build was made without nfpm or wixl)' : ''}.</Note>
              )}
            </section>
          )}

          <details className="group rounded-xl border border-line/70 px-4 py-3 text-sm">
            <summary className="cursor-pointer select-none font-medium">What this does</summary>
            <ul className="mt-2 list-disc space-y-1 pl-5 text-xs text-muted">
              {linux ? (
                <>
                  <li>Creates a low-privilege <code className="mono">sentrel-agent</code> user; sudo may only run the agent's own validated helper (<code className="mono">sentrel-agent priv</code>).</li>
                  <li>Installs <code className="mono">/usr/local/bin/sentrel-agent</code> and a systemd service limited to 192 MB of memory and 25% CPU.</li>
                  <li>Registers with this server using the one-time token, receiving a client certificate for mutual TLS. The token is then spent.</li>
                  <li>{info.tls_mode === 'selfsigned' ? 'Pins this server\'s certificate for the download and the registration.' : 'Uses your server\'s certificate to verify the connection.'}</li>
                </>
              ) : (
                <>
                  <li>Installs <code className="mono">sentrel-agent.exe</code> into Program Files and its keys into ProgramData, readable only by SYSTEM and Administrators.</li>
                  <li>Registers with this server using the one-time token and receives a client certificate for mutual TLS.</li>
                  <li>Runs as the <b>SentrelAgent</b> Windows service (automatic start, restarts on failure).</li>
                  <li>{info.tls_mode === 'selfsigned' ? 'Pins this server\'s certificate for the download and the registration.' : 'Uses your server\'s certificate to verify the connection.'}</li>
                </>
              )}
            </ul>
          </details>
        </div>
      </Card>

      {tok && <Waiting tok={tok} since={since} />}
      <Tokens />
    </div>
  )
}

function TlsBanner({ info }: { info: InstallInfo }) {
  if (info.tls_mode === 'selfsigned') {
    return <Note tone="ok">Pinned to this server's certificate (fingerprint <code className="mono break-all">{info.ca_fingerprint}</code>). Downloads and registration accept only this certificate.</Note>
  }
  if (info.tls_mode === 'acme' || info.server_url.startsWith('https://')) {
    return <Note tone="ok">Verified with your public certificate.</Note>
  }
  return (
    <Note tone="warn">Traffic between agents and this server is not encrypted, and the token can be intercepted. <Link to="/admin/settings?tab=tls" className="font-medium text-accent hover:underline">Turn on TLS in Settings</Link>.</Note>
  )
}

function UrlWarning({ info }: { info: InstallInfo }) {
  const wrong = urlLooksWrong(info.server_url, window.location.hostname)
  if (info.server_url_set && !wrong) return null
  return (
    <Note tone="warn"><AlertTriangle className="mr-1 inline size-4" />
      {!info.server_url_set
        ? <>The server's public URL is not set, so the commands use <code className="mono">{info.server_url}</code> from your browser's address bar. </>
        : <>The server's public URL is <code className="mono">{info.server_url}</code>, which other hosts probably cannot reach. </>}
      <Link to="/admin/settings" className="font-medium text-accent hover:underline">Set it in Settings</Link>.
    </Note>
  )
}

function Waiting({ tok, since }: { tok: EnrolmentToken; since: number }) {
  const [now, setNow] = useState(() => Date.now() / 1000)
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now() / 1000), 15_000)
    return () => clearInterval(t)
  }, [])
  const live = now < tok.expires_at
  const hosts = useQuery({
    queryKey: ['hosts', 'new-since', since],
    queryFn: () => get<Host[]>('/hosts', { kind: 'agent' }).then((hs) => hs.filter((h) => h.created_at >= since)),
    refetchInterval: live ? 3000 : false,
  })
  return (
    <Card title="4. Waiting for the agent">
      {hosts.data?.length ? (
        <div className="space-y-1.5">
          {hosts.data.map((h) => (
            <div key={h.id} className="flex items-center gap-2 text-sm"><CheckCircle2 className="size-4 text-ok" />Connected: <Link to={`/hosts/${h.id}`} className="font-medium text-accent hover:underline">{h.name}</Link></div>
          ))}
        </div>
      ) : live ? (
        <div className="flex items-center gap-2 text-sm text-muted"><Spinner className="size-4" />Waiting for an agent to register with this token…</div>
      ) : (
        <p className="text-sm text-muted">The token has expired. Create a new one to continue.</p>
      )}
    </Card>
  )
}

function Tokens() {
  const qc = useQueryClient()
  const tokens = useQuery({ queryKey: ['tokens'], queryFn: () => get<any[]>('/enrollment-tokens') })
  if (!tokens.data?.length) return null
  return (
    <Card title="Unused tokens" bodyClass="p-0">
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
  )
}

// Summary: agents by version and write policy, and the ones that need an update.
function Summary({ info }: { info: InstallInfo }) {
  const { can } = useAuth()
  const toast = useToast()
  const confirm = useConfirm()
  const qc = useQueryClient()
  const hosts = useQuery({ queryKey: ['hosts', 'agents'], queryFn: () => get<Host[]>('/hosts', { kind: 'agent' }), refetchInterval: 30_000 })
  const stats = useMemo(() => {
    const byVersion = new Map<string, number>()
    const byPolicy = new Map<string, number>()
    const outdated: Host[] = []
    const latest = info.version && info.version !== 'unknown' && info.version !== 'dev' ? info.version : ''
    for (const h of hosts.data ?? []) {
      if (h.revoked) continue
      const v = h.agent_version || 'unknown'
      byVersion.set(v, (byVersion.get(v) ?? 0) + 1)
      const pol = h.write_policy ?? 'server'
      byPolicy.set(pol, (byPolicy.get(pol) ?? 0) + 1)
      if (latest && h.agent_version && h.agent_version !== 'dev' && h.agent_version !== latest) outdated.push(h)
    }
    return { byVersion: [...byVersion.entries()].sort((a, b) => b[1] - a[1]), byPolicy: [...byPolicy.entries()], outdated, total: (hosts.data ?? []).filter((h) => !h.revoked).length }
  }, [hosts.data, info.version])

  const update = async (h: Host) => {
    const ok = await confirm({
      title: 'Update agent', confirmLabel: 'Update',
      body: <p>Update the agent on <b>{h.name}</b> from {h.agent_version} to {info.version}? The new binary is signature-checked and self-tested before it replaces the old one, and it rolls back automatically if it cannot reconnect.{h.write_policy === 'operator' && ' This host requires an operator signature: the update waits under Jobs until you sign it.'}</p>,
    })
    if (!ok) return
    try {
      const j = await post<{ status?: string }>(`/hosts/${h.id}/update-agent`, { confirm: true })
      toast(j?.status === 'awaiting_signature' ? 'Agent update is waiting for your operator signature (see Jobs)' : 'Agent update queued')
      qc.invalidateQueries({ queryKey: ['jobs'] })
    } catch (e) {
      toast(errorMessage(e), 'crit')
    }
  }

  if (!hosts.data?.length) return null
  return (
    <Card title="Your agents" subtitle={`${stats.total} installed. This server offers version ${info.version}.`} bodyClass="p-0">
      <div className="grid gap-4 p-5 sm:grid-cols-3">
        <Stat label="Agents" value={stats.total} sub={stats.outdated.length ? `${stats.outdated.length} outdated` : 'all up to date'} tone={stats.outdated.length ? 'warn' : 'ok'} icon={<Server className="size-4" />} />
        <div>
          <div className="mb-1.5 text-xs font-medium text-muted">By version</div>
          <div className="flex flex-wrap gap-1.5">
            {stats.byVersion.map(([v, n]) => <Badge key={v} tone={v === info.version ? 'ok' : 'muted'}>{v} <b className="ml-1">{n}</b></Badge>)}
          </div>
        </div>
        <div>
          <div className="mb-1.5 text-xs font-medium text-muted">By write policy</div>
          <div className="flex flex-wrap gap-1.5">
            {stats.byPolicy.map(([k, n]) => <Badge key={k} tone={k === 'none' ? 'info' : k === 'operator' ? 'accent' : 'muted'}>{policyLabel[k] ?? k} <b className="ml-1">{n}</b></Badge>)}
          </div>
        </div>
      </div>
      {stats.outdated.length > 0 && (
        <div className="border-t border-line/70">
          <Table>
            <thead><tr><Th>Outdated host</Th><Th>Version</Th><Th>Policy</Th><Th /></tr></thead>
            <tbody>
              {stats.outdated.map((h) => (
                <tr key={h.id}>
                  <Td><Link to={`/hosts/${h.id}`} className="font-medium hover:text-accent">{h.name}</Link></Td>
                  <Td className="text-xs">{h.agent_version} <span className="text-muted">to {info.version}</span></Td>
                  <Td className="text-xs">{policyLabel[h.write_policy ?? 'server']}</Td>
                  <Td className="text-right">
                    {h.write_policy === 'none'
                      ? <span className="text-xs text-muted" title="Read-only agents refuse update jobs. Reinstall to update.">Read-only: reinstall</span>
                      : can('jobs') && <Button size="sm" icon={<ArrowUpCircle className="size-3.5" />} onClick={() => update(h)}>Update</Button>}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
    </Card>
  )
}
