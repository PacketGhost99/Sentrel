import type { ReactNode } from 'react'
import { parseNotes, type Inline } from '@/lib/updates'

function inline(parts: Inline[]): ReactNode[] {
  return parts.map((p, i) => {
    switch (p.t) {
      case 'b': return <strong key={i} className="font-semibold text-fg">{p.text}</strong>
      case 'code': return <code key={i} className="mono rounded bg-panel-2 px-1 py-px text-[12px]">{p.text}</code>
      case 'link': return <a key={i} href={p.href} target="_blank" rel="noopener noreferrer" className="break-words text-accent hover:underline">{p.text}</a>
      default: return <span key={i}>{p.text}</span>
    }
  })
}

/** Renders GitHub release notes without ever interpreting them as HTML. */
export function ReleaseNotes({ text }: { text: string }) {
  const blocks = parseNotes(text)
  if (!blocks.length) return <p className="text-sm text-muted">This release has no notes.</p>
  return (
    <div className="space-y-2.5 text-sm leading-relaxed text-fg/90">
      {blocks.map((b, i) => {
        switch (b.t) {
          case 'h': return <p key={i} className={b.level === 1 ? 'pt-1 text-base font-semibold' : 'pt-1 font-semibold'}>{inline(b.inline)}</p>
          case 'ul': return <ul key={i} className="list-disc space-y-1 pl-5 marker:text-muted">{b.items.map((it, j) => <li key={j}>{inline(it)}</li>)}</ul>
          case 'ol': return <ol key={i} className="list-decimal space-y-1 pl-5 marker:text-muted">{b.items.map((it, j) => <li key={j}>{inline(it)}</li>)}</ol>
          default: return <p key={i}>{inline(b.inline)}</p>
        }
      })}
    </div>
  )
}
