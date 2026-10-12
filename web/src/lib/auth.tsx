import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { get, onUnauthorized, post, setCsrf } from './api'
import type { Branding, Me } from './types'

export interface PublicInfo {
  setup_required: boolean
  version: string
  branding: Branding
  tenant_slug?: string
  tenant_name?: string
  ldap: boolean
  radius: boolean
  server_url: string
  tls_mode: string
}

interface AuthState {
  me: Me | null
  info: PublicInfo | null
  loading: boolean
  refresh: () => Promise<Me | null>
  logout: () => Promise<void>
  can: (perm: string) => boolean
  setMe: (m: Me | null) => void
}

const Ctx = createContext<AuthState>(null as unknown as AuthState)
export const useAuth = () => useContext(Ctx)

export function applyBranding(b?: Branding | null) {
  if (!b) return
  const root = document.documentElement
  if (b.accent_color) root.style.setProperty('--accent', b.accent_color)
  document.title = b.product_name || 'Heartbeat'
  const fav = document.getElementById('favicon') as HTMLLinkElement | null
  if (fav) fav.href = b.favicon_data_url || '/favicon.svg'
}

export type ThemePref = 'system' | 'light' | 'dark'

export function applyTheme(pref: ThemePref) {
  const dark = pref === 'dark' || (pref === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
}

export function getThemePref(): ThemePref {
  try {
    return (localStorage.getItem('hb-theme') as ThemePref) || 'system'
  } catch {
    return 'system'
  }
}

export function setThemePref(p: ThemePref) {
  try {
    localStorage.setItem('hb-theme', p)
  } catch {
    /* storage unavailable */
  }
  applyTheme(p)
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMeState] = useState<Me | null>(null)
  const [info, setInfo] = useState<PublicInfo | null>(null)
  const [loading, setLoading] = useState(true)

  const setMe = useCallback((m: Me | null) => {
    setMeState(m)
    if (m) {
      setCsrf(m.csrf_token)
      applyBranding(m.branding)
    }
  }, [])

  const refresh = useCallback(async () => {
    // Public info changes too (e.g. setup_required after the wizard).
    get<PublicInfo>('/public/info', { tenant: new URLSearchParams(location.search).get('tenant') || '' }).then(setInfo).catch(() => {})
    try {
      const m = await get<Me>('/auth/me')
      setMe(m)
      return m
    } catch {
      setMeState(null)
      return null
    }
  }, [setMe])

  useEffect(() => {
    applyTheme(getThemePref())
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const h = () => applyTheme(getThemePref())
    mq.addEventListener('change', h)
    const tenant = new URLSearchParams(location.search).get('tenant') || ''
    Promise.all([
      get<PublicInfo>('/public/info', { tenant }).then((i) => {
        setInfo(i)
        applyBranding(i.branding)
      }).catch(() => {}),
      refresh(),
    ]).finally(() => setLoading(false))
    const off = onUnauthorized(() => setMeState(null))
    return () => {
      mq.removeEventListener('change', h)
      off()
    }
  }, [refresh])

  const logout = useCallback(async () => {
    try {
      await post('/auth/logout')
    } finally {
      setMeState(null)
      setCsrf('')
    }
  }, [])

  const can = useCallback((perm: string) => !!me?.permissions?.includes(perm), [me])

  return <Ctx.Provider value={{ me, info, loading, refresh, logout, can, setMe }}>{children}</Ctx.Provider>
}
