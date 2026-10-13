import { useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { errorMessage, put } from '@/lib/api'
import { formatSetting, inheritHint, inheritState, parseNumberDraft, sameList, type HostSettingKey, type InheritedSetting, type InheritKind } from '@/lib/inherit'
import { Badge, Button, Input, TagInput, Toggle, useToast, cx } from '@/components/ui'

type Value = boolean | number | string[]

interface Props {
  label: ReactNode
  description?: ReactNode
  kind: InheritKind
  setting: InheritedSetting<any>
  /** Called with the new override, or null to go back to the global value. */
  onSave: (value: Value | null) => Promise<void>
  disabled?: boolean
  unit?: string
  min?: number
  max?: number
  placeholder?: string
  className?: string
}

/**
 * One setting row that shows the effective value, where it comes from and
 * lets the user override it for this host or reset it to the global default.
 * Handles boolean, number and list values.
 */
export function InheritableField({ label, description, kind, setting, onSave, disabled, unit, min = 1, max = 100000, placeholder, className }: Props) {
  const toast = useToast()
  const state = inheritState(setting)
  const initialNum = String(setting.override ?? setting.effective)
  const initialList: string[] = (setting.override as string[] | null) ?? []
  const [editing, setEditing] = useState(false)
  const [num, setNum] = useState(initialNum)
  const [list, setList] = useState<string[]>(initialList)
  const [busy, setBusy] = useState(false)
  const sig = JSON.stringify([setting.override, setting.effective])
  useEffect(() => {
    setEditing(false)
    setNum(initialNum)
    setList(initialList)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig])

  const run = async (v: Value | null) => {
    setBusy(true)
    try {
      await onSave(v)
    } catch (e) {
      toast(errorMessage(e), 'crit')
    } finally {
      setBusy(false)
    }
  }

  const inherited = state === 'inherited'
  const open = !inherited || editing
  const parsed = kind === 'number' ? parseNumberDraft(num, min, max) : undefined
  const dirty = kind === 'number' ? parsed?.value !== undefined && parsed.value !== setting.override : !sameList(list, initialList) || (inherited && editing && list.length > 0)
  const canSave = !disabled && !busy && (kind === 'number' ? parsed?.value !== undefined && dirty : kind === 'list' ? dirty : false)

  return (
    <div className={cx('flex flex-wrap items-start justify-between gap-x-6 gap-y-2 border-b border-line/60 py-3 last:border-0', className)}>
      <div className="min-w-0 flex-1 basis-64">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">{label}</span>
          {!inherited && <Badge tone="accent">Overridden</Badge>}
        </div>
        {description && <p className="mt-0.5 text-xs text-muted">{description}</p>}
        <p className="mt-1 text-xs text-muted">
          {inheritHint(kind, setting, unit)}
          {!inherited && !disabled && (
            <>
              {' · '}
              <button type="button" className="text-accent hover:underline disabled:opacity-50" disabled={busy} onClick={() => run(null)}>Reset to global</button>
            </>
          )}
        </p>
      </div>
      <div className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-2 sm:basis-80">
        {kind === 'bool' && (
          <>
            <Toggle checked={!!setting.effective} disabled={disabled || busy} onChange={(v) => run(v)} />
            {inherited && !disabled && (
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => run(!!setting.effective)}>Override</Button>
            )}
          </>
        )}
        {kind === 'number' && (
          <>
            {open ? (
              <>
                <Input type="number" className="w-28" min={min} max={max} disabled={disabled || busy} value={num} placeholder={placeholder}
                  aria-label={typeof label === 'string' ? label : undefined} onChange={(e) => setNum(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter' && canSave) run(parsed!.value!) }} />
                {unit && <span className="text-xs text-muted">{unit}</span>}
                {!disabled && <Button size="sm" variant="primary" disabled={!canSave} loading={busy} onClick={() => run(parsed!.value!)}>Save</Button>}
                {inherited && editing && <Button size="sm" variant="ghost" onClick={() => { setEditing(false); setNum(initialNum) }}>Cancel</Button>}
              </>
            ) : (
              <>
                <span className="text-sm tabular-nums">{formatSetting('number', setting.effective, unit)}</span>
                {!disabled && <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>Override</Button>}
              </>
            )}
            {open && parsed?.error && num !== initialNum && <span className="w-full text-right text-xs text-crit-ink">{parsed.error}</span>}
          </>
        )}
        {kind === 'list' && (
          <div className="flex w-full flex-col items-stretch gap-2">
            {inherited && !editing && (
              <div className="flex flex-wrap items-center justify-end gap-2">
                <span className="text-sm text-muted">{formatSetting('list', setting.effective)}</span>
                {!disabled && <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>Add entries</Button>}
              </div>
            )}
            {open && (
              <>
                <TagInput value={list} onChange={setList} placeholder={placeholder} />
                {!disabled && (
                  <div className="flex justify-end gap-2">
                    {inherited && <Button size="sm" variant="ghost" onClick={() => { setEditing(false); setList([]) }}>Cancel</Button>}
                    <Button size="sm" variant="primary" disabled={!canSave} loading={busy} onClick={() => run(list)}>Save</Button>
                  </div>
                )}
              </>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

/** Saves one host setting override (null resets it) and refreshes the host. */
export function useHostSetting(hostId: number) {
  const qc = useQueryClient()
  return (key: HostSettingKey) => async (value: Value | null) => {
    await put(`/hosts/${hostId}`, { settings: { [key]: value } })
    await qc.invalidateQueries({ queryKey: ['host', String(hostId)] })
    qc.invalidateQueries({ queryKey: ['security', hostId] })
    qc.invalidateQueries({ queryKey: ['hosts'] })
  }
}
