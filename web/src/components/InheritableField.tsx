import { useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { RotateCcw } from 'lucide-react'
import { errorMessage, put } from '@/lib/api'
import { formatSetting, inheritHint, inheritState, parseNumberDraft, sameList, type HostSettingKey, type InheritedSetting, type InheritKind } from '@/lib/inherit'
import { Button, Input, SettingRow, TagInput, Toggle, useToast, cx } from '@/components/ui'

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

/** "Default" or "Custom" chip; a custom value gets a reset button. */
function SourceChip({ inherited, title, onReset, busy, disabled }: { inherited: boolean; title: string; onReset: () => void; busy: boolean; disabled?: boolean }) {
  if (inherited) return <span title={title} className="w-[4.75rem] text-right text-[11px] font-medium text-muted">Default</span>
  return (
    <span className="inline-flex w-[4.75rem] items-center justify-end gap-0.5" title={title}>
      <span className="rounded-full bg-accent/12 px-1.5 py-0.5 text-[11px] font-medium leading-4 text-accent">Custom</span>
      {!disabled && (
        <button type="button" aria-label="Reset to global default" title="Reset to global default" disabled={busy} onClick={onReset}
          className="rounded-md p-1 text-muted hover:bg-panel-2 hover:text-fg disabled:opacity-50">
          <RotateCcw className="size-3.5" />
        </button>
      )}
    </span>
  )
}

/**
 * One compact setting row: the effective value as an editable control and a
 * chip that says whether it is the global default or a custom value for this
 * host. Changing the value creates the override; the reset icon removes it.
 * The description and the global value are in the (i) tooltip.
 */
export function InheritableField({ label, description, kind, setting, onSave, disabled, unit, min = 1, max = 100000, placeholder, className }: Props) {
  const toast = useToast()
  const inherited = inheritState(setting) === 'inherited'
  const initialNum = String(setting.effective)
  const initialList: string[] = kind === 'list' && Array.isArray(setting.override) ? (setting.override as string[]) : []
  const [num, setNum] = useState(initialNum)
  const [list, setList] = useState<string[]>(initialList)
  const [editingList, setEditingList] = useState(false)
  const [busy, setBusy] = useState(false)
  const sig = JSON.stringify([setting.override, setting.effective])
  useEffect(() => {
    setNum(initialNum)
    setList(initialList)
    setEditingList(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig])

  const run = async (v: Value | null) => {
    setBusy(true)
    try {
      await onSave(v)
    } catch (e) {
      toast(errorMessage(e), 'crit')
      setNum(initialNum)
    } finally {
      setBusy(false)
    }
  }

  const hint = inheritHint(kind, setting, unit)
  const info = description ? <><span className="block">{description}</span><span className="mt-1 block text-muted">{hint}</span></> : hint
  const chip = <SourceChip inherited={inherited} title={hint} busy={busy} disabled={disabled} onReset={() => run(null)} />

  const parsed = kind === 'number' ? parseNumberDraft(num, min, max) : undefined
  const commitNum = () => {
    if (num === initialNum) return
    if (parsed?.value === undefined) return
    if (parsed.value === setting.effective) return
    run(parsed.value)
  }

  if (kind === 'list') {
    const listDirty = !sameList(list, initialList)
    return (
      <div className={cx('py-2', className)}>
        <SettingRow label={<span className="font-medium">{label}</span>} info={info} className="py-0">
          <span className="max-w-56 truncate text-sm text-muted" title={formatSetting('list', setting.effective)}>{formatSetting('list', setting.effective)}</span>
          {!disabled && !editingList && <Button size="sm" variant="ghost" onClick={() => setEditingList(true)}>Edit</Button>}
          {chip}
        </SettingRow>
        {editingList && (
          <div className="mt-2 space-y-2">
            <TagInput value={list} onChange={setList} placeholder={placeholder} />
            <p className="text-xs text-muted">These entries are added to the global list.</p>
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => { setEditingList(false); setList(initialList) }}>Cancel</Button>
              <Button size="sm" variant="primary" disabled={!listDirty || busy} loading={busy} onClick={() => run(list.length ? list : null)}>Save</Button>
            </div>
          </div>
        )}
      </div>
    )
  }

  return (
    <SettingRow label={<span className="font-medium">{label}</span>} info={info} className={className}>
      {kind === 'bool' && <Toggle checked={!!setting.effective} disabled={disabled || busy} onChange={(v) => run(v)} />}
      {kind === 'number' && (
        <>
          <Input type="number" className={cx('h-8 w-24 text-right tabular-nums', parsed?.error && num !== initialNum && 'border-crit')} min={min} max={max}
            disabled={disabled || busy} value={num} title={parsed?.error && num !== initialNum ? parsed.error : undefined}
            aria-label={typeof label === 'string' ? label : undefined} onChange={(e) => setNum(e.target.value)} onBlur={commitNum}
            onKeyDown={(e) => { if (e.key === 'Enter') commitNum(); if (e.key === 'Escape') setNum(initialNum) }} />
          {unit && <span className="w-16 text-xs text-muted">{unit}</span>}
        </>
      )}
      {chip}
    </SettingRow>
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
