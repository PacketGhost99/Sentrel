// Browser locale, sanitised: some systems report tags such as "en-US@posix"
// that Intl rejects, which would otherwise crash every date format call.
export const LOCALE: string = (() => {
  const cands = [...(typeof navigator !== 'undefined' ? navigator.languages || [] : []), typeof navigator !== 'undefined' ? navigator.language : '']
  for (const raw of cands) {
    const tag = (raw || '').split('@')[0].replace('_', '-')
    if (!tag) continue
    try { new Intl.DateTimeFormat(tag); return tag } catch { /* try the next one */ }
  }
  return 'en-US'
})()

// Formatting helpers.

export function num(v: unknown): number {
  if (typeof v === 'number') return v
  if (typeof v === 'string' && v !== '') return Number(v)
  return 0
}

export function bytes(v: unknown, digits = 1): string {
  let n = num(v)
  if (!isFinite(n)) return '–'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  while (Math.abs(n) >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : digits)} ${units[i]}`
}

export function bits(v: unknown): string {
  let n = num(v)
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps']
  let i = 0
  while (Math.abs(n) >= 1000 && i < units.length - 1) {
    n /= 1000
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function pct(v: number | null | undefined, digits = 1): string {
  if (v === null || v === undefined || !isFinite(v)) return '–'
  return `${v.toFixed(digits)}%`
}

export function duration(seconds: unknown): string {
  let s = Math.max(0, Math.floor(num(seconds)))
  const d = Math.floor(s / 86400)
  s %= 86400
  const h = Math.floor(s / 3600)
  s %= 3600
  const m = Math.floor(s / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s % 60}s`
  return `${s}s`
}

export function ago(unix: number | null | undefined): string {
  if (!unix) return 'never'
  const diff = Date.now() / 1000 - unix
  if (diff < 0) return 'in ' + duration(-diff)
  if (diff < 10) return 'just now'
  if (diff < 60) return `${Math.floor(diff)}s ago`
  return duration(diff).split(' ')[0] + ' ago'
}

export function until(unix: number | null | undefined): string {
  if (!unix) return '–'
  const diff = unix - Date.now() / 1000
  if (diff <= 0) return 'now'
  return 'in ' + duration(diff)
}

export function dateTime(unix: number | null | undefined): string {
  if (!unix) return '–'
  return new Date(unix * 1000).toLocaleString(LOCALE, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

export function shortDate(unix: number | null | undefined): string {
  if (!unix) return '–'
  return new Date(unix * 1000).toLocaleDateString(LOCALE, { month: 'short', day: 'numeric' })
}

export function plural(n: number, word: string, pluralWord?: string): string {
  return `${n} ${n === 1 ? word : pluralWord || word + 's'}`
}

export function osLabel(h: { os?: string; platform?: string; platform_version?: string }): string {
  if (!h) return ''
  if (h.os === 'windows') return h.platform || 'Windows'
  const p = h.platform ? h.platform.charAt(0).toUpperCase() + h.platform.slice(1) : h.os || ''
  return [p, h.platform_version].filter(Boolean).join(' ')
}

/** IANA zones for pickers; Intl omits the plain "UTC" alias. */
export function timezones(): string[] {
  let z: string[] = []
  try {
    z = (Intl as any).supportedValuesOf('timeZone') as string[]
  } catch {
    /* old browser */
  }
  return ['UTC', ...z.filter((x) => x !== 'UTC')]
}

export function localZone(): string {
  const z = Intl.DateTimeFormat(LOCALE).resolvedOptions().timeZone || 'UTC'
  return z === 'Etc/UTC' || z === 'Etc/GMT' ? 'UTC' : z
}

export function severityRank(s?: string): number {
  return s === 'critical' ? 2 : s === 'warning' ? 1 : 0
}
