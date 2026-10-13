import { useState } from 'react'
import { Download, Image } from 'lucide-react'
import { errorMessage } from '@/lib/api'
import { downloadBlob, downloadMetricsExport, EXPORT_PRESETS, exportFilename, exportQuery, localInputToUnix, type ExportFormat, type ExportPreset } from '@/lib/metricExport'
import type { Series } from '@/lib/types'
import { Button, Field, Input, Modal, Segmented, useToast } from './ui'
import { canvasToBlob, renderChartImage, safeRatio, stackImages } from './chartImage'
import type { Unit } from './chartOptions'

export interface ExportPanel {
  title: string
  unit: Unit
  max?: number
  series: Series[]
}

/** "Export data": downloads every standard series for the host as CSV or JSON. */
export function ExportDataButton({ hostId, hostName }: { hostId: number; hostName: string }) {
  const [open, setOpen] = useState(false)
  const [preset, setPreset] = useState<ExportPreset>('24h')
  const [format, setFormat] = useState<ExportFormat>('csv')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const query = exportQuery(format, preset, { start: localInputToUnix(from), end: localInputToUnix(to) })
  const go = async () => {
    if (!query) return
    setBusy(true)
    try {
      await downloadMetricsExport(hostId, query, exportFilename(hostName, 'metrics', format))
      setOpen(false)
    } catch (e) {
      toast(errorMessage(e), 'crit')
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button size="sm" icon={<Download className="size-3.5" />} onClick={() => setOpen(true)}>Export data</Button>
      <Modal open={open} onClose={() => setOpen(false)} title="Export metrics" size="sm"
        footer={<>
          <Button onClick={() => setOpen(false)}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={!query} onClick={go}>Download</Button>
        </>}>
        <div className="space-y-4 text-sm">
          <p className="text-muted">All standard series for {hostName}: one column per series, one row per timestamp.</p>
          <div><span className="mb-1.5 block text-xs font-medium text-muted">Time range</span>
            <Segmented<ExportPreset> options={[...EXPORT_PRESETS.map((p) => ({ id: p.id as ExportPreset, label: p.label })), { id: 'custom', label: 'Custom' }]} value={preset} onChange={setPreset} />
          </div>
          {preset === 'custom' && (
            <div className="grid grid-cols-2 gap-3">
              <Field label="From"><Input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} /></Field>
              <Field label="To"><Input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
            </div>
          )}
          <div><span className="mb-1.5 block text-xs font-medium text-muted">Format</span>
            <Segmented<ExportFormat> options={[{ id: 'csv', label: 'CSV' }, { id: 'json', label: 'JSON' }]} value={format} onChange={setFormat} />
          </div>
        </div>
      </Modal>
    </>
  )
}

/** "Export all charts (PNG)": every panel with data, stacked into one tall image. */
export function ExportChartsButton({ hostName, rangeLabel, panels }: { hostName: string; rangeLabel: string; panels: ExportPanel[] }) {
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const usable = panels.filter((p) => p.series.some((s) => (s.points?.length ?? 0) > 0))
  const go = async () => {
    setBusy(true)
    try {
      // Let the button repaint before the synchronous canvas work.
      await new Promise((r) => setTimeout(r, 20))
      const approxCss = 64 + usable.length * 340
      const ratio = safeRatio(approxCss)
      const parts = usable.map((p) => renderChartImage({ series: p.series, unit: p.unit, title: p.title, host: hostName, rangeLabel, max: p.max }, ratio))
      const img = stackImages(parts, { title: `${hostName} metrics`, sub: `${rangeLabel}  ·  exported ${new Date().toLocaleString()}` }, ratio)
      downloadBlob(await canvasToBlob(img), exportFilename(hostName, 'all-charts', 'png'))
    } catch (e) {
      toast(errorMessage(e), 'crit')
    } finally {
      setBusy(false)
    }
  }
  return <Button size="sm" icon={<Image className="size-3.5" />} loading={busy} disabled={usable.length === 0} onClick={go}>Export all charts (PNG)</Button>
}
