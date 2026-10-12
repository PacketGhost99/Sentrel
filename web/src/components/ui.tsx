import { clsx } from 'clsx'
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode, type ButtonHTMLAttributes, type InputHTMLAttributes, type SelectHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import { createPortal } from 'react-dom'
import { AlertTriangle, Check, ChevronDown, Copy, Info, Loader2, X } from 'lucide-react'

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
        'inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap',
        size === 'sm' ? 'h-7 px-2.5 text-xs' : 'h-9 px-3.5 text-sm',
        variant === 'primary' && 'bg-accent text-accent-fg hover:brightness-110',
        variant === 'secondary' && 'bg-panel-2 text-fg border border-line hover:brightness-95 dark:hover:brightness-125',
        variant === 'outline' && 'border border-line text-fg hover:bg-panel-2',
        variant === 'ghost' && 'text-muted hover:text-fg hover:bg-panel-2',
        variant === 'danger' && 'bg-crit text-white hover:brightness-110',
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  )
}

// ---------- Layout bits ----------

export function Card({ title, actions, children, className, bodyClass, subtitle }:
  { title?: ReactNode; actions?: ReactNode; children?: ReactNode; className?: string; bodyClass?: string; subtitle?: ReactNode }) {
  return (
    <section className={cx('rounded-lg border border-line bg-panel', className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5">
          <div className="min-w-0">
            {title && <h3 className="truncate text-sm font-semibold">{title}</h3>}
            {subtitle && <p className="truncate text-xs text-muted">{subtitle}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={cx('p-4', bodyClass)}>{children}</div>
    </section>
  )
}

export function PageHeader({ title, subtitle, actions, back }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; back?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        {back}
        <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <div className="mt-0.5 text-sm text-muted">{subtitle}</div>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function Stat({ label, value, sub, tone, onClick }: { label: string; value: ReactNode; sub?: ReactNode; tone?: 'ok' | 'warn' | 'crit' | 'info' | 'muted'; onClick?: () => void }) {
  return (
    <div onClick={onClick} className={cx('rounded-lg border border-line bg-panel p-4', onClick && 'cursor-pointer hover:border-accent/60')}>
      <div className="text-xs font-medium uppercase tracking-wide text-muted">{label}</div>
      <div className={cx('mt-1 text-2xl font-semibold tabular-nums',
        tone === 'ok' && 'text-ok', tone === 'warn' && 'text-warn', tone === 'crit' && 'text-crit', tone === 'info' && 'text-info', tone === 'muted' && 'text-muted')}>{value}</div>
      {sub && <div className="mt-0.5 text-xs text-muted">{sub}</div>}
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
    <div className="flex flex-col items-center justify-center gap-2 px-4 py-12 text-center">
      {icon && <div className="text-muted">{icon}</div>}
      <div className="font-medium">{title}</div>
      {children && <div className="max-w-md text-sm text-muted">{children}</div>}
    </div>
  )
}

export function ErrorBox({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null
  const msg = error instanceof Error ? error.message : String(error)
  return (
    <div className={cx('flex items-start gap-2 rounded-md border border-crit/40 bg-crit/10 px-3 py-2 text-sm text-crit', className)}>
      <AlertTriangle className="mt-0.5 size-4 shrink-0" />
      <span className="break-words">{msg}</span>
    </div>
  )
}

export function Note({ children, tone = 'info', className }: { children: ReactNode; tone?: 'info' | 'warn' | 'ok'; className?: string }) {
  return (
    <div className={cx('flex items-start gap-2 rounded-md border px-3 py-2 text-sm',
      tone === 'info' && 'border-info/30 bg-info/10', tone === 'warn' && 'border-warn/40 bg-warn/10', tone === 'ok' && 'border-ok/40 bg-ok/10', className)}>
      <Info className={cx('mt-0.5 size-4 shrink-0', tone === 'info' && 'text-info', tone === 'warn' && 'text-warn', tone === 'ok' && 'text-ok')} />
      <div className="min-w-0">{children}</div>
    </div>
  )
}

// ---------- Badges & status ----------

export function Badge({ children, tone = 'muted', className, title }: { children: ReactNode; tone?: 'ok' | 'warn' | 'crit' | 'info' | 'muted' | 'accent' | 'maint'; className?: string; title?: string }) {
  return (
    <span title={title} className={cx('inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium leading-4 whitespace-nowrap',
      tone === 'ok' && 'bg-ok/15 text-ok', tone === 'warn' && 'bg-warn/15 text-warn', tone === 'crit' && 'bg-crit/15 text-crit',
      tone === 'info' && 'bg-info/15 text-info', tone === 'muted' && 'bg-panel-2 text-muted', tone === 'accent' && 'bg-accent/15 text-accent',
      tone === 'maint' && 'bg-maint/15 text-maint', className)}>
      {children}
    </span>
  )
}

export function statusTone(s?: string): 'ok' | 'warn' | 'crit' | 'muted' | 'info' {
  switch (s) {
    case 'up': case 'succeeded': case 'completed': case 'active': case 'operational': case 'running': case 'sent':
      return 'ok'
    case 'warning': case 'pending': case 'queued': case 'scheduled': case 'degraded': case 'skipped':
      return 'warn'
    case 'down': case 'failed': case 'critical': case 'rejected': case 'expired': case 'firing':
      return 'crit'
    default:
      return 'muted'
  }
}

export function StatusDot({ status, pulse }: { status?: string; pulse?: boolean }) {
  const tone = statusTone(status)
  const color = tone === 'ok' ? 'bg-ok' : tone === 'warn' ? 'bg-warn' : tone === 'crit' ? 'bg-crit' : 'bg-muted'
  return (
    <span className="relative inline-flex size-2.5 shrink-0">
      {pulse && tone === 'crit' && <span className={cx('pulse-ring absolute inset-0 rounded-full', color)} />}
      <span className={cx('relative inline-flex size-2.5 rounded-full', color)} />
    </span>
  )
}

export function SeverityBadge({ severity }: { severity?: string }) {
  if (!severity) return null
  return <Badge tone={severity === 'critical' ? 'crit' : severity === 'warning' ? 'warn' : 'muted'}>{severity}</Badge>
}

export function Meter({ value, warn = 80, crit = 90 }: { value?: number | null; warn?: number; crit?: number }) {
  if (value === null || value === undefined) return <span className="text-muted">–</span>
  const color = value >= crit ? 'bg-crit' : value >= warn ? 'bg-warn' : 'bg-ok'
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-16 overflow-hidden rounded-full bg-panel-2">
        <div className={cx('h-full rounded-full', color)} style={{ width: `${Math.min(100, Math.max(0, value))}%` }} />
      </div>
      <span className="w-12 text-right text-xs tabular-nums">{value.toFixed(1)}%</span>
    </div>
  )
}

// ---------- Form controls ----------

export function Field({ label, hint, error, children, className, required }: { label?: ReactNode; hint?: ReactNode; error?: string; children: ReactNode; className?: string; required?: boolean }) {
  return (
    <label className={cx('block', className)}>
      {label && <span className="mb-1 block text-xs font-medium text-muted">{label}{required && <span className="text-crit"> *</span>}</span>}
      {children}
      {hint && !error && <span className="mt-1 block text-xs text-muted">{hint}</span>}
      {error && <span className="mt-1 block text-xs text-crit">{error}</span>}
    </label>
  )
}

const inputCls = 'w-full rounded-md border border-line bg-panel px-2.5 py-1.5 text-sm text-fg placeholder:text-muted/70 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/25 disabled:opacity-60'

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...rest} className={cx(inputCls, 'h-9', className)} />
}

export function Textarea({ className, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...rest} className={cx(inputCls, 'min-h-20', className)} />
}

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <div className="relative">
      <select {...rest} className={cx(inputCls, 'h-9 appearance-none pr-8', className)}>{children}</select>
      <ChevronDown className="pointer-events-none absolute right-2.5 top-2.5 size-4 text-muted" />
    </div>
  )
}

