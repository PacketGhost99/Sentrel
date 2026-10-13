import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Activity, ArrowUpCircle, Bell, Boxes, Building2, CalendarClock, ChevronDown, ClipboardList, Cog, FileClock, Gauge, KeyRound, LayoutDashboard, LogOut,
  HardDriveDownload, Menu, Moon, PlayCircle, Radar, Search, Server, ShieldAlert, SlidersHorizontal, Sun, User as UserIcon, Users, Wrench, X, Globe, BookOpen,
} from 'lucide-react'
import { useAuth, getThemePref, setThemePref, type ThemePref } from '@/lib/auth'
import { get } from '@/lib/api'
import { dismiss, showBanner, showPill, type UpdateInfo } from '@/lib/updates'
import { useLiveEvents } from '@/lib/events'
import type { Host, Tenant } from '@/lib/types'
import { cx, PageBoundary, StatusDot } from './ui'

// Provider users can scope list views to one tenant.
const TenantCtx = createContext<{ tenant: number | null; setTenant: (t: number | null) => void }>({ tenant: null, setTenant: () => {} })
export const useTenantFilter = () => useContext(TenantCtx)

interface NavItem { to: string; label: string; icon: ReactNode; perm?: string; badge?: number; provider?: boolean }

export function Layout() {
  const { me, logout, can } = useAuth()
  const qc = useQueryClient()
  const live = useLiveEvents(qc, !!me && !me.mfa_pending)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [tenant, setTenantState] = useState<number | null>(() => {
    const v = sessionStorage.getItem('sentrel-tenant')
    return v ? Number(v) : null
  })
  const setTenant = (t: number | null) => {
    setTenantState(t)
    if (t === null) sessionStorage.removeItem('sentrel-tenant')
    else sessionStorage.setItem('sentrel-tenant', String(t))
    qc.invalidateQueries()
  }
  const loc = useLocation()
  useEffect(() => setMobileOpen(false), [loc.pathname])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen(true)
      } else if (e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes((e.target as HTMLElement).tagName)) {
        e.preventDefault()
        setPaletteOpen(true)
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])

  const provider = me?.user.tenant_id === 0
  const dash = useQuery({ queryKey: ['dashboard', tenant], queryFn: () => get('/dashboard', { tenant_id: tenant ?? undefined }), refetchInterval: 60_000, enabled: can('read') })
  const tenants = useQuery({ queryKey: ['tenants'], queryFn: () => get<Tenant[]>('/tenants'), enabled: provider && can('read') })
  const isAdmin = can('settings') && provider
  const upd = useQuery({ queryKey: ['updates'], queryFn: () => get<UpdateInfo>('/server/updates'), refetchInterval: 30 * 60_000, enabled: isAdmin })
  const [bannerGone, setBannerGone] = useState(false)
  const firing = (dash.data?.alerts?.critical ?? 0) + (dash.data?.alerts?.warning ?? 0)

  const sections: { title: string; items: NavItem[] }[] = [
    { title: '', items: [{ to: '/', label: 'Dashboard', icon: <LayoutDashboard />, perm: 'read' }] },
    {
      title: 'Monitoring', items: [
        { to: '/hosts', label: 'Hosts', icon: <Server />, perm: 'read' },
        { to: '/agents', label: 'Agents', icon: <HardDriveDownload />, perm: 'hosts' },
        { to: '/checks', label: 'Checks', icon: <Activity />, perm: 'read' },
        { to: '/alerts', label: 'Alerts', icon: <Bell />, perm: 'read', badge: firing },
        { to: '/maintenance', label: 'Maintenance', icon: <CalendarClock />, perm: 'read' },
        { to: '/thresholds', label: 'Thresholds', icon: <SlidersHorizontal />, perm: 'read' },
      ],
    },
    {
      title: 'Security & updates', items: [
        { to: '/vulnerabilities', label: 'Vulnerabilities', icon: <ShieldAlert />, perm: 'read' },
        { to: '/jobs', label: 'Jobs', icon: <PlayCircle />, perm: 'read' },
      ],
    },
    {
      title: 'Configure', items: [
        { to: '/groups', label: 'Groups & tags', icon: <Boxes />, perm: 'read' },
        { to: '/notifications', label: 'Notifications', icon: <Radar />, perm: 'read' },
        { to: '/status-pages', label: 'Status pages', icon: <Globe />, perm: 'read' },
      ],
    },
    {
      title: 'Administration', items: [
        { to: '/admin/users', label: 'Users', icon: <Users />, perm: 'users' },
        { to: '/admin/tenants', label: 'Tenants', icon: <Building2 />, perm: 'tenants' },
        { to: '/admin/api-keys', label: 'API keys', icon: <KeyRound /> },
        { to: '/admin/audit', label: 'Audit log', icon: <FileClock />, perm: 'audit' },
        { to: '/admin/settings', label: 'Settings', icon: <Cog />, perm: 'settings' },
        { to: '/admin/branding', label: 'Branding', icon: <Wrench />, perm: 'branding' },
        { to: '/api-docs', label: 'API reference', icon: <BookOpen /> },
      ],
    },
  ]

  const b = me?.branding
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    try { return JSON.parse(localStorage.getItem('sentrel-nav-collapsed') || '{}') } catch { return {} }
  })
  const toggleSection = (t: string) => setCollapsed((c) => {
    const n = { ...c, [t]: !c[t] }
    try { localStorage.setItem('sentrel-nav-collapsed', JSON.stringify(n)) } catch { /* storage unavailable */ }
    return n
  })
  const sidebar = (
    <nav className="flex h-full flex-col">
      <div className="flex h-16 items-center gap-2.5 px-5">
        {b?.logo_data_url ? <img src={b.logo_data_url} alt="" className="h-7 max-w-[140px] object-contain" /> : <Logo />}
        {!b?.logo_data_url && <span className="truncate text-[17px] font-semibold tracking-tight">{b?.product_name || 'Sentrel'}</span>}
      </div>
      <div className="scrollbar-thin flex-1 overflow-y-auto px-3 pb-3">
        {sections.map((s) => {
          const items = s.items.filter((i) => !i.perm || can(i.perm))
          if (!items.length) return null
          const hasActive = items.some((i) => (i.to === '/' ? loc.pathname === '/' : loc.pathname.startsWith(i.to)))
          const closed = !!s.title && !!collapsed[s.title] && !hasActive
          return (
            <div key={s.title} className="mb-3">
              {s.title && (
                <button onClick={() => toggleSection(s.title)} aria-expanded={!closed}
                  className="mb-1 flex w-full items-center justify-between rounded-md px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted hover:text-fg">
                  {s.title}
                  <ChevronDown className={cx('size-3.5 transition-transform', closed && '-rotate-90')} />
                </button>
              )}
              {!closed && items.map((i) => (
                <NavLink key={i.to} to={i.to} end={i.to === '/'}
                  className={({ isActive }) => cx('group relative flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm [&_svg]:size-[17px] [&_svg]:shrink-0',
                    isActive ? 'bg-accent/10 font-medium text-accent' : 'text-muted hover:bg-panel-2 hover:text-fg')}>
                  {i.icon}
                  <span className="flex-1 truncate">{i.label}</span>
                  {!!i.badge && <span className="rounded-full bg-crit px-1.5 py-px text-[11px] font-semibold leading-4 text-white">{i.badge}</span>}
                </NavLink>
              ))}
            </div>
          )
        })}
      </div>
      <div className="flex items-center justify-between px-5 py-3 text-[11px] text-muted">
        <span className="flex min-w-0 items-center gap-1.5">
          <span>v{me?.version}</span>
          {showPill(upd.data, isAdmin) && (
            <NavLink to="/admin/settings?tab=updates" title={`Sentrel ${upd.data!.latest} is available`}
              className="truncate rounded-full bg-ok/14 px-2 py-0.5 text-[10.5px] font-semibold text-ok-ink hover:bg-ok/22">Update v{upd.data!.latest}</NavLink>
          )}
        </span>
        <span className="flex items-center gap-1.5" title={live ? 'Live updates connected' : 'Live updates reconnecting'}>
          <StatusDot status={live ? 'up' : 'warning'} />{live ? 'live' : 'offline'}
        </span>
      </div>
    </nav>
  )

  return (
    <TenantCtx.Provider value={{ tenant, setTenant }}>
      <div className="flex h-full">
        <aside className="hidden w-60 shrink-0 border-r border-line/70 bg-panel lg:block">{sidebar}</aside>
        {mobileOpen && (
          <div className="fixed inset-0 z-40 lg:hidden" onClick={() => setMobileOpen(false)}>
            <div className="fade-in absolute inset-0 bg-slate-950/45 backdrop-blur-sm" />
            <aside className="pop-in absolute inset-y-0 left-0 w-64 border-r border-line bg-panel shadow-pop" onClick={(e) => e.stopPropagation()}>{sidebar}</aside>
          </div>
        )}
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-16 shrink-0 items-center gap-2 border-b border-line/70 bg-bg/80 px-3 backdrop-blur sm:px-6">
            <button className="rounded-lg p-2 text-muted hover:bg-panel-2 lg:hidden" onClick={() => setMobileOpen(true)} aria-label="Menu"><Menu className="size-5" /></button>
            <button onClick={() => setPaletteOpen(true)} className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-xl border border-line/70 bg-panel px-3.5 text-sm text-muted shadow-card hover:border-muted/40 hover:text-fg sm:max-w-sm">
              <Search className="size-4 shrink-0" /><span className="truncate">Search hosts and pages…</span>
              <kbd className="ml-auto hidden rounded-md border border-line bg-panel-2 px-1.5 text-[10px] sm:inline">Ctrl K</kbd>
            </button>
            <div className="flex-1" />
            {provider && (tenants.data?.length ?? 0) > 1 && (
              <div className="relative hidden sm:block">
                <select value={tenant ?? ''} onChange={(e) => setTenant(e.target.value === '' ? null : Number(e.target.value))}
                  className="h-10 appearance-none rounded-xl border border-line/70 bg-panel pl-3.5 pr-8 text-sm shadow-card">
                  <option value="">All tenants</option>
                  {tenants.data!.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
                <ChevronDown className="pointer-events-none absolute right-2.5 top-3 size-4 text-muted" />
              </div>
            )}
            <ThemeSwitch />
            <UserMenu name={me?.user.display_name || me?.user.username || ''} onLogout={logout} />
          </header>
          <main className="scrollbar-thin flex-1 overflow-y-auto">
            <div className="fade-in mx-auto max-w-[1500px] p-4 sm:p-6 lg:p-8">
              {!bannerGone && showBanner(upd.data, isAdmin) && (
                <div className="mb-4 flex items-center gap-3 rounded-xl border border-ok/25 bg-ok/10 px-4 py-2.5 text-sm">
                  <ArrowUpCircle className="size-4 shrink-0 text-ok" />
                  <span className="min-w-0 flex-1">Sentrel <b>{upd.data!.latest}</b> is available (you are running {upd.data!.current}).{' '}
                    <NavLink to="/admin/settings?tab=updates" className="font-medium text-accent hover:underline">View release notes and update</NavLink></span>
                  <button aria-label="Dismiss" title="Dismiss for this version" className="rounded-md p-1 text-muted hover:bg-panel-2 hover:text-fg"
                    onClick={() => { dismiss(upd.data!.latest!); setBannerGone(true) }}><X className="size-4" /></button>
                </div>
              )}
              <PageBoundary key={loc.pathname}><Outlet /></PageBoundary>
            </div>
            {me?.branding.footer_text && <footer className="px-5 pb-4 text-center text-xs text-muted">{me.branding.footer_text}</footer>}
          </main>
        </div>
      </div>
      {paletteOpen && <CommandPalette onClose={() => setPaletteOpen(false)} />}
    </TenantCtx.Provider>
  )
}

