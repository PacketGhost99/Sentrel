import { describe, expect, it } from 'vitest'
import { bits, bytes, duration, pct, severityRank, timezones } from './format'

describe('format', () => {
  it('bytes', () => {
    expect(bytes(0)).toBe('0 B')
    expect(bytes(1023)).toBe('1023 B')
    expect(bytes(1024)).toBe('1.0 KB')
    expect(bytes(1536)).toBe('1.5 KB')
    expect(bytes(1024 ** 3, 2)).toBe('1.00 GB')
    expect(bytes('2048')).toBe('2.0 KB')
    expect(bytes(Infinity)).toBe('–')
  })
  it('bits', () => {
    expect(bits(999)).toBe('999 bps')
    expect(bits(1000)).toBe('1.0 Kbps')
    expect(bits(2_500_000)).toBe('2.5 Mbps')
    expect(bits(1e9)).toBe('1.0 Gbps')
  })
  it('pct', () => {
    expect(pct(12.345)).toBe('12.3%')
    expect(pct(50, 0)).toBe('50%')
    expect(pct(null)).toBe('–')
    expect(pct(undefined)).toBe('–')
    expect(pct(NaN)).toBe('–')
  })
  it('duration', () => {
    expect(duration(0)).toBe('0s')
    expect(duration(59)).toBe('59s')
    expect(duration(61)).toBe('1m 1s')
    expect(duration(3661)).toBe('1h 1m')
    expect(duration(90061)).toBe('1d 1h')
    expect(duration(-5)).toBe('0s')
  })
  it('timezones starts with UTC', () => {
    const z = timezones()
    expect(z[0]).toBe('UTC')
    expect(z.filter((x) => x === 'UTC')).toHaveLength(1)
  })
  it('severityRank', () => {
    expect(severityRank('critical')).toBe(2)
    expect(severityRank('warning')).toBe(1)
    expect(severityRank('info')).toBe(0)
    expect(severityRank()).toBe(0)
  })
})