export function Toggle({ checked, onChange, label, hint, disabled }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; hint?: ReactNode; disabled?: boolean }) {
  return (
    <label className={cx('flex items-start gap-3', disabled ? 'opacity-60' : 'cursor-pointer')}>
      <button type="button" role="switch" aria-checked={checked} disabled={disabled} onClick={() => onChange(!checked)}
        className={cx('relative mt-0.5 inline-flex h-5 w-9 shrink-0 rounded-full transition-colors', checked ? 'bg-accent' : 'bg-line')}>
        <span className={cx('absolute top-0.5 size-4 rounded-full bg-white shadow transition-transform', checked ? 'translate-x-4.5' : 'translate-x-0.5')} />
      </button>
      {(label || hint) && (
        <span className="min-w-0">
          {label && <span className="block text-sm">{label}</span>}
          {hint && <span className="block text-xs text-muted">{hint}</span>}
        </span>
      )}
    </label>
  )
}

export function Checkbox({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode }) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="size-4 accent-[var(--accent)]" />
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
        <span key={t} className="inline-flex items-center gap-1 rounded bg-panel-2 px-1.5 py-0.5 text-xs">
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
    <div className={cx('group relative rounded-md border border-line bg-panel-2', className)}>
      <pre className="mono overflow-x-auto whitespace-pre-wrap break-all p-3 pr-12 text-xs leading-relaxed">{code}</pre>
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
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/50 p-4 pt-[8vh] backdrop-blur-[1px]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={cx('w-full rounded-lg border border-line bg-panel shadow-2xl', size === 'sm' && 'max-w-md', size === 'md' && 'max-w-xl', size === 'lg' && 'max-w-3xl', size === 'xl' && 'max-w-5xl')}>
        <div className="flex items-center justify-between border-b border-line px-4 py-3">
          <h2 className="font-semibold">{title}</h2>
          <button onClick={onClose} className="rounded p-1 text-muted hover:bg-panel-2 hover:text-fg" aria-label="Close"><X className="size-4" /></button>
        </div>
        <div className="max-h-[70vh] overflow-y-auto p-4">{children}</div>
        {footer && <div className="flex flex-wrap justify-end gap-2 border-t border-line px-4 py-3">{footer}</div>}
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
            <div key={t.id} className={cx('pointer-events-auto flex items-start gap-2 rounded-md border bg-panel px-3 py-2 text-sm shadow-lg',
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

export function Tabs<T extends string>({ tabs, value, onChange, className }: { tabs: { id: T; label: ReactNode; count?: number; hidden?: boolean }[]; value: T; onChange: (v: T) => void; className?: string }) {
  return (
    <div className={cx('scrollbar-thin -mx-1 flex gap-1 overflow-x-auto border-b border-line px-1', className)}>
      {tabs.filter((t) => !t.hidden).map((t) => (
        <button key={t.id} onClick={() => onChange(t.id)}
          className={cx('-mb-px flex items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2 text-sm transition-colors',
            value === t.id ? 'border-accent font-medium text-fg' : 'border-transparent text-muted hover:text-fg')}>
          {t.label}
          {t.count !== undefined && t.count > 0 && <span className="rounded-full bg-panel-2 px-1.5 text-[11px] text-muted">{t.count}</span>}
        </button>
      ))}
    </div>
  )
}

// ---------- Table ----------

export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cx('scrollbar-thin overflow-x-auto', className)}>
      <table className="w-full border-collapse text-sm">{children}</table>
    </div>
  )
}

