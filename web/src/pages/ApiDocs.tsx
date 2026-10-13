import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Download } from 'lucide-react'
import { get } from '@/lib/api'
import { Badge, Button, Card, CodeBlock, Input, Loading, PageHeader, cx } from '@/components/ui'

const methodTone: Record<string, string> = { get: 'bg-info/15 text-info', post: 'bg-ok/15 text-ok', put: 'bg-warn/15 text-warn', delete: 'bg-crit/15 text-crit' }

export function ApiDocs() {
  const spec = useQuery({ queryKey: ['openapi'], queryFn: () => get<any>('/openapi.json') })
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<string | null>(null)
  const groups = useMemo(() => {
    const out: Record<string, { path: string; method: string; op: any }[]> = {}
    if (!spec.data) return out
    for (const [path, ops] of Object.entries<any>(spec.data.paths)) {
      for (const [method, op] of Object.entries<any>(ops)) {
        if (q && !`${method} ${path} ${op.summary}`.toLowerCase().includes(q.toLowerCase())) continue
        const tag = op.tags?.[0] || 'other'
        ;(out[tag] ||= []).push({ path, method, op })
      }
    }
    return out
  }, [spec.data, q])
  if (spec.isLoading) return <Loading />
  const schemaFor = (ref?: any) => {
    if (!ref) return null
    const name = ref.$ref?.split('/').pop() || (ref.items?.$ref ? ref.items.$ref.split('/').pop() + '[]' : null)
    const s = ref.$ref ? spec.data.components.schemas[name] : ref.items?.$ref ? spec.data.components.schemas[ref.items.$ref.split('/').pop()] : ref
    return { name, props: s?.properties ? Object.keys(s.properties) : [] }
  }
  return (
    <div>
      <PageHeader title="API reference" info="Generated from the server's route table: every action in the UI is available here."
        actions={<a href="/api/v1/openapi.json" target="_blank" rel="noreferrer"><Button icon={<Download className="size-4" />}>OpenAPI JSON</Button></a>} />
      <Card className="mb-4">
        <div className="space-y-2 text-sm">
          <p>Authenticate with an API key (Administration → API keys):</p>
          <CodeBlock code={`curl -H "Authorization: Bearer sentrel_xxxxxxxx_…" ${location.origin}/api/v1/hosts`} />
          <p className="text-muted">Browser sessions use a cookie plus an <code className="mono">X-CSRF-Token</code> header on writes. Responses are JSON; errors look like <code className="mono">{'{"code":"bad_request","error":"…"}'}</code>.</p>
        </div>
      </Card>
      <Input placeholder="Filter endpoints" value={q} onChange={(e) => setQ(e.target.value)} className="mb-4 max-w-sm" />
      <div className="space-y-4">
        {Object.entries(groups).sort().map(([tag, ops]) => (
          <Card key={tag} title={tag} bodyClass="p-0">
            <ul className="divide-y divide-line/60">
              {ops.map(({ path, method, op }) => {
                const id = method + path
                const req = schemaFor(op.requestBody?.content?.['application/json']?.schema)
                const resp = schemaFor(op.responses?.['200']?.content?.['application/json']?.schema)
                return (
                  <li key={id}>
                    <button className="flex w-full items-center gap-3 px-4 py-2 text-left hover:bg-panel-2/60" onClick={() => setOpen(open === id ? null : id)}>
                      <span className={cx('w-16 rounded px-1.5 py-0.5 text-center text-[11px] font-semibold uppercase', methodTone[method])}>{method}</span>
                      <span className="mono text-xs">{path}</span>
                      <span className="ml-auto truncate text-xs text-muted">{op.summary}</span>
                    </button>
                    {open === id && (
                      <div className="space-y-2 bg-panel-2/40 px-4 py-3 text-xs">
                        <p>{op.summary}{op.description && <span className="text-muted"> · {op.description}</span>}{op.security?.length === 0 && <Badge className="ml-2">public</Badge>}</p>
                        {op.parameters?.length > 0 && <p><b>Parameters:</b> {op.parameters.map((p: any) => `${p.name} (${p.in})`).join(', ')}</p>}
                        {req && <p><b>Body:</b> <span className="mono">{req.name}</span> {req.props.length > 0 && <span className="text-muted">{'{ '}{req.props.join(', ')}{' }'}</span>}</p>}
                        {resp && <p><b>Response:</b> <span className="mono">{resp.name}</span> {resp.props.length > 0 && <span className="text-muted">{'{ '}{resp.props.slice(0, 30).join(', ')}{resp.props.length > 30 ? ', …' : ''}{' }'}</span>}</p>}
                      </div>
                    )}
                  </li>
                )
              })}
            </ul>
          </Card>
        ))}
      </div>
    </div>
  )
}
