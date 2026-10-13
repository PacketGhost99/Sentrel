import uPlot from 'uplot'
import type { Series } from '@/lib/types'
import { LOCALE } from '@/lib/format'
import { align, buildChartOptions, cssVar, seriesColor, type Unit } from './chartOptions'

export interface ChartImageSpec {
  series: Series[]
  unit: Unit
  title: string
  host?: string
  rangeLabel?: string
  max?: number
  min?: number
  /** CSS width of the image; defaults to 900. */
  width?: number
  /** CSS height of the plot area; defaults to 260. */
  height?: number
}

const FONT = 'ui-sans-serif, system-ui, sans-serif'
const PAD = 16
const MAX_LEGEND = 48

export function isDarkTheme() {
  return document.documentElement.classList.contains('dark')
}

function colors() {
  const dark = isDarkTheme()
  return { dark, bg: dark ? cssVar('--panel') : '#ffffff', fg: dark ? cssVar('--text') : '#0f172a', muted: cssVar('--muted') }
}

function subtitle(spec: ChartImageSpec): string {
  const xs = spec.series.flatMap((s) => (s.points || []).map((p) => p[0]))
  const parts: string[] = []
  if (spec.host) parts.push(spec.host)
  if (spec.rangeLabel) parts.push(spec.rangeLabel)
  if (xs.length) {
    const f = (t: number) => new Date(t * 1000).toLocaleString(LOCALE, { dateStyle: 'medium', timeStyle: 'short' })
    parts.push(`${f(Math.min(...xs))} to ${f(Math.max(...xs))}`)
  }
  return parts.join('  ·  ')
}

/** Lays out legend entries in wrapped rows; returns positions and total height. */
function layoutLegend(ctx: CanvasRenderingContext2D, names: string[], width: number) {
  ctx.font = `12px ${FONT}`
  const items: { x: number; y: number; i: number; text: string }[] = []
  let x = 0
  let y = 0
  const shown = names.slice(0, MAX_LEGEND)
  const labels = shown.map((n, i) => ({ i, text: n.length > 60 ? n.slice(0, 59) + '…' : n }))
  if (names.length > MAX_LEGEND) labels.push({ i: -1, text: `+${names.length - MAX_LEGEND} more` })
  for (const l of labels) {
    const w = (l.i >= 0 ? 16 : 0) + ctx.measureText(l.text).width + 16
    if (x > 0 && x + w > width) {
      x = 0
      y += 18
    }
    items.push({ x, y, ...l })
    x += w
  }
  return { items, height: items.length ? y + 18 : 0 }
}

/**
 * Renders a chart with title, host, time range and legend onto a canvas, at
 * `ratio` times its CSS size. The plot itself comes from an offscreen uPlot
 * drawn at that ratio, so the output is sharp regardless of the screen.
 */
export function renderChartImage(spec: ChartImageSpec, ratio = 2): HTMLCanvasElement {
  const { bg, fg, muted, dark } = colors()
  const W = spec.width ?? 900
  const plotH = spec.height ?? 260
  const inner = W - PAD * 2
  const data = align(spec.series)
  const empty = data[0].length === 0

  const scratch = document.createElement('canvas').getContext('2d')!
  const legend = layoutLegend(scratch, empty ? [] : spec.series.map((s) => s.name), inner)
  const headH = 22 + 20
  const H = PAD + headH + (legend.height ? legend.height + 8 : 0) + plotH + PAD

  const out = document.createElement('canvas')
  out.width = Math.round(W * ratio)
  out.height = Math.round(H * ratio)
  const ctx = out.getContext('2d')!
  ctx.scale(ratio, ratio)
  ctx.fillStyle = bg
  ctx.fillRect(0, 0, W, H)
  ctx.textBaseline = 'top'
  ctx.fillStyle = fg
  ctx.font = `600 16px ${FONT}`
  ctx.fillText(spec.title, PAD, PAD)
  ctx.fillStyle = muted
  ctx.font = `12px ${FONT}`
  ctx.fillText(subtitle(spec), PAD, PAD + 24)

  let y = PAD + headH
  if (legend.items.length) {
    ctx.font = `12px ${FONT}`
    for (const it of legend.items) {
      if (it.i >= 0) {
        ctx.fillStyle = seriesColor(it.i, spec.series.length)
        ctx.fillRect(PAD + it.x, y + it.y + 3, 10, 10)
      }
      ctx.fillStyle = fg
      ctx.fillText(it.text, PAD + it.x + (it.i >= 0 ? 16 : 0), y + it.y + 1)
    }
    y += legend.height + 8
  }

  if (empty) {
    ctx.fillStyle = muted
    ctx.font = `13px ${FONT}`
    ctx.textAlign = 'center'
    ctx.fillText('No data for this period', W / 2, y + plotH / 2 - 8)
    ctx.textAlign = 'left'
    return out
  }

  const holder = document.createElement('div')
  holder.style.cssText = 'position:fixed;left:-99999px;top:0;visibility:hidden'
  document.body.appendChild(holder)
  let u: uPlot | null = null
  try {
    u = new uPlot(buildChartOptions({ series: spec.series, unit: spec.unit, width: inner, height: plotH, dark, max: spec.max, min: spec.min, legend: false, pxRatio: ratio }), data, holder)
    ctx.drawImage(u.ctx.canvas, PAD, y, inner, plotH)
  } finally {
    u?.destroy()
    holder.remove()
  }
  return out
}

/** Stacks canvases (rendered at the same ratio) into one tall image with a header. */
export function stackImages(parts: HTMLCanvasElement[], header: { title: string; sub: string }, ratio: number, width = 900): HTMLCanvasElement {
  const { bg, fg, muted } = colors()
  const headH = 64
  const heightPx = parts.reduce((n, c) => n + c.height, 0) + headH * ratio
  const out = document.createElement('canvas')
  out.width = Math.round(width * ratio)
  out.height = heightPx
  const ctx = out.getContext('2d')!
  ctx.fillStyle = bg
  ctx.fillRect(0, 0, out.width, out.height)
  ctx.textBaseline = 'top'
  ctx.scale(ratio, ratio)
  ctx.fillStyle = fg
  ctx.font = `600 20px ${FONT}`
  ctx.fillText(header.title, PAD, PAD)
  ctx.fillStyle = muted
  ctx.font = `12px ${FONT}`
  ctx.fillText(header.sub, PAD, PAD + 30)
  ctx.setTransform(1, 0, 0, 1, 0, 0)
  let y = headH * ratio
  for (const c of parts) {
    ctx.drawImage(c, 0, y)
    y += c.height
  }
  return out
}

/** Largest ratio (up to 2) that keeps a stack of CSS height `cssHeight` inside canvas limits. */
export function safeRatio(cssHeight: number): number {
  return Math.max(1, Math.min(2, 16000 / cssHeight))
}

export function canvasToBlob(c: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => c.toBlob((b) => (b ? resolve(b) : reject(new Error('Could not encode image'))), 'image/png'))
}

export const canCopyImage = () => typeof ClipboardItem !== 'undefined' && !!navigator.clipboard?.write

export async function copyCanvas(c: HTMLCanvasElement) {
  await navigator.clipboard.write([new ClipboardItem({ 'image/png': canvasToBlob(c) })])
}
