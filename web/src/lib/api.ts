// Thin client for the Sentrel REST API. Every UI action goes through it.

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

let csrf = ''
export function setCsrf(t: string) {
  csrf = t || ''
}

type Listener = () => void
const unauthListeners = new Set<Listener>()
export function onUnauthorized(l: Listener) {
  unauthListeners.add(l)
  return () => unauthListeners.delete(l)
}

export async function api<T = any>(path: string, opts: { method?: string; body?: unknown; query?: Record<string, unknown>; raw?: boolean } = {}): Promise<T> {
  let url = '/api/v1' + path
  if (opts.query) {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(opts.query)) {
      if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
    }
    const s = q.toString()
    if (s) url += (url.includes('?') ? '&' : '?') + s
  }
  const method = opts.method || (opts.body !== undefined ? 'POST' : 'GET')
  const headers: Record<string, string> = { Accept: 'application/json' }
  let body: BodyInit | undefined
  if (opts.body !== undefined) {
    if (opts.raw) {
      body = opts.body as BodyInit
    } else {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(opts.body)
    }
  }
  if (method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf
  const res = await fetch(url, { method, headers, body, credentials: 'same-origin' })
  if (res.status === 204) return undefined as T
  let data: any = null
  const text = await res.text()
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }
  if (!res.ok) {
    const msg = (data && data.error) || res.statusText || 'Request failed'
    const code = (data && data.code) || 'error'
    if (res.status === 401 && code !== 'mfa_required' && !path.startsWith('/auth/login') && !path.startsWith('/auth/mfa') && !path.startsWith('/auth/passkey')) {
      unauthListeners.forEach((l) => l())
    }
    throw new ApiError(res.status, code, msg)
  }
  return data as T
}

export const get = <T = any>(path: string, query?: Record<string, unknown>) => api<T>(path, { query })
export const post = <T = any>(path: string, body?: unknown, query?: Record<string, unknown>) => api<T>(path, { method: 'POST', body: body ?? {}, query })
export const put = <T = any>(path: string, body?: unknown, query?: Record<string, unknown>) => api<T>(path, { method: 'PUT', body: body ?? {}, query })
export const del = <T = any>(path: string) => api<T>(path, { method: 'DELETE' })

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}
