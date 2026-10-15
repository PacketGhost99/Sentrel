import { clsx } from 'clsx'
import { Component, createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode, type ButtonHTMLAttributes, type InputHTMLAttributes, type SelectHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import { createPortal } from 'react-dom'
import { AlertTriangle, BellOff, Check, CheckCircle2, ChevronDown, Copy, Info, Loader2, X } from 'lucide-react'

export const cx = clsx

// ---------- Button ----------

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'outline'
export function Button({ variant = 'secondary', size = 'md', loading, icon, className, children, ...rest }:
  ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: 'sm' | 'md'; loading?: boolean; icon?: ReactNode }) {
  return (
    <button
      {...rest}
      disabled={rest.disabled || loading}
      className={cx(
        'inline-flex items-center justify-center gap-1.5 rounded-md font-medium whitespace-nowrap select-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent active:scale-[0.98] disabled:pointer-events-none disabled:opacity-50',
        size === 'sm' ? 'h-8 px-3 text-xs' : 'h-9 px-4 text-sm',
        variant === 'primary' && 'bg-accent text-accent-fg shadow-sm hover:brightness-110',
        variant === 'secondary' && 'border border-line bg-panel text-fg shadow-sm hover:bg-panel-2',
        variant === 'outline' && 'border border-line text-fg hover:bg-panel-2',
        variant === 'ghost' && 'text-muted hover:bg-panel-2 hover:text-fg',
        variant === 'danger' && 'bg-crit text-white shadow-sm hover:brightness-110',
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  )
}

// ---------- Layout bits ----------

export function Card({ title, actions, children, className, bodyClass, subtitle, info }:
  { title?: ReactNode; actions?: ReactNode; children?: ReactNode; className?: string; bodyClass?: string; subtitle?: ReactNode; info?: ReactNode }) {
  return (
    <section className={cx('console-panel overflow-hidden rounded-md border bg-panel', !className?.includes('border-') && 'border-line/70', className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-3 border-b border-line/70 bg-panel-2/40 px-4 py-3">
          <div className="min-w-0">
            {title && <h3 className="flex min-w-0 items-center gap-1.5 text-sm font-semibold tracking-tight"><span className="truncate">{title}</span>{info && <InfoTip>{info}</InfoTip>}</h3>}
            {subtitle && <p className="mt-0.5 text-xs leading-relaxed text-muted">{subtitle}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={bodyClass ?? 'p-5'}>{children}</div>
    </section>
  )
}

export function PageHeader({ title, subtitle, actions, back, info }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; back?: ReactNode; info?: ReactNode }) {
  return (
    <div className="console-page-header mb-5 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        {back}
        <h1 className="flex min-w-0 items-center gap-2 text-[22px] font-semibold tracking-tight"><span className="truncate">{title}</span>{info && <InfoTip size="md">{info}</InfoTip>}</h1>
        {subtitle && <div className="mt-1 text-sm text-muted">{subtitle}</div>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function Stat({ label, value, sub, tone, onClick, icon, title, info }: { label: string; value: ReactNode; sub?: ReactNode; tone?: 'ok' | 'warn' | 'crit' | 'info' | 'muted' | 'maint'; onClick?: () => void; icon?: ReactNode; title?: string; info?: ReactNode }) {
  const Container = onClick ? 'button' : 'div'
  return (
    <Container type={onClick ? 'button' : undefined} onClick={onClick} title={title} className={cx('console-stat flex h-full w-full flex-col rounded-md border border-line bg-panel p-4 text-left transition-[border-color] duration-150', onClick && 'cursor-pointer hover:border-accent/40')}>
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1 text-xs font-medium text-muted"><span className="truncate">{label}</span>{info && <InfoTip>{info}</InfoTip>}</div>
        {icon && <div className={cx('[&_svg]:size-4', tone === 'ok' && 'text-ok', tone === 'warn' && 'text-warn', tone === 'crit' && 'text-crit', tone === 'info' && 'text-info', tone === 'maint' && 'text-maint', (!tone || tone === 'muted') && 'text-muted')}>{icon}</div>}
      </div>
      <div className={cx('mt-1.5 text-3xl font-semibold tabular-nums tracking-tight',
        tone === 'ok' && 'text-ok-ink', tone === 'warn' && 'text-warn-ink', tone === 'crit' && 'text-crit-ink', tone === 'info' && 'text-info-ink', tone === 'maint' && 'text-maint-ink', tone === 'muted' && 'text-muted')}>{value}</div>
      {sub && <div className="mt-0.5 text-xs leading-relaxed text-muted" title={typeof sub === 'string' ? sub : undefined}>{sub}</div>}
    </Container>
  )
}

/**
 * A small (i) button that shows an explanation on hover, focus or click.
 * Use it for background information that does not need to be on screen all
 * the time. The popover is portalled so a card never clips it.
 */
export function InfoTip({ children, size = 'sm', className, label = 'More information' }: { children: ReactNode; size?: 'sm' | 'md'; className?: string; label?: string }) {
  const [hover, setHover] = useState(false)
  const [pinned, setPinned] = useState(false)
  const [pos, setPos] = useState<{ left: number; top: number; above: boolean } | null>(null)
  const btn = useRef<HTMLButtonElement>(null)
  const show = hover || pinned
  useEffect(() => {
    if (!show || !btn.current) return
    const place = () => {
      if (!btn.current) return
      const r = btn.current.getBoundingClientRect()
      const w = 288
      const left = Math.max(8, Math.min(window.innerWidth - w - 8, r.left + r.width / 2 - w / 2))
      const above = r.bottom + 160 > window.innerHeight && r.top > 160
      setPos({ left, top: above ? r.top - 6 : r.bottom + 6, above })
    }
    place()
    window.addEventListener('scroll', place, true)
    window.addEventListener('resize', place)
    return () => {
      window.removeEventListener('scroll', place, true)
      window.removeEventListener('resize', place)
    }
  }, [show])
  useEffect(() => {
    if (!pinned) return
    const close = (e: Event) => {
      if (e.type === 'keydown' ? (e as KeyboardEvent).key === 'Escape' : !btn.current?.contains(e.target as Node)) setPinned(false)
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', close)
    }
  }, [pinned])
  return (
    <>
      <button ref={btn} type="button" aria-label={label} aria-expanded={show}
        onMouseEnter={() => setHover(true)} onMouseLeave={() => setHover(false)} onFocus={() => setHover(true)} onBlur={() => setHover(false)}
        onClick={(e) => { e.stopPropagation(); e.preventDefault(); setPinned((p) => !p) }}
        className={cx('inline-flex shrink-0 items-center justify-center rounded-full font-normal text-muted/70 hover:text-fg focus-visible:outline-2 focus-visible:outline-accent', className)}>
        <Info className={size === 'md' ? 'size-4' : 'size-3.5'} />
      </button>
      {show && pos && createPortal(
        <div role="tooltip" style={{ left: pos.left, top: pos.top, width: 288 }}
          className={cx('tip-in pointer-events-none fixed z-[60] rounded-lg border border-line bg-panel px-3 py-2 text-left text-xs font-normal normal-case leading-relaxed tracking-normal text-fg shadow-pop', pos.above && '-translate-y-full')}>
          {children}
        </div>,
        document.body,
      )}
    </>
  )
}

/** Right-hand slide-over panel for secondary content such as settings. */
export function Drawer({ open, onClose, title, subtitle, children, footer }: { open: boolean; onClose: () => void; title: ReactNode; subtitle?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  useEffect(() => {
    if (!open) return
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', h)
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      window.removeEventListener('keydown', h)
      document.body.style.overflow = prev
    }
  }, [open, onClose])
  if (!open) return null
  return createPortal(
    <div className="tip-in fixed inset-0 z-50 flex justify-end bg-slate-950/35 backdrop-blur-[2px]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside role="dialog" aria-modal="true" className="drawer-in flex h-full w-full max-w-lg flex-col border-l border-line/70 bg-panel shadow-pop">
        <div className="flex items-start justify-between gap-3 border-b border-line/70 px-5 py-4">
          <div className="min-w-0">
            <h2 className="text-base font-semibold tracking-tight">{title}</h2>
            {subtitle && <p className="mt-0.5 text-xs text-muted">{subtitle}</p>}
          </div>
          <button onClick={onClose} className="rounded-lg p-1.5 text-muted hover:bg-panel-2 hover:text-fg" aria-label="Close"><X className="size-4" /></button>
        </div>
        <div className="flex-1 overflow-y-auto px-5 py-2">{children}</div>
        {footer && <div className="flex flex-wrap justify-end gap-2 border-t border-line/70 px-5 py-3.5">{footer}</div>}
      </aside>
    </div>,
    document.body,
  )
}

/** Heading that groups setting rows inside a Drawer or Card. */
export function SettingsGroup({ title, info, children, className }: { title: ReactNode; info?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cx('py-3', className)}>
      <h3 className="mb-1 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted">{title}{info && <InfoTip>{info}</InfoTip>}</h3>
      <div className="divide-y divide-line/60">{children}</div>
    </section>
  )
}

/** One compact setting: label (with optional tooltip) on the left, control on the right. */
export function SettingRow({ label, info, children, className }: { label: ReactNode; info?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={cx('flex min-h-12 flex-wrap items-center justify-between gap-x-4 gap-y-2 py-2', className)}>
      <div className="flex min-w-0 items-center gap-1.5 text-sm">{label}{info && <InfoTip>{info}</InfoTip>}</div>
      <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">{children}</div>
    </div>
  )
}

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cx('size-5 animate-spin text-muted', className)} />
}

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted"><Spinner />{label}</div>
}

export function Empty({ title, children, icon }: { title: string; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-4 py-8 text-center">
      {icon && <div className="text-muted">{icon}</div>}
      <div className="font-medium">{title}</div>
      {children && <div className="max-w-md text-sm text-muted">{children}</div>}
    </div>
  )
}

/** Empty state with a green check, for "nothing to report" views. */
export function AllClear({ title, children }: { title: string; children?: ReactNode }) {
  return <Empty title={title} icon={<CheckCircle2 className="size-9 text-ok" />}>{children}</Empty>
}

export function ErrorBox({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null
  const msg = error instanceof Error ? error.message : String(error)
  return (
    <div className={cx('flex items-start gap-2 rounded-xl border border-crit/30 bg-crit/10 px-3.5 py-2.5 text-sm text-crit-ink', className)}>
      <AlertTriangle className="mt-0.5 size-4 shrink-0" />
      <span className="break-words">{msg}</span>
    </div>
  )
}

export function Note({ children, tone = 'info', className }: { children: ReactNode; tone?: 'info' | 'warn' | 'ok'; className?: string }) {
  return (
    <div className={cx('flex items-start gap-2.5 rounded-xl border px-3.5 py-2.5 text-sm',
      tone === 'info' && 'border-info/20 bg-info/8', tone === 'warn' && 'border-warn/30 bg-warn/10', tone === 'ok' && 'border-ok/25 bg-ok/10', className)}>
      <Info className={cx('mt-0.5 size-4 shrink-0', tone === 'info' && 'text-info', tone === 'warn' && 'text-warn', tone === 'ok' && 'text-ok')} />
      <div className="min-w-0">{children}</div>
    </div>
  )
}

// ---------- Badges & status ----------

export type BadgeTone = 'ok' | 'warn' | 'crit' | 'info' | 'muted' | 'accent' | 'maint'

export function Badge({ children, tone = 'muted', className, title }: { children: ReactNode; tone?: BadgeTone; className?: string; title?: string }) {
  return (
    <span title={title} className={cx('inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium leading-4 whitespace-nowrap',
      tone === 'ok' && 'bg-ok/12 text-ok-ink', tone === 'warn' && 'bg-warn/14 text-warn-ink', tone === 'crit' && 'bg-crit/12 text-crit-ink',
      tone === 'info' && 'bg-info/12 text-info-ink', tone === 'muted' && 'bg-panel-2 text-muted', tone === 'accent' && 'bg-accent/12 text-accent',
      tone === 'maint' && 'bg-maint/12 text-maint-ink', className)}>
      {children}
    </span>
  )
}

export function statusTone(s?: string): 'ok' | 'warn' | 'crit' | 'muted' | 'info' | 'maint' {
  switch (s) {
    case 'up': case 'succeeded': case 'completed': case 'active': case 'operational': case 'running': case 'sent':
      return 'ok'
    case 'warning': case 'pending': case 'awaiting_signature': case 'queued': case 'scheduled': case 'degraded': case 'skipped':
      return 'warn'
    case 'down': case 'failed': case 'critical': case 'rejected': case 'expired': case 'firing':
      return 'crit'
    case 'maintenance':
      return 'maint'
    default:
      return 'muted'
  }
}

export function StatusDot({ status, pulse }: { status?: string; pulse?: boolean }) {
  const tone = statusTone(status)
  const color = tone === 'ok' ? 'bg-ok-fill' : tone === 'warn' ? 'bg-warn-fill' : tone === 'crit' ? 'bg-crit-fill' : tone === 'maint' ? 'bg-maint' : 'bg-unknown-fill'
  return (
    <span className="relative inline-flex size-2.5 shrink-0">
      {pulse && tone === 'crit' && <span className={cx('pulse-ring absolute inset-0 rounded-full', color)} />}
      <span className={cx('relative inline-flex size-2.5 rounded-full', color)} />
    </span>
  )
}

/** Pill with a status dot and label, in the style of an uptime monitor. */
export function StatusPill({ label, status, tone, className }: { label: ReactNode; status?: string; tone?: BadgeTone; className?: string }) {
  const t: BadgeTone = tone ?? statusTone(status)
  return (
    <span className={cx('inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium whitespace-nowrap',
      t === 'ok' && 'bg-ok/12 text-ok-ink', t === 'warn' && 'bg-warn/14 text-warn-ink', t === 'crit' && 'bg-crit/12 text-crit-ink',
      t === 'maint' && 'bg-maint/12 text-maint-ink', t === 'info' && 'bg-info/12 text-info-ink', t === 'accent' && 'bg-accent/12 text-accent',
      t === 'muted' && 'bg-panel-2 text-muted', className)}>
      <span className={cx('size-1.5 rounded-full',
        t === 'ok' && 'bg-ok-fill', t === 'warn' && 'bg-warn-fill', t === 'crit' && 'bg-crit-fill', t === 'maint' && 'bg-maint',
        t === 'info' && 'bg-info', t === 'accent' && 'bg-accent', t === 'muted' && 'bg-unknown-fill')} />
      {label}
    </span>
  )
}

/** Uptime percentage pill. Shows an en dash when there is no data. */
export function UptimePill({ value, title, className }: { value?: number | null; title?: string; className?: string }) {
  const none = value === null || value === undefined || !isFinite(value)
  const v = none ? 0 : (value as number)
  const tone: BadgeTone = none ? 'muted' : v >= 99.5 ? 'ok' : v >= 95 ? 'warn' : 'crit'
  return (
    <Badge tone={tone} title={title} className={cx('min-w-14 justify-center px-2.5 py-1 text-xs font-semibold tabular-nums', className)}>
      {none ? '–' : `${v >= 99.995 ? '100' : v.toFixed(v >= 99 ? 2 : 1)}%`}
    </Badge>
  )
}

/** Marks an alert whose rule has notifications turned off. */
export function MutedBadge({ className }: { className?: string }) {
  return (
    <span title="Notifications are disabled for this rule: no emails or webhooks are sent" className={cx('inline-flex items-center gap-1 rounded-full bg-panel-2 px-2 py-0.5 text-[11px] font-medium leading-4 text-muted', className)}>
      <BellOff className="size-3" />muted
    </span>
  )
}

export function SeverityBadge({ severity }: { severity?: string }) {
  if (!severity) return null
  return <Badge tone={severity === 'critical' ? 'crit' : severity === 'warning' ? 'warn' : 'muted'}>{severity}</Badge>
}

export function Meter({ value, warn = 80, crit = 90, className, showValue = true }: { value?: number | null; warn?: number; crit?: number; className?: string; showValue?: boolean }) {
  if (value === null || value === undefined) return <span className="text-muted">–</span>
  const color = value >= crit ? 'bg-crit-fill' : value >= warn ? 'bg-warn-fill' : 'bg-ok-fill'
  return (
    <div className="flex items-center gap-2">
      <div className={cx('h-1.5 overflow-hidden rounded-full bg-panel-2', className ?? 'w-16')}>
        <div className={cx('h-full rounded-full transition-[width] duration-500', color)} style={{ width: `${Math.min(100, Math.max(0, value))}%` }} />
      </div>
      {showValue && <span className="w-12 text-right text-xs tabular-nums">{value.toFixed(1)}%</span>}
    </div>
  )
}

// ---------- Form controls ----------

export function Field({ label, hint, error, children, className, required, info }: { label?: ReactNode; hint?: ReactNode; error?: string; children: ReactNode; className?: string; required?: boolean; info?: ReactNode }) {
  return (
    <label className={cx('block', className)}>
      {label && <span className="mb-1.5 flex items-center gap-1 text-xs font-medium text-muted">{label}{required && <span className="text-crit"> *</span>}{info && <InfoTip>{info}</InfoTip>}</span>}
      {children}
      {hint && !error && <span className="mt-1 block text-xs text-muted">{hint}</span>}
      {error && <span className="mt-1 block text-xs text-crit-ink">{error}</span>}
    </label>
  )
}

const inputCls = 'w-full rounded-md border border-line bg-panel px-3 py-1.5 text-sm text-fg shadow-sm placeholder:text-muted/70 hover:border-muted/40 focus:border-accent focus:outline-none focus:ring-3 focus:ring-accent/20 disabled:opacity-60'

/** inputCls, minus its default width and height when the caller sets their own. */
function fieldCls(className: string | undefined, height: string) {
  const own = className?.split(/\s+/) ?? []
  const base = own.some((c) => /^(sm:|md:|lg:)?w-/.test(c)) ? inputCls.replace(/(^| )w-full( |$)/, ' ') : inputCls
  return cx(base, !own.some((c) => /^h-/.test(c)) && height, className)
}

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...rest} className={fieldCls(className, 'h-9')} />
}

export function Textarea({ className, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...rest} className={cx(inputCls, 'min-h-20', className)} />
}

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <div className="relative">
      <select {...rest} className={cx(fieldCls(className, 'h-9'), 'appearance-none pr-8')}>{children}</select>
      <ChevronDown className="pointer-events-none absolute right-2.5 top-1/2 size-4 -translate-y-1/2 text-muted" />
    </div>
  )
}

