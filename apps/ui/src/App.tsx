import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import { AppLayout } from './components/AppLayout'
import { AccessPage } from './pages/AccessPage'
import { AdminAssetsPage } from './pages/AdminAssetsPage'
import { AdminAssetDetailPage } from './pages/AdminAssetDetailPage'
import { AdminDashboardPage } from './pages/AdminDashboardPage'
import { AdminAuditEventsPage } from './pages/AdminAuditEventsPage'
import { AdminAuditEventDetailPage } from './pages/AdminAuditEventDetailPage'
import { AdminSessionsPage } from './pages/AdminSessionsPage'
import { AdminUserDetailPage } from './pages/AdminUserDetailPage'
import { AdminUsersPage } from './pages/AdminUsersPage'
import { LoginPage } from './pages/LoginPage'
import { MySessionsPage } from './pages/MySessionsPage'
import { SessionDetailPage } from './pages/SessionDetailPage'

function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<LoginRoute />} />
        <Route path="/" element={<ProtectedRoute><AccessPage /></ProtectedRoute>} />
        <Route path="/sessions" element={<ProtectedRoute><MySessionsPage /></ProtectedRoute>} />
        <Route path="/sessions/:sessionID" element={<ProtectedRoute><SessionDetailPage /></ProtectedRoute>} />
        <Route path="/admin/users" element={<AdminRoute><AdminUsersPage /></AdminRoute>} />
        <Route path="/admin/users/:userID" element={<AdminRoute><AdminUserDetailPage /></AdminRoute>} />
        <Route path="/admin/assets" element={<AdminRoute><AdminAssetsPage /></AdminRoute>} />
        <Route path="/admin/assets/:assetID" element={<AdminRoute><AdminAssetDetailPage /></AdminRoute>} />
        <Route path="/admin/dashboard" element={<AdminReadRoute><AdminDashboardPage /></AdminReadRoute>} />
        <Route path="/admin/audit/events" element={<AdminReadRoute><AdminAuditEventsPage /></AdminReadRoute>} />
        <Route path="/admin/audit/events/:eventID" element={<AdminReadRoute><AdminAuditEventDetailPage /></AdminReadRoute>} />
        <Route path="/admin/sessions" element={<AdminReadRoute><AdminSessionsPage /></AdminReadRoute>} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AuthProvider>
  )
}

function LoginRoute() {
  const { status } = useAuth()
  if (status === 'loading') {
    return <PageLoader />
  }
  if (status === 'authenticated') {
    return <Navigate to="/" replace />
  }
  return <LoginPage />
}

function ProtectedRoute({ children }: { children: JSX.Element }) {
  const { status } = useAuth()
  if (status === 'loading') {
    return <PageLoader />
  }
  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace />
  }
  return <AppLayout>{children}</AppLayout>
}

function AdminRoute({ children }: { children: JSX.Element }) {
  const { status, user } = useAuth()
  if (status === 'loading') {
    return <PageLoader />
  }
  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace />
  }
  if (user?.roles.includes('admin') !== true) {
    return <Navigate to="/" replace />
  }
  return <AppLayout>{children}</AppLayout>
}

function AdminReadRoute({ children }: { children: JSX.Element }) {
  const { status, user } = useAuth()
  if (status === 'loading') {
    return <PageLoader />
  }
  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace />
  }
  const canReadAdmin = user?.roles.includes('admin') || user?.roles.includes('auditor')
  if (canReadAdmin !== true) {
    return <Navigate to="/" replace />
  }
  return <AppLayout>{children}</AppLayout>
}

function PageLoader() {
  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <div className="flex items-center gap-3 text-gray-500">
        <svg className="h-5 w-5 animate-spin" viewBox="0 0 24 24" fill="none">
          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
        </svg>
        <span className="text-sm">Loading...</span>
      </div>
    </div>
  )
}

export default App
