import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowUpCircle, FlaskConical } from 'lucide-react'
import { errorMessage, get, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { ago } from '@/lib/format'
import type { HostDetail, Job, Package } from '@/lib/types'
import { Badge, Button, Card, Checkbox, Empty, ErrorBox, Field, Input, Loading, Modal, Note, Segmented, Select, Table, Td, Th, useToast } from '@/components/ui'

export function HostPackages({ host }: { host: HostDetail }) {
  const [view, setView] = useState<'upgradable' | 'installed'>('upgradable')
  const [q, setQ] = useState('')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [dialog, setDialog] = useState(false)
  const { can } = useAuth()
  const pk = useQuery({ queryKey: ['packages', String(host.id), view, q], queryFn: () => get<Package[]>(`/hosts/${host.id}/packages`, { upgradable: view === 'upgradable' ? 1 : '', q }) })
  const inv = host.inventory
  const win = inv?.package_manager === 'windows'
  const keyOf = (p: Package) => (win ? p.kb || p.name : p.name)
  return (
    <div className="space-y-4">
      {inv?.error && <Note tone="warn">Inventory warning: {inv.error}</Note>}
      {!host.allow_upgrades && can('jobs') && <Note>Remote upgrades are off for this host. Dry runs still work; enable “Allow remote upgrades” in Settings to install updates from here.</Note>}
      <Card bodyClass="p-0"
        title={<span>{inv ? `${inv.upgradable_count} upgradable · ${inv.installed_count} installed` : 'Packages'}</span>}
        subtitle={inv ? `${inv.package_manager} · checked ${ago(inv.updated_at)}` : 'No inventory received yet'}
        actions={<>
          <Segmented options={[{ id: 'upgradable', label: 'Upgradable' }, { id: 'installed', label: 'Installed' }]} value={view} onChange={(v) => { setView(v); setSel(new Set()) }} />
          {can('jobs') && <Button size="sm" variant="primary" icon={<ArrowUpCircle className="size-3.5" />} onClick={() => setDialog(true)} disabled={!inv?.upgradable_count}>Upgrade…</Button>}
        </>}>
        <div className="border-b border-line/70 p-4"><Input placeholder="Search packages" value={q} onChange={(e) => setQ(e.target.value)} className="max-w-xs" /></div>
        {pk.isLoading ? <Loading /> : !pk.data?.length ? <Empty title={view === 'upgradable' ? 'Everything is up to date' : 'No packages'} /> : (
          <Table className="max-h-[600px] overflow-y-auto">
            <thead><tr>
              {view === 'upgradable' && can('jobs') && <Th className="w-8" />}
              <Th>{win ? 'Name' : 'Package'}</Th><Th>{view === 'upgradable' ? 'Installed' : 'Version'}</Th>
              {view === 'upgradable' && <Th>Available</Th>}<Th className="hidden md:table-cell">{win ? 'Publisher / category' : 'Arch / source'}</Th>
            </tr></thead>
            <tbody>
              {pk.data.map((p) => (
                <tr key={p.name + p.arch + p.kb}>
                  {view === 'upgradable' && can('jobs') && <Td><Checkbox checked={sel.has(keyOf(p))} onChange={(v) => { const s = new Set(sel); if (v) s.add(keyOf(p)); else s.delete(keyOf(p)); setSel(s) }} /></Td>}
                  <Td><span className="mono text-xs">{p.name}</span> {p.security && <Badge tone="crit">security</Badge>} {p.kb && <Badge>{p.kb}</Badge>}</Td>
                  <Td className="mono text-xs">{p.version}</Td>
                  {view === 'upgradable' && <Td className="mono text-xs text-ok">{p.new_version}</Td>}
                  <Td className="hidden text-xs text-muted md:table-cell">{win ? p.publisher : [p.arch, p.source].filter(Boolean).join(' · ')}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      {dialog && <UpgradeDialog host={host} packages={[...sel]} onClose={() => setDialog(false)} />}
    </div>
  )
}

export function UpgradeDialog({ host, packages, onClose }: { host: HostDetail; packages: string[]; onClose: () => void }) {
  const toast = useToast()
  const nav = useNavigate()
  const [scope, setScope] = useState<'all' | 'security' | 'selected'>(packages.length ? 'selected' : 'all')
  const [schedule, setSchedule] = useState<'now' | 'at' | 'maintenance'>('now')
  const [at, setAt] = useState('')
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const params: Record<string, string> = {}
  if (scope === 'security') params.security_only = 'true'
  if (scope === 'selected') params.packages = packages.join(',')
  const create = async (dry: boolean) => {
    setBusy(true)
    setErr(null)
    try {
      const j = await post<Job>('/jobs', {
        host_id: host.id, type: 'upgrade', params, dry_run: dry, confirm: !dry,
        schedule: dry ? 'now' : schedule, run_at: schedule === 'at' && at ? Math.floor(new Date(at).getTime() / 1000) : 0,
      })
      toast(dry ? 'Dry run started' : schedule === 'now' ? 'Upgrade started' : 'Upgrade scheduled')
      onClose()
      nav(`/jobs/${j.id}`)
    } catch (e) {
      setErr(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal open onClose={onClose} title={`Upgrade packages on ${host.name}`} size="lg"
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button icon={<FlaskConical className="size-4" />} loading={busy} onClick={() => create(true)}>Dry run</Button>
        <Button variant="danger" loading={busy} disabled={!host.allow_upgrades || typed !== host.name || (schedule === 'at' && !at)} onClick={() => create(false)}>
          {schedule === 'now' ? 'Upgrade now' : 'Schedule upgrade'}
        </Button>
      </>}>
      <div className="space-y-4">
        <Field label="What to upgrade">
          <Segmented value={scope} onChange={setScope} options={[
            { id: 'all', label: 'All updates' }, { id: 'security', label: 'Security only' },
            ...(packages.length ? [{ id: 'selected' as const, label: `Selected (${packages.length})` }] : []),
          ]} />
        </Field>
        {scope === 'selected' && <p className="mono break-all text-xs text-muted">{packages.join(', ')}</p>}
        <Field label="When">
          <Select value={schedule} onChange={(e) => setSchedule(e.target.value as any)}>
            <option value="now">Now</option>
            <option value="at">At a specific time</option>
            <option value="maintenance">During the next maintenance window that allows jobs</option>
          </Select>
        </Field>
        {schedule === 'at' && <Field label="Start at"><Input type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} /></Field>}
        <Note tone="warn">
          The package manager runs non-interactively and keeps existing configuration files. Output is captured in the job log and every step is audited. A dry run shows what would change without installing anything.
        </Note>
        {host.allow_upgrades ? (
          <Field label={<>Type the host name <b className="mono text-fg">{host.name}</b> to enable the upgrade button</>}>
            <Input value={typed} onChange={(e) => setTyped(e.target.value)} />
          </Field>
        ) : <Note>Remote upgrades are disabled for this host. Only a dry run is possible.</Note>}
        <ErrorBox error={err} />
      </div>
    </Modal>
  )
}
