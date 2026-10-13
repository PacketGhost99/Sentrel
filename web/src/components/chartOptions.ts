import uPlot from 'uplot'
import type { Series } from '@/lib/types'
import { bits, bytes, LOCALE } from '@/lib/format'

export const palette = ['#6366f1', '#10b981', '#f59e0b', '#ef4444', '#a855f7', '#14b8a6', '#ec4899', '#84cc16', '#f97316', '#06b6d4', '#8b5cf6', '#eab308']

/** #rrggbb + alpha to an rgba() string; falls back to the input for other colour formats. */
export function withAlpha(color: string, a: number): string {
  const m = /^#([0-9a-f]{6})$/i.exec(color.trim())
  if (!m) return color
  const n = parseInt(m[1], 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${a})`
}

export type Unit = 'percent' | 'bytes' | 'bytes/s' | 'bits/s' | 'ms' | 'number' | 'celsius' | 'seconds'

export function formatUnit(v: number | null | undefined, unit: Unit): string {
  if (v === null || v === undefined || !isFinite(v)) return '–'
  switch (unit) {
    case 'percent':
      return `${v.toFixed(1)}%`
    case 'bytes':
      return bytes(v)
    case 'bytes/s':
      return `${bytes(v)}/s`
    case 'bits/s':
      return bits(v)
    case 'ms':
      return `${v.toFixed(v < 10 ? 1 : 0)} ms`
    case 'celsius':
      return `${v.toFixed(1)} °C`
    case 'seconds':
      return `${v.toFixed(0)} s`
    default:
      return Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(2)
  }
}

export function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888'
}

/** Aligns per-series points onto a shared timestamp axis for uPlot. */
export function align(series: Series[]): uPlot.AlignedData {
  const ts = new Set<number>()
  series.forEach((s) => s.points?.forEach((p) => ts.add(p[0])))
  const xs = Array.from(ts).sort((a, b) => a - b)
  const idx = new Map(xs.map((x, i) => [x, i]))
  const ys = series.map((s) => {
    const arr: (number | null)[] = new Array(xs.length).fill(null)
    s.points?.forEach((p) => {
      arr[idx.get(p[0])!] = p[1]
    })
    return arr
  })
  return [xs, ...ys] as uPlot.AlignedData
}

/** Colour used for series i (a lone series takes the accent colour). */
export function seriesColor(i: number, count: number): string {
  const accent = cssVar('--accent')
  return i === 0 && count === 1 && /^#[0-9a-f]{6}$/i.test(accent) ? accent : palette[i % palette.length]
}

export interface ChartOpts {
  series: Series[]
  unit: Unit
  width: number
  height: number
  dark: boolean
  max?: number
  min?: number
  legend?: boolean
  pxRatio?: number
}

export function buildChartOptions({ series, unit, width, height, dark, max, min, legend = true, pxRatio }: ChartOpts): uPlot.Options {
  const grid = withAlpha(cssVar('--border'), dark ? 0.7 : 0.8)
  const muted = cssVar('--muted')
  const axisFont = '11px Inter, ui-sans-serif, system-ui, sans-serif'
  return {
    width,
    height,
    ...(pxRatio ? { pxRatio } : {}),
    padding: [8, 8, 0, 0],
    cursor: { drag: { x: true, y: false }, points: { size: 7, width: 2 } },
    legend: { show: legend && series.length > 0, live: true },
    scales: { x: { time: true, range: (_u, lo, hi) => (hi - lo < 60 ? [lo - 300, hi + 60] : [lo, hi]) }, y: { range: (_u, lo, hi) => [min ?? Math.min(0, lo), max ?? (hi === lo ? hi + 1 : hi * 1.08)] } },
    axes: [
      { stroke: muted, font: axisFont, grid: { stroke: grid, width: 1, dash: [3, 4] }, ticks: { show: false } },
      { stroke: muted, font: axisFont, grid: { stroke: grid, width: 1, dash: [3, 4] }, ticks: { show: false }, size: 64, values: (_u, vals) => vals.map((v) => formatUnit(v, unit)) },
    ],
    series: [
      { value: (_u, v) => (v ? new Date(v * 1000).toLocaleString(LOCALE) : '') },
      ...series.map((s, i) => {
        const color = seriesColor(i, series.length)
        return {
          label: s.name,
          stroke: color,
          width: 1.5,
          fill: series.length === 1
            ? (u: uPlot) => {
              // bbox is NaN before the first layout pass (e.g. a hidden or
              // zero-size container); a solid fill avoids a canvas exception.
              const { top, height } = u.bbox
              if (!Number.isFinite(top) || !Number.isFinite(height)) return withAlpha(color, 0.15)
              const g = u.ctx.createLinearGradient(0, top, 0, top + height)
              g.addColorStop(0, withAlpha(color, 0.28))
              g.addColorStop(1, withAlpha(color, 0))
              return g
            }
            : undefined,
          spanGaps: false,
          points: { show: false },
          value: (_u: uPlot, v: number | null) => formatUnit(v, unit),
        }
      }),
    ],
  }
}