export function Logo({ className }: { className?: string }) {
  return (
    <>
      <img src="/logo-light.svg" alt="" aria-hidden className={cx('size-8 shrink-0 dark:hidden', className)} />
      <img src="/logo.svg" alt="" aria-hidden className={cx('hidden size-8 shrink-0 dark:block', className)} />
    </>
  )
}

function ThemeSwitch() {
  const [pref, setPref] = useState<ThemePref>(getThemePref())
  const next: ThemePref = pref === 'dark' ? 'light' : 'dark'
  return (
    <button title={pref === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'} aria-label="Toggle colour theme"
      onClick={() => { setPref(next); setThemePref(next) }}
      className="rounded-xl p-2.5 text-muted hover:bg-panel-2 hover:text-fg">
      {pref === 'dark' ? <Sun className="size-[18px]" /> : <Moon className="size-[18px]" />}
    </button>
  )
}

function UserMenu({ name, onLogout }: { name: string; onLogout: () => void }) {
  const [open, setOpen] = useState(false)
  const nav = useNavigate()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const h = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false)
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [])
  return (
    <div className="relative" ref={ref}>
      <button onClick={() => setOpen(!open)} className="flex items-center gap-2 rounded-xl px-2 py-1.5 text-sm hover:bg-panel-2">
        <span className="flex size-8 items-center justify-center rounded-full bg-accent/12 text-xs font-semibold uppercase text-accent">{name.slice(0, 2)}</span>
        <span className="hidden max-w-32 truncate sm:inline">{name}</span>
        <ChevronDown className="size-4 text-muted" />
      </button>
      {open && (
        <div className="pop-in absolute right-0 z-30 mt-2 w-52 rounded-xl border border-line/70 bg-panel p-1 shadow-pop">
          <button onClick={() => { setOpen(false); nav('/profile') }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm hover:bg-panel-2"><UserIcon className="size-4" />Profile & security</button>
          <button onClick={() => { setOpen(false); nav('/admin/api-keys') }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm hover:bg-panel-2"><KeyRound className="size-4" />API keys</button>
          <div className="my-1 border-t border-line/70" />
          <button onClick={onLogout} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-crit-ink hover:bg-panel-2"><LogOut className="size-4" />Log out</button>
        </div>
      )}
    </div>
  )
}

const pages = [
  ['Dashboard', '/'], ['Hosts', '/hosts'], ['Add host', '/hosts/new'], ['Agents', '/agents'], ['Checks', '/checks'], ['Alerts', '/alerts'], ['Maintenance windows', '/maintenance'],
  ['Thresholds', '/thresholds'], ['Vulnerabilities', '/vulnerabilities'], ['Jobs', '/jobs'], ['Groups & tags', '/groups'], ['Notifications', '/notifications'],
  ['Status pages', '/status-pages'], ['Users', '/admin/users'], ['Tenants', '/admin/tenants'], ['API keys', '/admin/api-keys'], ['Audit log', '/admin/audit'],
  ['Settings', '/admin/settings'], ['Branding', '/admin/branding'], ['Profile & security', '/profile'], ['API reference', '/api-docs'],
] as const

function CommandPalette({ onClose }: { onClose: () => void }) {
  const [q, setQ] = useState('')
  const [sel, setSel] = useState(0)
  const nav = useNavigate()
  const hosts = useQuery({ queryKey: ['hosts', 'palette'], queryFn: () => get<Host[]>('/hosts'), staleTime: 30_000 })
  const results = useMemo(() => {
    const s = q.trim().toLowerCase()
    const hs = (hosts.data || [])
      .filter((h) => !s || h.name.toLowerCase().includes(s) || h.address.toLowerCase().includes(s) || (h.tags || []).some((t) => t.toLowerCase().includes(s)))
      .slice(0, 8)
      .map((h) => ({ label: h.name, hint: h.address || h.kind, to: `/hosts/${h.id}`, icon: <StatusDot status={h.status} />, kind: 'Host' }))
    const ps = pages.filter(([l]) => !s || l.toLowerCase().includes(s)).slice(0, 8)
      .map(([l, to]) => ({ label: l, hint: to, to, icon: <ClipboardList className="size-4 text-muted" />, kind: 'Page' }))
    return [...hs, ...ps]
  }, [q, hosts.data])
  useEffect(() => setSel(0), [q])
  const go = (to: string) => { onClose(); nav(to) }
  return (
    <div className="fade-in fixed inset-0 z-50 flex items-start justify-center bg-slate-950/45 p-4 pt-[12vh] backdrop-blur-sm" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="pop-in w-full max-w-lg overflow-hidden rounded-2xl border border-line/70 bg-panel shadow-pop">
        <div className="flex items-center gap-2 border-b border-line/70 px-4">
          <Search className="size-4 text-muted" />
          <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search hosts, tags, IPs or pages"
            onKeyDown={(e) => {
              if (e.key === 'Escape') onClose()
              else if (e.key === 'ArrowDown') { e.preventDefault(); setSel((x) => Math.min(x + 1, results.length - 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setSel((x) => Math.max(x - 1, 0)) }
              else if (e.key === 'Enter' && results[sel]) go(results[sel].to)
            }}
            className="h-12 flex-1 bg-transparent text-sm outline-none" />
          <button onClick={onClose} className="text-muted hover:text-fg"><X className="size-4" /></button>
        </div>
        <div className="max-h-80 overflow-y-auto p-2">
          {results.length === 0 && <div className="px-4 py-6 text-center text-sm text-muted">No matches</div>}
          {results.map((r, i) => (
            <button key={r.kind + r.to} onMouseEnter={() => setSel(i)} onClick={() => go(r.to)}
              className={cx('flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm', i === sel && 'bg-panel-2')}>
              {r.icon}
              <span className="flex-1 truncate">{r.label}</span>
              <span className="truncate text-xs text-muted">{r.kind} · {r.hint}</span>
            </button>
          ))}
        </div>
        <div className="flex items-center gap-3 border-t border-line/70 px-4 py-2.5 text-[11px] text-muted"><Gauge className="size-3" />↑↓ to move · Enter to open · Esc to close</div>
      </div>
    </div>
  )
}
