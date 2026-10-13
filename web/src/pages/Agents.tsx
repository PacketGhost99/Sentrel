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
import { Badge, Button, Card, CodeBlock, CopyButton, ErrorBox, Field, Input, Loading, Note, PageHeader, Segmented, Select, InfoTip, Spinner, Table, Td, Th, Toggle, cx, useConfirm, useToast } from '@/components/ui'

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
      <PageHeader title="Agents" subtitle="Install and update the Sentrel agent" />
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

  const platformTitle = platforms.find((x) => x.id === platform)!.title
  return (
    <div className="space-y-5">
      <Summary info={info} />
      <TlsBanner info={info} />
      <UrlWarning info={info} />

      <Card title="Install an agent" info={<>A token is used once: the agent swaps it for a client certificate signed by this server. {tlsInfo(info)}</>}>
        <ol className="space-y-6">
          <Step n={1} title="Create a token">
            <div className="flex flex-wrap items-center gap-3">
              {tok ? (
                <>
                  <KeyRound className="size-4 text-accent" />
                  <code className="mono rounded-lg bg-panel-2 px-2.5 py-1 text-sm font-semibold">{tok.token}</code>
                  <CopyButton text={tok.token} />
                  <span className="text-xs text-muted">Expires {until(tok.expires_at)}. One host per token.</span>
                  <Button size="sm" loading={busy} onClick={create}>New token</Button>
                </>
              ) : (
                <>
                  <Button variant="primary" icon={<KeyRound className="size-4" />} loading={busy} onClick={create}>Create token</Button>
                  <span className="text-xs text-muted">Commands show a placeholder until you do.</span>
                </>
              )}
              <Field label="Valid for" className="ml-auto flex items-center gap-2 [&>span]:mb-0">
                <Select value={ttl} onChange={(e) => setTtl(Number(e.target.value))} className="h-8 text-xs">
                  <option value={60}>1 hour</option><option value={1440}>24 hours</option><option value={10080}>7 days</option>
                </Select>
              </Field>
            </div>
            <ErrorBox error={err} className="mt-3" />
            <details className="mt-3 rounded-xl border border-line/70 px-4 py-2.5 text-sm">
              <summary className="cursor-pointer select-none font-medium text-muted">Options: name, group, tags, write policy</summary>
              <div className="mt-3 grid gap-5 pb-1 lg:grid-cols-2">
                <PlacementFields p={p} onChange={setP} />
                <div className="space-y-3">
                  <Field label="Write policy" info={policyHelp[policy]}>
                    <Segmented value={policy} onChange={setPolicy} options={[{ id: 'server', label: 'Standard' }, { id: 'operator', label: 'Operator-signed' }, { id: 'none', label: 'Read-only' }]} />
                  </Field>
                  {policy === 'operator' && (
                    <Field label="Operator public key" hint={<>From <code className="mono">sentrel-sign keygen-operator</code>. Separate several keys with spaces.</>} error={keys.invalid ? 'Keys may only contain letters, digits and + / = _ -' : undefined}>
                      <Input className="mono" value={opKey} onChange={(e) => setOpKey(e.target.value)} placeholder="base64 public key" />
                    </Field>
                  )}
                  <Toggle checked={docker && linux} disabled={!linux} onChange={setDocker} label="Monitor Docker containers (Linux)" info="Adds the agent user to the docker group." />
                  {docker && linux && <Note tone="warn">Docker group membership is root-equivalent on the host. Only enable it on hosts you trust.</Note>}
                </div>
              </div>
            </details>
          </Step>

          <Step n={2} title="Choose a platform">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              {platforms.map((o) => (
                <button key={o.id} onClick={() => setPlatform(o.id)} aria-pressed={platform === o.id}
                  className={cx('flex items-start gap-3 rounded-xl border p-3 text-left [&_svg]:size-5', platform === o.id ? 'border-accent bg-accent/8' : 'border-line bg-panel hover:border-muted')}>
                  <span className={platform === o.id ? 'text-accent' : 'text-muted'}>{o.icon}</span>
                  <span><span className="block text-sm font-medium">{o.title}</span><span className="block text-xs text-muted">{o.sub}</span></span>
                </button>
              ))}
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-3">
              <span className="text-xs font-medium text-muted">Architecture</span>
              <Segmented value={platform === 'windows' ? 'amd64' : arch} onChange={setArch} options={[{ id: 'amd64', label: 'x86-64' }, { id: 'arm64', label: 'ARM64' }]} />
              {platform === 'windows' && <InfoTip>Windows on ARM runs the x64 agent under emulation.</InfoTip>}
            </div>
          </Step>

          <Step n={3} title={`Install on ${platformTitle}`}>
            <div className="space-y-5">
              <section className="space-y-2">
                <h4 className="flex items-center gap-1.5 text-sm font-semibold">Quick install
                  <InfoTip>{platform === 'windows' ? 'Run in an elevated Windows PowerShell (Run as administrator).' : 'Run as a user with sudo on the host.'}{platform === 'windows' && pinOf(info) && ' The pinned one-liner uses Windows PowerShell 5.1, the default on Windows. On PowerShell 7 use the MSI below.'}</InfoTip>
                </h4>
                <CodeBlock code={platform === 'windows' ? windowsCommand(opts) : scriptCommand(opts)} />
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
                    <p className="text-sm text-muted">No {platform === 'windows' ? 'MSI' : `${platform === 'deb' ? '.deb' : '.rpm'} ${arch === 'amd64' ? 'x86-64' : 'ARM64'}`} package on this server. Use the quick install above{info.packages.length === 0 ? ' (this build was made without nfpm or wixl)' : ''}.</p>
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
          </Step>
        </ol>
      </Card>

      {tok && <Waiting tok={tok} since={since} />}
      <Tokens />
    </div>
  )
}

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-full bg-accent/12 text-xs font-semibold text-accent">{n}</span>
      <div className="min-w-0 flex-1">
        <h3 className="mb-2.5 text-sm font-semibold">{title}</h3>
        {children}
      </div>
    </li>
  )
}

