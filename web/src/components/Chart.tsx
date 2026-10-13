import { useEffect, useMemo, useRef, useState } from 'react'
import uPlot from 'uplot'
import { Check, Copy, Download, MoreHorizontal } from 'lucide-react'
import type { Series } from '@/lib/types'
import { exportFilename, downloadBlob } from '@/lib/metricExport'
import { errorMessage } from '@/lib/api'
import { Segmented, useToast } from './ui'
import { align, buildChartOptions, type Unit } from './chartOptions'
import { canCopyImage, canvasToBlob, copyCanvas, renderChartImage } from './chartImage'

export { formatUnit, type Unit } from './chartOptions'

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

/** Small "..." menu on a chart: download or copy it as an image. */
function ChartMenu({ onDownload, onCopy }: { onDownload: () => void; onCopy?: () => void }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: Event) => {
      if (e.type === 'keydown' ? (e as KeyboardEvent).key === 'Escape' : !ref.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', close)
    }
  }, [open])
  const item = 'flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg hover:bg-panel-2'
  return (
    <div ref={ref} className="absolute right-1 top-0 z-10">
      <button type="button" aria-label="Chart menu" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}
        className="flex size-6 items-center justify-center rounded-md text-muted opacity-60 hover:bg-panel-2 hover:text-fg hover:opacity-100 focus-visible:opacity-100">
        <MoreHorizontal className="size-4" />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 mt-1 w-36 overflow-hidden rounded-lg border border-line bg-panel py-1 shadow-card">
          <button type="button" role="menuitem" className={item} onClick={() => { setOpen(false); onDownload() }}><Download className="size-3.5" />Download PNG</button>
          {onCopy && <button type="button" role="menuitem" className={item} onClick={() => { setOpen(false); onCopy() }}><Copy className="size-3.5" />Copy image</button>}
        </div>
      )}
    </div>
  )
}

export function Chart({ series, unit = 'number', height = 220, max, min, title = 'Chart', host, rangeLabel }: { series: Series[]; unit?: Unit; height?: number; max?: number; min?: number; title?: string; host?: string; rangeLabel?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const plot = useRef<uPlot | null>(null)
  const [width, setWidth] = useState(600)
  const data = useMemo(() => align(series), [series])
  const empty = data[0].length === 0
  const dark = useDarkMode()
  const toast = useToast()

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
    plot.current?.destroy()
    plot.current = new uPlot(buildChartOptions({ series, unit, width, height, dark, max, min }), data, ref.current)
    return () => {
      plot.current?.destroy()
      plot.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, unit, height, dark, series.length, max, min, empty])

  useEffect(() => {
    plot.current?.setSize({ width, height })
  }, [width, height])

  const image = () => renderChartImage({ series, unit, title, host, rangeLabel, max, min })
  const download = async () => {
    try {
      downloadBlob(await canvasToBlob(await image()), exportFilename(host || 'chart', title, 'png'))
    } catch (e) {
      toast(errorMessage(e), 'crit')
    }
  }
  const copy = async () => {
    try {
      await copyCanvas(image())
      toast(<span className="inline-flex items-center gap-1.5"><Check className="size-4" />Image copied</span>, 'ok')
    } catch (e) {
      toast(errorMessage(e), 'crit')
    }
  }

  return (
    <div className="relative w-full min-w-0">
      {!empty && <ChartMenu onDownload={download} onCopy={canCopyImage() ? copy : undefined} />}
      <div ref={ref} className="w-full min-w-0">
        {empty && <div className="flex items-center justify-center text-sm text-muted" style={{ height }}>No data for this period</div>}
      </div>
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
