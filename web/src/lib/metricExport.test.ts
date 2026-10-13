import { describe, expect, it } from 'vitest'
import { dispositionName, exportFilename, exportQuery, fileSlug } from './metricExport'

describe('metric export helpers', () => {
  it('builds filenames', () => {
    const d = new Date(2026, 9, 3, 7, 5)
    expect(exportFilename('Web 01', 'CPU & load', 'png', d)).toBe('web-01-cpu-load-20261003-0705.png')
    expect(exportFilename('', '', 'csv', d)).toBe('x-x-20261003-0705.csv')
  })

  it('slugs', () => {
    expect(fileSlug('  db.example.com ')).toBe('db.example.com')
    expect(fileSlug('../etc/passwd')).toBe('etc-passwd')
  })

  it('builds export queries', () => {
    expect(exportQuery('csv', '24h')).toEqual({ format: 'csv', range: 86400 })
    expect(exportQuery('json', '7d')).toEqual({ format: 'json', range: 604800 })
    expect(exportQuery('csv', 'custom', { start: 100.9, end: 200 })).toEqual({ format: 'csv', start: 100, end: 200 })
    expect(exportQuery('csv', 'custom', { start: 200, end: 100 })).toBeNull()
    expect(exportQuery('csv', 'custom')).toBeNull()
    expect(exportQuery('csv', 'custom', { start: NaN, end: 100 })).toBeNull()
  })

  it('reads the server filename', () => {
    expect(dispositionName('attachment; filename="web-metrics-20260101-0000.csv"')).toBe('web-metrics-20260101-0000.csv')
    expect(dispositionName(null)).toBeNull()
  })
})
