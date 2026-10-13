import { describe, expect, it } from 'vitest'
import { critHigh, critHighLabel, critHighTitle, findingKBs } from './vulns'

describe('vulnerability counts', () => {
  it('badges use critical + high with a scope label', () => {
    expect(critHigh({ critical: 81, high: 564 })).toBe(645)
    expect(critHighLabel({ critical: 81, high: 564 })).toBe('645 critical/high')
    expect(critHighTitle({ critical: 81, high: 564, total: 3456 })).toBe('81 critical + 564 high open CVEs, 3,456 total')
    expect(critHigh(undefined)).toBe(0)
  })
  it('extracts KB ids from a Windows finding', () => {
    expect(findingKBs({ fixed_version: 'KB5044284', package: '2026-10 Cumulative Update (KB5044284), .NET update (KB5126052)' })).toEqual(['KB5044284', 'KB5126052'])
    expect(findingKBs({ fixed_version: 'Windows Update', package: 'Driver' })).toEqual([])
  })
})