export function Toggle({ checked, onChange, label, hint, disabled, info }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; hint?: ReactNode; disabled?: boolean; info?: ReactNode }) {
  return (
    <label className={cx('flex items-start gap-3', disabled ? 'opacity-60' : 'cursor-pointer')}>
      <button type="button" role="switch" aria-checked={checked} disabled={disabled} onClick={() => onChange(!checked)}
        className={cx('relative mt-0.5 inline-flex h-5 w-9 shrink-0 rounded-full transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent', checked ? 'bg-accent' : 'bg-unknown-fill')}>
        <span className={cx('absolute top-0.5 size-4 rounded-full bg-white shadow-sm transition-transform', checked ? 'translate-x-[18px]' : 'translate-x-0.5')} />
      </button>
      {(label || hint) && (
        <span className="min-w-0">
          {label && <span className="flex items-center gap-1.5 text-sm">{label}{info && <InfoTip>{info}</InfoTip>}</span>}
          {hint && <span className="block text-xs text-muted">{hint}</span>}
        </span>
      )}
    </label>
  )
}

export function Checkbox({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode }) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="size-4 rounded accent-[var(--accent)]" />
      {label}
    </label>
  )
}

export function TagInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [draft, setDraft] = useState('')
  const add = () => {
    const t = draft.trim().replace(/,$/, '')
    if (t && !value.includes(t)) onChange([...value, t])
    setDraft('')
  }
  return (
    <div className={cx(inputCls, 'flex min-h-9 flex-wrap items-center gap-1 py-1')}>
      {value.map((t) => (
        <span key={t} className="inline-flex items-center gap-1 rounded-full bg-panel-2 px-2 py-0.5 text-xs">
          {t}
          <button type="button" onClick={() => onChange(value.filter((x) => x !== t))} className="text-muted hover:text-fg"><X className="size-3" /></button>
        </span>
      ))}
      <input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={value.length ? '' : placeholder}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add() } else if (e.key === 'Backspace' && !draft && value.length) onChange(value.slice(0, -1)) }}
        onBlur={add} className="min-w-24 flex-1 bg-transparent text-sm outline-none" />
    </div>
  )
}

