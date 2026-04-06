import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
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
    return <PageMessage message="Checking session..." />
  }
  if (status === 'authenticated') {
    return <Navigate to="/" replace />
  }
  return <LoginPage />
}

function ProtectedRoute({ children }: { children: JSX.Element }) {
  const { status } = useAuth()
  if (status === 'loading') {
    return <PageMessage message="Loading..." />
  }
  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace />
  }
  return children
}

function AdminRoute({ children }: { children: JSX.Element }) {
  const { status, user } = useAuth()
  if (status === 'loading') {
    return <PageMessage message="Loading..." />
  }
  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace />
  }
  if (user?.roles.includes('admin') !== true) {
    return <Navigate to="/" replace />
  }
  return children
}

function AdminReadRoute({ children }: { children: JSX.Element }) {
  const { status, user } = useAuth()
  if (status === 'loading') {
    return <PageMessage message="Loading..." />
  }
  if (status === 'unauthenticated') {
    return <Navigate to="/login" replace />
  }
  const canReadAdmin = user?.roles.includes('admin') || user?.roles.includes('auditor')
  if (canReadAdmin !== true) {
    return <Navigate to="/" replace />
  }
  return children
}

function PageMessage({ message }: { message: string }) {
  return (
    <main className="page-center">
      <p>{message}</p>
    </main>
  )
}

export default App
