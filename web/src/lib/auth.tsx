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
  oidc_enabled?: boolean
  oidc_label?: string
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
  // Earlier default accents are ignored so the brand palette applies unless a custom colour was chosen.
  const legacy = ['#e11d48', '#4f46e5', '#1f6bf5']
  if (b.accent_color && !legacy.includes(b.accent_color.toLowerCase())) root.style.setProperty('--accent', b.accent_color)
  else root.style.removeProperty('--accent')
  document.title = b.product_name || 'Sentrel'
  const fav = document.getElementById('favicon') as HTMLLinkElement | null
  if (fav) fav.href = b.favicon_data_url || '/favicon.svg'
}

export type ThemePref = 'light' | 'dark'

export function applyTheme(pref: ThemePref) {
  document.documentElement.classList.toggle('dark', pref === 'dark')
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', pref === 'dark' ? '#0c111d' : '#f4f6fb')
}

/** Light is the default; any other stored value (including the legacy "system") is treated as light. */
export function getThemePref(): ThemePref {
  try {
    return localStorage.getItem('sentrel-theme') === 'dark' ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}

export function setThemePref(p: ThemePref) {
  try {
    localStorage.setItem('sentrel-theme', p)
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
