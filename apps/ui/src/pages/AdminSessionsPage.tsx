import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getAdminSessions } from '../api'
import { useAuth } from '../auth'
import type { SessionSummary } from '../types'

const ACTION_OPTIONS = ['', 'shell', 'dbeaver', 'sftp', 'redis']
const STATUS_OPTIONS = ['', 'pending', 'active', 'completed', 'failed', 'terminated', 'expired']

export function AdminSessionsPage() {
  const { user, logout } = useAuth()
  const isAdmin = user?.roles.includes('admin') === true
  const [items, setItems] = useState<SessionSummary[]>([])
  const [status, setStatus] = useState('')
  const [action, setAction] = useState('')
  const [userID, setUserID] = useState('')
  const [assetID, setAssetID] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const exportHref = (() => {
    const params = new URLSearchParams()
    if (status) {
      params.set('status', status)
    }
    if (action) {
      params.set('action', action)
    }
    if (userID) {
      params.set('user_id', userID)
    }
    if (assetID) {
      params.set('asset_id', assetID)
    }
    params.set('limit', '200')
    const query = params.toString()
    return `/api/admin/sessions/export${query ? `?${query}` : ''}`
  })()

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await getAdminSessions({
        status,
        action,
        user_id: userID,
        asset_id: assetID,
        limit: 150,
      })
      setItems(response.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'failed to load admin sessions'
      setError(message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
    // Filter changes trigger reloads.
  }, [status, action, userID, assetID])

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Admin Sessions</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/admin/dashboard">Dashboard</Link>
          <Link to="/admin/audit/events">Audit Events</Link>
          <Link to="/sessions">My Sessions</Link>
          {isAdmin ? <Link to="/admin/users">Admin Users</Link> : null}
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      <section className="card section-block">
        <h2>Filters</h2>
        <div className="actions-inline">
          <label>
            Action{' '}
            <select value={action} onChange={(e) => setAction(e.target.value)}>
              {ACTION_OPTIONS.map((option) => (
                <option key={option || 'all-actions'} value={option}>
                  {option || 'all'}
                </option>
              ))}
            </select>
          </label>
          <label>
            Status{' '}
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              {STATUS_OPTIONS.map((option) => (
                <option key={option || 'all-status'} value={option}>
                  {option || 'all'}
                </option>
              ))}
            </select>
          </label>
          <label>
            User ID{' '}
            <input value={userID} onChange={(e) => setUserID(e.target.value)} placeholder="optional" />
          </label>
          <label>
            Asset ID{' '}
            <input value={assetID} onChange={(e) => setAssetID(e.target.value)} placeholder="optional" />
          </label>
          <a href={exportHref}>Export CSV</a>
        </div>
      </section>

      {loading ? <p>Loading sessions...</p> : null}
      {error === null ? null : <p className="error">{error}</p>}

      {loading === false && error === null ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Session</th>
                <th>User</th>
                <th>Asset</th>
                <th>Asset Type</th>
                <th>Action</th>
                <th>Launch Type</th>
                <th>Status</th>
                <th>Created</th>
                <th>Duration (s)</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.session_id}>
                  <td>
                    <Link to={`/sessions/${item.session_id}`}>{item.session_id}</Link>
                  </td>
                  <td>{item.user.username}</td>
                  <td>{item.asset.name}</td>
                  <td>{item.asset.asset_type}</td>
                  <td>{item.action}</td>
                  <td>{item.launch_type}</td>
                  <td>{item.status}</td>
                  <td>{new Date(item.created_at).toLocaleString()}</td>
                  <td>{item.duration_seconds === undefined ? '-' : item.duration_seconds}</td>
                </tr>
              ))}
              {items.length === 0 ? (
                <tr>
                  <td colSpan={9} className="muted">
                    No sessions found for the selected filters.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      ) : null}
    </main>
  )
}
