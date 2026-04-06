import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getMySessions } from '../api'
import { useAuth } from '../auth'
import type { SessionSummary } from '../types'

const ACTION_OPTIONS = ['', 'shell', 'dbeaver', 'sftp', 'redis']
const STATUS_OPTIONS = ['', 'pending', 'active', 'completed', 'failed', 'terminated', 'expired']

export function MySessionsPage() {
  const { user, logout } = useAuth()
  const canReadAdmin = user?.roles.includes('admin') || user?.roles.includes('auditor')
  const [items, setItems] = useState<SessionSummary[]>([])
  const [status, setStatus] = useState('')
  const [action, setAction] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = async (nextStatus: string, nextAction: string) => {
    setLoading(true)
    setError(null)
    try {
      const response = await getMySessions({ status: nextStatus, action: nextAction, limit: 100 })
      setItems(response.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'failed to load sessions'
      setError(message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load(status, action)
    // Filter changes trigger reloads.
  }, [status, action])

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>My Sessions</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          {canReadAdmin ? <Link to="/admin/dashboard">Admin Dashboard</Link> : null}
          {canReadAdmin ? <Link to="/admin/sessions">Admin Sessions</Link> : null}
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
                  <td colSpan={8} className="muted">
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
