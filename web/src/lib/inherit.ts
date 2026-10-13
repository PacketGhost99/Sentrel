// Display logic for settings that inherit a global default and can be
// overridden per host. The server reports each setting as
// { effective, override, global, inherited }.

export type InheritKind = 'bool' | 'number' | 'list'

export interface InheritedSetting<T> {
  effective: T
  /** The host's own value; null when it inherits. */
  override: T | null
  /** The global default. */
  global: T
  inherited: boolean
}

export type HostSettingKey =
  | 'allow_ip_block' | 'auto_block' | 'allow_cleanup' | 'allow_upgrades'
  | 'bruteforce_enabled' | 'bruteforce_threshold' | 'bruteforce_window_minutes'
  | 'portscan_enabled' | 'portscan_threshold_per_min'
  | 'auto_block_threshold' | 'auto_block_minutes' | 'never_block'

export interface HostSettingsMap {
  allow_ip_block: InheritedSetting<boolean>
  auto_block: InheritedSetting<boolean>
  allow_cleanup: InheritedSetting<boolean>
  allow_upgrades: InheritedSetting<boolean>
  bruteforce_enabled: InheritedSetting<boolean>
  bruteforce_threshold: InheritedSetting<number>
  bruteforce_window_minutes: InheritedSetting<number>
  portscan_enabled: InheritedSetting<boolean>
  portscan_threshold_per_min: InheritedSetting<number>
  auto_block_threshold: InheritedSetting<number>
  auto_block_minutes: InheritedSetting<number>
  never_block: InheritedSetting<string[]>
}

export function formatSetting(kind: InheritKind, v: unknown, unit?: string): string {
  if (kind === 'bool') return v ? 'On' : 'Off'
  if (kind === 'list') {
    const l = (v as string[] | null | undefined) ?? []
    return l.length ? l.join(', ') : 'none'
  }
  return unit ? `${v} ${unit}` : String(v)
}

export type InheritState = 'inherited' | 'overridden'

export function inheritState(s: Pick<InheritedSetting<unknown>, 'override' | 'inherited'>): InheritState {
  return s.inherited && s.override === null ? 'inherited' : 'overridden'
}

/** The line shown under a setting: where its value comes from. */
export function inheritHint(kind: InheritKind, s: InheritedSetting<unknown>, unit?: string): string {
  if (inheritState(s) === 'inherited') return `Inherited from global (${formatSetting(kind, s.global, unit)})`
  if (kind === 'list') return `Added on this host. Global entries still apply (${formatSetting(kind, s.global)})`
  return `Overridden. Global: ${formatSetting(kind, s.global, unit)}`
}

/** Validates a numeric draft; returns the number or an error message. */
export function parseNumberDraft(draft: string, min: number, max: number): { value?: number; error?: string } {
  const t = draft.trim()
  if (t === '') return { error: 'Enter a number' }
  const n = Number(t)
  if (!Number.isFinite(n) || !Number.isInteger(n)) return { error: 'Enter a whole number' }
  if (n < min || n > max) return { error: `Must be between ${min} and ${max}` }
  return { value: n }
}

export function sameList(a: unknown, b: unknown): boolean {
  if (!Array.isArray(a) || !Array.isArray(b)) return false
  return a.length === b.length && a.every((x, i) => x === b[i])
}
