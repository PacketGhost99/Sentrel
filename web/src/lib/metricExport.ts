// Helpers for exporting metrics as images and as CSV/JSON files.

export type ExportFormat = 'csv' | 'json'

function pad(n: number) {
  return String(n).padStart(2, '0')
}

/** Lower-case, filesystem-safe fragment: letters, digits, dots and dashes only. */
export function fileSlug(s: string): string {
  const out = s.trim().toLowerCase().replace(/[^a-z0-9.]+/g, '-').replace(/^[-.]+|[-.]+$/g, '')
  return out || 'x'
}

/** `<host>-<metric>-<YYYYMMDD-HHMM>.<ext>` in the browser's local time. */
export function exportFilename(host: string, metric: string, ext: string, when: Date = new Date()): string {
  const stamp = `${when.getFullYear()}${pad(when.getMonth() + 1)}${pad(when.getDate())}-${pad(when.getHours())}${pad(when.getMinutes())}`
  return `${fileSlug(host)}-${fileSlug(metric)}-${stamp}.${ext}`
}

export const EXPORT_PRESETS = [
  { id: '1h', label: '1h', seconds: 3600 },
  { id: '6h', label: '6h', seconds: 6 * 3600 },
  { id: '24h', label: '24h', seconds: 86400 },
  { id: '7d', label: '7d', seconds: 7 * 86400 },
  { id: '30d', label: '30d', seconds: 30 * 86400 },
] as const
export type ExportPreset = (typeof EXPORT_PRESETS)[number]['id'] | 'custom'

/**
 * Query for GET /hosts/{id}/metrics/export. Custom bounds are unix seconds;
 * returns null when a custom range is incomplete or inverted.
 */
export function exportQuery(format: ExportFormat, preset: ExportPreset, custom?: { start: number; end: number }): Record<string, string | number> | null {
  if (preset === 'custom') {
    if (!custom || !isFinite(custom.start) || !isFinite(custom.end) || custom.start >= custom.end) return null
    return { format, start: Math.floor(custom.start), end: Math.floor(custom.end) }
  }
  const p = EXPORT_PRESETS.find((x) => x.id === preset)
  return { format, range: p ? p.seconds : 3600 }
}

/** Parses the value of a datetime-local input (local time) to unix seconds, or NaN. */
export function localInputToUnix(v: string): number {
  const t = new Date(v).getTime()
  return isFinite(t) ? Math.floor(t / 1000) : NaN
}

/** Saves a blob through a temporary link (works with authenticated fetches). */
export function downloadBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}

/** Filename from a Content-Disposition header, if any. */
export function dispositionName(h: string | null): string | null {
  const m = h && /filename="?([^";]+)"?/i.exec(h)
  return m ? m[1] : null
}

/** Fetches the export with the session cookie and saves it. Throws with the server's message on failure. */
export async function downloadMetricsExport(hostId: number, query: Record<string, string | number>, fallbackName: string) {
  const q = new URLSearchParams(Object.entries(query).map(([k, v]) => [k, String(v)]))
  const res = await fetch(`/api/v1/hosts/${hostId}/metrics/export?${q}`, { credentials: 'same-origin' })
  if (!res.ok) {
    let msg = res.statusText || 'Export failed'
    try {
      const j = await res.json()
      if (j?.error) msg = j.error
    } catch {
      /* not JSON */
    }
    throw new Error(msg)
  }
  downloadBlob(await res.blob(), dispositionName(res.headers.get('Content-Disposition')) || fallbackName)
}
