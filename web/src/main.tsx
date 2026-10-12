import { StrictMode, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@fontsource-variable/inter'
import './index.css'
import { AuthProvider, useAuth } from './lib/auth'
import { ConfirmProvider, Loading, ToastProvider } from './components/ui'
import { Layout } from './components/Layout'
import { Login, MfaSetupRequired } from './pages/Login'
import { Setup } from './pages/Setup'
import { Dashboard } from './pages/Dashboard'
import { Hosts } from './pages/Hosts'
import { HostDetail } from './pages/HostDetail'
import { AddHost } from './pages/AddHost'
import { Agents } from './pages/Agents'
import { Checks, CheckDetail } from './pages/Checks'
import { Alerts, AlertDetail } from './pages/Alerts'
import { Maintenance } from './pages/Maintenance'
import { Thresholds } from './pages/Thresholds'
import { Groups } from './pages/Groups'
import { Notifications } from './pages/Notifications'
import { Vulnerabilities } from './pages/Vulnerabilities'
import { Jobs, JobDetail } from './pages/Jobs'
import { StatusPages, PublicStatus } from './pages/StatusPages'
import { UsersPage } from './pages/Users'
import { Tenants } from './pages/Tenants'
import { ApiKeys } from './pages/ApiKeys'
import { Audit } from './pages/Audit'
import { Settings, BrandingPage } from './pages/Settings'
import { Profile } from './pages/Profile'
import { ApiDocs } from './pages/ApiDocs'
import { NotFound } from './pages/NotFound'

const qc = new QueryClient({
  defaultOptions: {
    queries: { refetchOnWindowFocus: true, retry: 1, staleTime: 10_000 },
  },
})

function Guard({ children }: { children: ReactNode }) {
  const { me, info, loading } = useAuth()
  const loc = useLocation()
  if (loading) return <Loading />
  if (info?.setup_required) return <Navigate to="/setup" replace />
  if (!me) return <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />
  if (me.mfa_pending) return <Navigate to="/login" replace />
  if (me.mfa_setup_required) return <MfaSetupRequired />
  return <>{children}</>
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/setup" element={<Setup />} />
      <Route path="/status/:slug" element={<PublicStatus />} />
      <Route element={<Guard><Layout /></Guard>}>
        <Route index element={<Dashboard />} />
        <Route path="hosts" element={<Hosts />} />
        <Route path="hosts/new" element={<AddHost />} />
        <Route path="agents" element={<Agents />} />
        <Route path="hosts/:id" element={<HostDetail />} />
        <Route path="checks" element={<Checks />} />
        <Route path="checks/:id" element={<CheckDetail />} />
        <Route path="alerts" element={<Alerts />} />
        <Route path="alerts/:id" element={<AlertDetail />} />
        <Route path="maintenance" element={<Maintenance />} />
        <Route path="thresholds" element={<Thresholds />} />
        <Route path="groups" element={<Groups />} />
        <Route path="notifications" element={<Notifications />} />
        <Route path="vulnerabilities" element={<Vulnerabilities />} />
        <Route path="jobs" element={<Jobs />} />
        <Route path="jobs/:id" element={<JobDetail />} />
        <Route path="status-pages" element={<StatusPages />} />
        <Route path="admin/users" element={<UsersPage />} />
        <Route path="admin/tenants" element={<Tenants />} />
        <Route path="admin/api-keys" element={<ApiKeys />} />
        <Route path="admin/audit" element={<Audit />} />
        <Route path="admin/settings" element={<Settings />} />
        <Route path="admin/branding" element={<BrandingPage />} />
        <Route path="profile" element={<Profile />} />
        <Route path="api-docs" element={<ApiDocs />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <AuthProvider>
          <ToastProvider>
            <ConfirmProvider>
              <App />
            </ConfirmProvider>
          </ToastProvider>
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
