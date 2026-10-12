import { useEffect, useState } from 'react'
import type { QueryClient } from '@tanstack/react-query'

interface ServerEvent {
  type: string
  tenant_id: number
  host_id?: number
  id?: string
}

// Map an event to the query keys it makes stale.
function keysFor(e: ServerEvent): unknown[][] {
  const keys: unknown[][] = []
  const host = e.host_id ? [['host', String(e.host_id)]] : []
  switch (true) {
    case e.type.startsWith('host.'):
      keys.push(['hosts'], ['dashboard'], ...host)
      if (e.type === 'host.inventory') keys.push(['packages', String(e.host_id)])
      break
    case e.type === 'alert.changed':
      keys.push(['alerts'], ['dashboard'], ['hosts'], ['alert-events'])
      if (e.id) keys.push(['alert', e.id])
      break
    case e.type === 'check.changed':
      keys.push(['checks'], ['dashboard'], ...host)
      break
    case e.type.startsWith('job.'):
      keys.push(['jobs'])
      if (e.id) keys.push(['job', e.id])
      break
    case e.type === 'vuln.scanned':
      keys.push(['vulns'], ...host)
      break
    case e.type === 'push.updated':
      keys.push(['push'])
      if (e.id) keys.push(['push', e.id])
      break
  }
  return keys
}

/** Subscribes to server-sent events and invalidates affected queries. */
export function useLiveEvents(qc: QueryClient, enabled: boolean) {
  const [connected, setConnected] = useState(false)
  useEffect(() => {
    if (!enabled) return
    let es: EventSource | null = null
    let retry: ReturnType<typeof setTimeout>
    const pending = new Map<string, unknown[]>()
    let flush: ReturnType<typeof setTimeout> | null = null
    const schedule = () => {
      if (flush) return
      flush = setTimeout(() => {
        flush = null
        pending.forEach((k) => qc.invalidateQueries({ queryKey: k }))
        pending.clear()
      }, 400)
    }
    const open = () => {
      es = new EventSource('/api/v1/events')
      es.addEventListener('hello', () => setConnected(true))
      es.onmessage = (m) => {
        try {
          const e = JSON.parse(m.data) as ServerEvent
          for (const k of keysFor(e)) pending.set(JSON.stringify(k), k)
          schedule()
        } catch {
          /* ignore malformed */
        }
      }
      es.onerror = () => {
        setConnected(false)
        es?.close()
        retry = setTimeout(open, 5000)
      }
    }
    open()
    return () => {
      clearTimeout(retry)
      if (flush) clearTimeout(flush)
      es?.close()
    }
  }, [qc, enabled])
  return connected
}
