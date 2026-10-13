import { describe, expect, it } from 'vitest'
import { seriesLabel } from './seriesLabel'

describe('seriesLabel', () => {
  it('maps known metrics', () => {
    expect(seriesLabel('cpu_usage_percent')).toBe('Usage')
    expect(seriesLabel('disk_read_bytes_per_sec sda1')).toBe('Read sda1')
  })
  it('uses the label value alone when the metric is implied by the chart', () => {
    expect(seriesLabel('disk_used_percent /boot')).toBe('/boot')
  })
  it('keeps unknown names', () => {
    expect(seriesLabel('something_else x')).toBe('something_else x')
    expect(seriesLabel('disk_used_percent')).toBe('disk_used_percent')
  })
})
