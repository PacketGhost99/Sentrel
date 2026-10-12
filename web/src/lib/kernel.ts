import type { FleetKernelHost, Finding, Remediation } from './types'

/** Short label and tone for the remediation badge of a finding (null = nothing to say). */
export function remediationBadge(f: Pick<Finding, 'remediation' | 'kernel_abi'>): { label: string; tone: 'info' | 'warn' | 'muted' } | null {
  switch (f.remediation) {
    case 'remove_old_kernel': return { label: 'Old kernel (not running)', tone: 'info' }
    case 'reboot': return { label: 'Newer kernel installed — reboot to apply', tone: 'warn' }
    case 'upgrade_kernel': return { label: 'Kernel upgrade needed', tone: 'warn' }
    default: return null
  }
}

/** Whether the "Fix X not yet in repositories" hint applies; only for the wait remediation. */
export function showsNotInRepos(f: Pick<Finding, 'remediation' | 'fixed_version' | 'os'>): boolean {
  if (f.os === 'windows') return false
  if (f.remediation) return f.remediation === 'wait' && !!f.fixed_version
  return !!f.fixed_version
}

/** True when the finding's action is removing a kernel (needs the remove dialog). */
export function isKernelRemoval(f: Pick<Finding, 'remediation' | 'kernel_abi'>): boolean {
  return f.remediation === 'remove_old_kernel' && !!f.kernel_abi
}

/** Packages to pass to the upgrade job for an upgrade_kernel finding (the meta-packages). */
export function kernelUpgradePackages(f: Pick<Finding, 'kernel_meta'>): string[] {
  return f.kernel_meta || []
}

export function isKernelAction(r?: Remediation): boolean {
  return r === 'remove_old_kernel' || r === 'reboot' || r === 'upgrade_kernel'
}

export interface DryRunResult { abi: string; packages: string[]; bytes: number }

/** Parses the result_json of a remove_kernel dry run. */
export function parseKernelDryRun(raw?: string | null): DryRunResult | null {
  if (!raw) return null
  try {
    const r = JSON.parse(raw)
    if (!r || !Array.isArray(r.packages)) return null
    return { abi: String(r.abi || ''), packages: r.packages.map(String).filter(Boolean), bytes: Number(r.bytes) || 0 }
  } catch {
    return null
  }
}

/** Whether a dry-run package list belongs to the requested ABI only (a sanity check before confirming). */
export function packagesMatchAbi(packages: string[], abi: string): boolean {
  return packages.length > 0 && packages.every((p) => p.includes(abi))
}

export interface FleetSummary { hosts: number; kernels: number; cves: number; critical: number }

export function summarizeFleet(hosts: FleetKernelHost[]): FleetSummary {
  const withOld = hosts.filter((h) => h.removable.length > 0)
  return {
    hosts: withOld.length,
    kernels: withOld.reduce((n, h) => n + h.removable.length, 0),
    cves: withOld.reduce((n, h) => n + h.resolves, 0),
    critical: withOld.reduce((n, h) => n + h.resolves_critical, 0),
  }
}

/** Why a host cannot be changed from the UI ('' when it can). */
export function removalBlocker(h: Pick<FleetKernelHost, 'allow_upgrades' | 'write_policy'>): string {
  if (h.write_policy === 'none') return 'read-only agent'
  if (!h.allow_upgrades) return 'remote upgrades off'
  return ''
}

/** "1300 CVEs (1150 in an inactive kernel)" style text. */
export function inactiveText(total: number, inactive: number): string {
  if (!inactive) return `${total}`
  return `${total} (${inactive} in an inactive kernel)`
}