function tlsInfo(info: InstallInfo) {
  if (info.tls_mode === 'selfsigned') return <>Pinned to this server's certificate (fingerprint <code className="mono break-all">{info.ca_fingerprint}</code>); downloads and registration accept only that certificate.</>
  if (info.tls_mode === 'acme' || info.server_url.startsWith('https://')) return 'Verified with your public certificate.'
  return null
}

function TlsBanner({ info }: { info: InstallInfo }) {
  if (tlsInfo(info)) return null
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
    <Card title="Waiting for the agent">
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
    <Card title="Your agents" bodyClass="p-0"
      actions={<Badge tone={stats.outdated.length ? 'warn' : 'ok'}>{stats.outdated.length ? `${stats.outdated.length} outdated` : 'All up to date'}</Badge>}>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3.5 text-sm">
        <span><b className="tabular-nums">{stats.total}</b> <span className="text-muted">{stats.total === 1 ? 'agent' : 'agents'} · server offers {info.version}</span></span>
        <span className="flex flex-wrap items-center gap-1.5"><span className="text-xs text-muted">Versions</span>
          {stats.byVersion.map(([v, n]) => <Badge key={v} tone={v === info.version ? 'ok' : 'muted'}>{v} <b className="ml-1">{n}</b></Badge>)}
        </span>
        <span className="flex flex-wrap items-center gap-1.5"><span className="text-xs text-muted">Write policy</span>
          {stats.byPolicy.map(([k, n]) => <Badge key={k} tone={k === 'none' ? 'info' : k === 'operator' ? 'accent' : 'muted'}>{policyLabel[k] ?? k} <b className="ml-1">{n}</b></Badge>)}
        </span>
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
