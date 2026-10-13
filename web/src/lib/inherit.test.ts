import { describe, expect, it } from 'vitest'
import { formatSetting, inheritHint, inheritState, parseNumberDraft, sameList, type InheritedSetting } from './inherit'

const inherited = <T,>(v: T): InheritedSetting<T> => ({ effective: v, override: null, global: v, inherited: true })

describe('formatSetting', () => {
  it('formats each kind', () => {
    expect(formatSetting('bool', true)).toBe('On')
    expect(formatSetting('bool', false)).toBe('Off')
    expect(formatSetting('number', 60, 'minutes')).toBe('60 minutes')
    expect(formatSetting('number', 20)).toBe('20')
    expect(formatSetting('list', ['10.0.0.0/8', '192.0.2.1'])).toBe('10.0.0.0/8, 192.0.2.1')
    expect(formatSetting('list', [])).toBe('none')
    expect(formatSetting('list', null)).toBe('none')
  })
})

describe('inheritHint', () => {
  it('shows the global value while inherited', () => {
    expect(inheritState(inherited(true))).toBe('inherited')
    expect(inheritHint('bool', inherited(true))).toBe('Inherited from global (On)')
    expect(inheritHint('number', inherited(20), 'failures')).toBe('Inherited from global (20 failures)')
    expect(inheritHint('list', inherited<string[]>([]))).toBe('Inherited from global (none)')
  })
  it('marks an override and keeps the global value visible', () => {
    const s: InheritedSetting<boolean> = { effective: true, override: true, global: false, inherited: false }
    expect(inheritState(s)).toBe('overridden')
    expect(inheritHint('bool', s)).toBe('Overridden. Global: Off')
  })
  it('treats an override equal to the global value as an override', () => {
    const s: InheritedSetting<number> = { effective: 60, override: 60, global: 60, inherited: false }
    expect(inheritState(s)).toBe('overridden')
  })
  it('describes list additions', () => {
    const s: InheritedSetting<string[]> = { effective: ['a', 'b'], override: ['b'], global: ['a'], inherited: false }
    expect(inheritHint('list', s)).toContain('Global entries still apply (a)')
  })
})

describe('parseNumberDraft', () => {
  it('validates range and shape', () => {
    expect(parseNumberDraft('15', 1, 100)).toEqual({ value: 15 })
    expect(parseNumberDraft('', 1, 100).error).toBeTruthy()
    expect(parseNumberDraft('1.5', 1, 100).error).toBeTruthy()
    expect(parseNumberDraft('0', 1, 100).error).toContain('between 1 and 100')
    expect(parseNumberDraft('abc', 1, 100).error).toBeTruthy()
  })
})

describe('sameList', () => {
  it('compares in order', () => {
    expect(sameList(['a'], ['a'])).toBe(true)
    expect(sameList(['a'], ['b'])).toBe(false)
    expect(sameList([], ['a'])).toBe(false)
  })
})
