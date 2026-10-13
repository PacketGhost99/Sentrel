import type { Finding } from './types'

/*
 * Count model used on every page:
 *  - the unit is one open CVE on one host (a CVE on several packages counts once);
 *  - the severity of a CVE is the highest among its packages;
 *  - a badge shows critical + high only;
 *  - "fixable" means actionable now: a package upgrade, a kernel upgrade,
 *    removing an old kernel, a reboot, or (Windows) installing a pending update.
 */

export const CRIT_HIGH_TITLE = 'Critical and high severity CVEs (open, one per host and CVE)'
export const FIXABLE_TITLE = 'Fixable now: a package or Windows update can be installed, an old kernel removed, or the host rebooted'

export interface VulnCounts {
  critical: number
  high: number
  medium: number
  low: number
  other: number
  total: number
  fixable: number
  kev: number
  inactive: number
}

export const nf = (n: number) => n.toLocaleString('en-US')

export function critHigh(c?: { critical?: number; high?: number } | null): number {
  return (c?.critical || 0) + (c?.high || 0)
}

/** "645 critical/high" */
export function critHighLabel(c?: { critical?: number; high?: number } | null): string {
  return `${nf(critHigh(c))} critical/high`
}

/** Tooltip of a critical/high badge: the split plus the total. */
export function critHighTitle(c?: Partial<VulnCounts> | null): string {
  const t = c?.total != null ? `, ${nf(c.total)} total` : ''
  return `${nf(c?.critical || 0)} critical + ${nf(c?.high || 0)} high open CVEs${t}`
}

/** Windows KB ids named by a finding (its fix and the updates in its package list). */
export function findingKBs(f: Pick<Finding, 'package' | 'fixed_version'>): string[] {
  const m = `${f.fixed_version} ${f.package}`.match(/KB\d{4,}/gi) || []
  return Array.from(new Set(m.map((x) => x.toUpperCase())))
}
