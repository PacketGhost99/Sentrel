import { useEffect, useMemo, useRef, useState } from 'react'
import uPlot from 'uplot'
import type { Series } from '@/lib/types'
import { bits, bytes } from '@/lib/format'
import { Segmented } from './ui'

const palette = ['#6366f1', '#10b981', '#f59e0b', '#ef4444', '#a855f7', '#14b8a6', '#ec4899', '#84cc16', '#f97316', '#06b6d4', '#8b5cf6', '#eab308']

/** #rrggbb + alpha to an rgba() string; falls back to the input for other colour formats. */
function withAlpha(color: string, a: number): string {
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

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888'
}

/** Tracks the `.dark` class on <html> so charts redraw when the theme is toggled. */
function useDarkMode() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  useEffect(() => {
    const el = document.documentElement
    const mo = new MutationObserver(() => setDark(el.classList.contains('dark')))
    mo.observe(el, { attributes: true, attributeFilter: ['class'] })
    return () => mo.disconnect()
  }, [])
  return dark
}

/** Aligns per-series points onto a shared timestamp axis for uPlot. */
function align(series: Series[]): uPlot.AlignedData {
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

export function Chart({ series, unit = 'number', height = 220, max, min }: { series: Series[]; unit?: Unit; height?: number; max?: number; min?: number }) {
  const ref = useRef<HTMLDivElement>(null)
  const plot = useRef<uPlot | null>(null)
  const [width, setWidth] = useState(600)
  const data = useMemo(() => align(series), [series])
  const empty = data[0].length === 0
  const dark = useDarkMode()

  useEffect(() => {
    if (!ref.current) return
    const ro = new ResizeObserver((e) => setWidth(Math.max(200, Math.floor(e[0].contentRect.width))))
    ro.observe(ref.current)
    return () => ro.disconnect()
  }, [])

  useEffect(() => {
    if (!ref.current || empty) {
      plot.current?.destroy()
      plot.current = null
      return
    }
    const grid = withAlpha(cssVar('--border'), dark ? 0.7 : 0.8)
    const muted = cssVar('--muted')
    const accent = cssVar('--accent')
    const colorOf = (i: number) => (i === 0 && series.length === 1 && /^#[0-9a-f]{6}$/i.test(accent) ? accent : palette[i % palette.length])
    const axisFont = '11px Inter, ui-sans-serif, system-ui, sans-serif'
    const opts: uPlot.Options = {
      width,
      height,
      padding: [8, 8, 0, 0],
      cursor: { drag: { x: true, y: false }, points: { size: 7, width: 2 } },
      legend: { show: series.length > 0, live: true },
      scales: { x: { time: true, range: (_u, lo, hi) => (hi - lo < 60 ? [lo - 300, hi + 60] : [lo, hi]) }, y: { range: (_u, lo, hi) => [min ?? Math.min(0, lo), max ?? (hi === lo ? hi + 1 : hi * 1.08)] } },
      axes: [
        { stroke: muted, font: axisFont, grid: { stroke: grid, width: 1, dash: [3, 4] }, ticks: { show: false } },
        { stroke: muted, font: axisFont, grid: { stroke: grid, width: 1, dash: [3, 4] }, ticks: { show: false }, size: 64, values: (_u, vals) => vals.map((v) => formatUnit(v, unit)) },
      ],
      series: [
        { value: (_u, v) => (v ? new Date(v * 1000).toLocaleString() : '') },
        ...series.map((s, i) => {
          const color = colorOf(i)
          return {
            label: s.name,
            stroke: color,
            width: 1.5,
            fill: series.length === 1
              ? (u: uPlot) => {
                const g = u.ctx.createLinearGradient(0, u.bbox.top, 0, u.bbox.top + u.bbox.height)
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
    plot.current?.destroy()
    plot.current = new uPlot(opts, data, ref.current)
    return () => {
      plot.current?.destroy()
      plot.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, unit, height, dark, series.length, max, min, empty])

  useEffect(() => {
    plot.current?.setSize({ width, height })
  }, [width, height])

  return (
    <div ref={ref} className="w-full min-w-0">
      {empty && <div className="flex items-center justify-center text-sm text-muted" style={{ height }}>No data for this period</div>}
    </div>
  )
}

export const RANGES = [
  { id: '1h', label: '1h', seconds: 3600 },
  { id: '6h', label: '6h', seconds: 6 * 3600 },
  { id: '24h', label: '24h', seconds: 86400 },
  { id: '7d', label: '7d', seconds: 7 * 86400 },
  { id: '30d', label: '30d', seconds: 30 * 86400 },
] as const
export type RangeId = (typeof RANGES)[number]['id']

export function RangePicker({ value, onChange }: { value: RangeId; onChange: (r: RangeId) => void }) {
  return <Segmented options={RANGES.map((r) => ({ id: r.id, label: r.label }))} value={value} onChange={onChange} />
}

export function rangeSeconds(r: RangeId) {
  return RANGES.find((x) => x.id === r)?.seconds ?? 3600
}

/** Tiny inline sparkline (SVG) for tables. */
export function Sparkline({ points, width = 80, height = 22, color = 'var(--accent)' }: { points: number[]; width?: number; height?: number; color?: string }) {
  if (points.length < 2) return <svg width={width} height={height} />
  const lo = Math.min(...points)
  const hi = Math.max(...points)
  const span = hi - lo || 1
  const d = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${(i / (points.length - 1)) * width},${height - 2 - ((p - lo) / span) * (height - 4)}`).join(' ')
  return (
    <svg width={width} height={height} className="overflow-visible">
      <path d={d} fill="none" stroke={color} strokeWidth="1.5" />
    </svg>
  )
}
