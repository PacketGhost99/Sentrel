import { describe, expect, it } from 'vitest'
import { inactiveText, isKernelRemoval, packagesMatchAbi, parseKernelDryRun, remediationBadge, removalBlocker, showsNotInRepos, summarizeFleet } from './kernel'
import type { FleetKernelHost } from './types'

const host = (o: Partial<FleetKernelHost>): FleetKernelHost => ({ host_id: 1, host_name: 'a', running: '7.0.0-38-generic', removable: [], reboot_pending: false, allow_upgrades: true, write_policy: 'server', resolves: 0, resolves_critical: 0, ...o })

describe('kernel helpers', () => {
  it('badges', () => {
    expect(remediationBadge({ remediation: 'remove_old_kernel' })?.label).toBe('Old kernel (not running)')
    expect(remediationBadge({ remediation: 'reboot' })?.label).toContain('reboot')
    expect(remediationBadge({ remediation: 'upgrade' })).toBeNull()
    expect(remediationBadge({})).toBeNull()
  })
  it('not-in-repos hint only for wait', () => {
    expect(showsNotInRepos({ remediation: 'wait', fixed_version: '1' })).toBe(true)
    expect(showsNotInRepos({ remediation: 'remove_old_kernel', fixed_version: '1' })).toBe(false)
    expect(showsNotInRepos({ remediation: 'reboot', fixed_version: '1' })).toBe(false)
    expect(showsNotInRepos({ remediation: 'upgrade_kernel', fixed_version: '1' })).toBe(false)
    expect(showsNotInRepos({ remediation: 'none', fixed_version: '' })).toBe(false)
    expect(showsNotInRepos({ remediation: 'wait', fixed_version: '1', os: 'windows' })).toBe(false)
    expect(showsNotInRepos({ fixed_version: '1' })).toBe(true)
  })
  it('removal needs an abi', () => {
    expect(isKernelRemoval({ remediation: 'remove_old_kernel', kernel_abi: '7.0.0-28' })).toBe(true)
    expect(isKernelRemoval({ remediation: 'remove_old_kernel', kernel_abi: '' })).toBe(false)
    expect(isKernelRemoval({ remediation: 'reboot', kernel_abi: '7.0.0-28' })).toBe(false)
  })
  it('dry run parsing', () => {
    expect(parseKernelDryRun('{"abi":"7.0.0-28","packages":["a-7.0.0-28","b-7.0.0-28"],"bytes":1024}')).toEqual({ abi: '7.0.0-28', packages: ['a-7.0.0-28', 'b-7.0.0-28'], bytes: 1024 })
    expect(parseKernelDryRun('')).toBeNull()
    expect(parseKernelDryRun('nope')).toBeNull()
    expect(parseKernelDryRun('{"abi":"x"}')).toBeNull()
  })
  it('package sanity check', () => {
    expect(packagesMatchAbi(['linux-image-7.0.0-28-generic', 'linux-headers-7.0.0-28'], '7.0.0-28')).toBe(true)
    expect(packagesMatchAbi(['linux-image-7.0.0-28-generic', 'linux-image-7.0.0-38-generic'], '7.0.0-28')).toBe(false)
    expect(packagesMatchAbi([], '7.0.0-28')).toBe(false)
  })
  it('fleet summary', () => {
    const s = summarizeFleet([
      host({ removable: [{ abi: '7.0.0-28', cve_count: 5, critical: 1 }], resolves: 5, resolves_critical: 1 }),
      host({ host_id: 2, removable: [{ abi: 'a', cve_count: 1, critical: 0 }, { abi: 'b', cve_count: 2, critical: 0 }], resolves: 3 }),
      host({ host_id: 3, reboot_pending: true }),
    ])
    expect(s).toEqual({ hosts: 2, kernels: 3, cves: 8, critical: 1 })
  })
  it('blockers', () => {
    expect(removalBlocker({ allow_upgrades: true, write_policy: 'server' })).toBe('')
    expect(removalBlocker({ allow_upgrades: false, write_policy: 'server' })).toContain('upgrades')
    expect(removalBlocker({ allow_upgrades: true, write_policy: 'none' })).toContain('read-only')
    expect(removalBlocker({ allow_upgrades: true, write_policy: 'operator' })).toBe('')
  })
  it('inactive text', () => {
    expect(inactiveText(1300, 1150)).toBe('1300 (1150 in an inactive kernel)')
    expect(inactiveText(5, 0)).toBe('5')
  })
})