export function Th({ children, className, onClick, sorted, title }: { children?: ReactNode; className?: string; onClick?: () => void; sorted?: "asc" | "desc" | null; title?: string }) {
  return (
    <th title={title} onClick={onClick} className={cx('border-b border-line px-3 py-2 text-left text-xs font-medium uppercase tracking-wide text-muted whitespace-nowrap', onClick && 'cursor-pointer select-none hover:text-fg', className)}>
      {children}{sorted === 'asc' ? ' ↑' : sorted === 'desc' ? ' ↓' : ''}
    </th>
  )
}

export function Td({ children, className, colSpan, title }: { children?: ReactNode; className?: string; colSpan?: number; title?: string }) {
  return <td colSpan={colSpan} title={title} className={cx('border-b border-line/60 px-3 py-2 align-middle', className)}>{children}</td>
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

export function KV({ items, className }: { items: [ReactNode, ReactNode][]; className?: string }) {
  return (
    <dl className={cx('grid grid-cols-[minmax(110px,max-content)_1fr] gap-x-4 gap-y-1.5 text-sm', className)}>
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
    <div className="inline-flex rounded-md border border-line bg-panel-2 p-0.5">
      {options.map((o) => (
        <button key={o.id} onClick={() => onChange(o.id)}
          className={cx('rounded px-2.5 py-1 text-xs font-medium', value === o.id ? 'bg-panel text-fg shadow-sm' : 'text-muted hover:text-fg')}>{o.label}</button>
      ))}
    </div>
  )
}