// ---------- Copy ----------

export function CopyButton({ text, label }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button size="sm" variant="ghost" onClick={async () => {
      try { await navigator.clipboard.writeText(text) } catch { fallbackCopy(text) }
      setDone(true)
      setTimeout(() => setDone(false), 1500)
    }} icon={done ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}>{label}</Button>
  )
}

function fallbackCopy(text: string) {
  const ta = document.createElement('textarea')
  ta.value = text
  document.body.appendChild(ta)
  ta.select()
  document.execCommand('copy')
  ta.remove()
}

export function CodeBlock({ code, className }: { code: string; className?: string }) {
  return (
    <div className={cx('group relative rounded-xl border border-line/70 bg-panel-2', className)}>
      <pre className="mono overflow-x-auto whitespace-pre-wrap break-all p-3.5 pr-12 text-xs leading-relaxed">{code}</pre>
      <div className="absolute right-1.5 top-1.5"><CopyButton text={code} /></div>
    </div>
  )
}

// ---------- Modal ----------

export function Modal({ open, onClose, title, children, footer, size = 'md' }: { open: boolean; onClose: () => void; title: ReactNode; children: ReactNode; footer?: ReactNode; size?: 'sm' | 'md' | 'lg' | 'xl' }) {
  useEffect(() => {
    if (!open) return
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [open, onClose])
  if (!open) return null
  return createPortal(
    <div className="fade-in fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-slate-950/45 p-4 pt-[8vh] backdrop-blur-sm" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={cx('pop-in w-full rounded-2xl border border-line/70 bg-panel shadow-pop', size === 'sm' && 'max-w-md', size === 'md' && 'max-w-xl', size === 'lg' && 'max-w-3xl', size === 'xl' && 'max-w-5xl')}>
        <div className="flex items-center justify-between border-b border-line/70 bg-panel-2/40 px-4 py-3">
          <h2 className="text-base font-semibold tracking-tight">{title}</h2>
          <button onClick={onClose} className="rounded-lg p-1.5 text-muted hover:bg-panel-2 hover:text-fg" aria-label="Close"><X className="size-4" /></button>
        </div>
        <div className="max-h-[70vh] overflow-y-auto p-5">{children}</div>
        {footer && <div className="flex flex-wrap justify-end gap-2 border-t border-line/70 px-5 py-3.5">{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}

// ---------- Confirm ----------

interface ConfirmOpts {
  title: string
  body: ReactNode
  confirmLabel?: string
  danger?: boolean
  typeToConfirm?: string
}

const ConfirmCtx = createContext<(o: ConfirmOpts) => Promise<boolean>>(async () => false)
export const useConfirm = () => useContext(ConfirmCtx)

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<(ConfirmOpts & { resolve: (v: boolean) => void }) | null>(null)
  const [typed, setTyped] = useState('')
  const confirm = useCallback((o: ConfirmOpts) => new Promise<boolean>((resolve) => { setTyped(''); setState({ ...o, resolve }) }), [])
  const close = (v: boolean) => { state?.resolve(v); setState(null) }
  return (
    <ConfirmCtx.Provider value={confirm}>
      {children}
      <Modal open={!!state} onClose={() => close(false)} title={state?.title} size="sm"
        footer={<>
          <Button onClick={() => close(false)}>Cancel</Button>
          <Button variant={state?.danger ? 'danger' : 'primary'} disabled={!!state?.typeToConfirm && typed !== state.typeToConfirm} onClick={() => close(true)}>{state?.confirmLabel || 'Confirm'}</Button>
        </>}>
        <div className="space-y-3 text-sm">
          {state?.body}
          {state?.typeToConfirm && (
            <Field label={<>Type <b className="mono text-fg">{state.typeToConfirm}</b> to confirm</>}>
              <Input autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} />
            </Field>
          )}
        </div>
      </Modal>
    </ConfirmCtx.Provider>
  )
}

// ---------- Toasts ----------

interface Toast { id: number; message: ReactNode; tone: 'ok' | 'crit' | 'info' }
const ToastCtx = createContext<(m: ReactNode, tone?: Toast['tone']) => void>(() => {})
export const useToast = () => useContext(ToastCtx)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const next = useRef(1)
  const push = useCallback((message: ReactNode, tone: Toast['tone'] = 'ok') => {
    const id = next.current++
    setToasts((t) => [...t, { id, message, tone }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), tone === 'crit' ? 7000 : 3500)
  }, [])
  return (
    <ToastCtx.Provider value={push}>
      {children}
      {createPortal(
        <div className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2">
          {toasts.map((t) => (
            <div key={t.id} className={cx('pop-in pointer-events-auto flex items-start gap-2.5 rounded-xl border bg-panel px-3.5 py-2.5 text-sm shadow-pop',
              t.tone === 'ok' && 'border-ok/50', t.tone === 'crit' && 'border-crit/60', t.tone === 'info' && 'border-info/50')}>
              {t.tone === 'crit' ? <AlertTriangle className="mt-0.5 size-4 shrink-0 text-crit" /> : <Check className={cx('mt-0.5 size-4 shrink-0', t.tone === 'ok' ? 'text-ok' : 'text-info')} />}
              <div className="min-w-0 break-words">{t.message}</div>
            </div>
          ))}
        </div>,
        document.body,
      )}
    </ToastCtx.Provider>
  )
}

// ---------- Tabs ----------

export function Tabs<T extends string>({ tabs, value, onChange, className, wrap = false }: { tabs: { id: T; label: ReactNode; count?: number; hidden?: boolean; countTitle?: string }[]; value: T; onChange: (v: T) => void; className?: string; wrap?: boolean }) {
  return (
    <div role="tablist" className={cx('scrollbar-thin flex gap-1', wrap ? 'flex-wrap' : '-mx-1 overflow-x-auto border-b border-line px-1', className)}>
      {tabs.filter((t) => !t.hidden).map((t) => (
        <button key={t.id} type="button" role="tab" aria-selected={value === t.id} onClick={() => onChange(t.id)}
          className={cx('flex items-center gap-1.5 whitespace-nowrap px-3.5 py-2.5 text-sm', wrap ? 'rounded-lg' : '-mb-px rounded-t-lg border-b-2',
            wrap && value === t.id && 'bg-accent/10',
            value === t.id ? 'border-accent font-semibold text-fg' : 'border-transparent text-muted hover:bg-panel-2/70 hover:text-fg')}>
          {t.label}
          {t.count !== undefined && t.count > 0 && <span title={t.countTitle} className={cx('rounded-full px-1.5 text-[11px] font-medium', value === t.id ? 'bg-accent/12 text-accent' : 'bg-panel-2 text-muted')}>{t.count.toLocaleString()}</span>}
        </button>
      ))}
    </div>
  )
}

// ---------- Table ----------

export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cx('scrollbar-thin overflow-x-auto', className)}>
      <table className="w-full border-collapse text-sm [&_tbody_tr]:transition-colors [&_tbody_tr:hover]:bg-panel-2/50 [&_tbody_tr:last-child_td]:border-b-0">{children}</table>
    </div>
  )
}

export function Th({ children, className, onClick, sorted, title }: { children?: ReactNode; className?: string; onClick?: () => void; sorted?: "asc" | "desc" | null; title?: string }) {
  return (
    <th title={title} onClick={onClick} className={cx('border-b border-line/70 bg-panel-2/40 px-4 py-2.5 text-left text-[11px] font-semibold uppercase tracking-wider text-muted whitespace-nowrap', onClick && 'cursor-pointer select-none hover:text-fg', className)}>
      {children}{sorted === 'asc' ? ' ↑' : sorted === 'desc' ? ' ↓' : ''}
    </th>
  )
}

export function Td({ children, className, colSpan, title }: { children?: ReactNode; className?: string; colSpan?: number; title?: string }) {
  return <td colSpan={colSpan} title={title} className={cx('border-b border-line/50 px-4 py-3 align-middle', className)}>{children}</td>
}

export function useSort<T>(rows: T[], initial: { key: string; dir: 'asc' | 'desc' }, getters: Record<string, (r: T) => unknown>) {
  const [sort, setSort] = useState(initial)
  const sorted = [...rows].sort((a, b) => {
    const g = getters[sort.key]
    if (!g) return 0
    const va = g(a) as any
    const vb = g(b) as any
    const r = typeof va === 'number' && typeof vb === 'number' ? va - vb : String(va ?? '').localeCompare(String(vb ?? ''), undefined, { numeric: true })
    return sort.dir === 'asc' ? r : -r
  })
  const th = (key: string) => ({
    onClick: () => setSort((s) => ({ key, dir: s.key === key && s.dir === 'asc' ? 'desc' : 'asc' })),
    sorted: sort.key === key ? sort.dir : null,
  })
  return { sorted, th }
}

// ---------- Misc ----------

export function KV({ items, className, stacked }: { items: ([ReactNode, ReactNode] | [ReactNode, ReactNode, 'wide'])[]; className?: string; stacked?: boolean }) {
  if (stacked) {
    // Label above value, two per row; items marked 'wide' take the full row.
    return (
      <dl className={cx('grid grid-cols-2 gap-x-4 gap-y-3 text-sm', className)}>
        {items.map(([k, v, w], i) => (
          <div key={i} className={cx('min-w-0', w === 'wide' && 'col-span-2')}>
            <dt className="text-xs font-medium text-muted">{k}</dt>
            <dd className="mt-0.5 min-w-0 break-words">{v ?? '–'}</dd>
          </div>
        ))}
      </dl>
    )
  }
  return (
    <dl className={cx('grid grid-cols-[minmax(110px,max-content)_1fr] gap-x-4 gap-y-2 text-sm', className)}>
      {items.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="min-w-0 break-words">{v ?? '–'}</dd>
        </div>
      ))}
    </dl>
  )
}

export function Segmented<T extends string>({ options, value, onChange }: { options: { id: T; label: ReactNode }[]; value: T; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex rounded-lg bg-panel-2 p-0.5">
      {options.map((o) => (
        <button key={o.id} onClick={() => onChange(o.id)}
          className={cx('rounded-md px-3 py-1 text-xs font-medium', value === o.id ? 'bg-panel text-fg shadow-card' : 'text-muted hover:text-fg')}>{o.label}</button>
      ))}
    </div>
  )
}

// ---------- Error boundary ----------

// PageBoundary keeps the shell usable when one page throws while rendering.
export class PageBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null }
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error) { console.error(error) }
  render() {
    if (!this.state.error) return this.props.children
    return (
      <div className="mx-auto max-w-xl py-16">
        <ErrorBox error={new Error(`This page failed to render: ${this.state.error.message}`)} />
        <Button className="mt-4" onClick={() => location.reload()}>Reload</Button>
      </div>
    )
  }
}
