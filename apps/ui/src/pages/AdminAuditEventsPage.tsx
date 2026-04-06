import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getAdminAuditEvents } from '../api'
import { useAuth } from '../auth'
import type { AdminAuditItem } from '../types'

const LIMIT_OPTIONS = [25, 50, 100, 200]

export function AdminAuditEventsPage() {
  const { user, logout } = useAuth()
  const isAdmin = user?.roles.includes('admin') === true

  const [items, setItems] = useState<AdminAuditItem[]>([])
  const [eventType, setEventType] = useState('')
  const [action, setAction] = useState('')
  const [userID, setUserID] = useState('')
  const [assetID, setAssetID] = useState('')
  const [sessionID, setSessionID] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [limit, setLimit] = useState(100)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await getAdminAuditEvents({
        event_type: eventType,
        action,
        user_id: userID,
        asset_id: assetID,
        session_id: sessionID,
        from: from ? new Date(from).toISOString() : undefined,
        to: to ? new Date(to).toISOString() : undefined,
        limit,
      })
      setItems(response.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'failed to load audit events'
      setError(message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Admin Audit Events</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/admin/dashboard">Dashboard</Link>
          <Link to="/admin/sessions">Sessions</Link>
          {isAdmin ? <Link to="/admin/users">Users</Link> : null}
          {isAdmin ? <Link to="/admin/assets">Assets</Link> : null}
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      <section className="card section-block">
        <h2>Filters</h2>
        <div className="form-grid">
          <label>
            Event Type
            <input value={eventType} onChange={(e) => setEventType(e.target.value)} placeholder="session_start" />
          </label>
          <label>
            Action
            <input value={action} onChange={(e) => setAction(e.target.value)} placeholder="shell_end" />
          </label>
          <label>
            User ID
            <input value={userID} onChange={(e) => setUserID(e.target.value)} placeholder="actor user id" />
          </label>
          <label>
            Asset ID
            <input value={assetID} onChange={(e) => setAssetID(e.target.value)} placeholder="asset id" />
          </label>
          <label>
            Session ID
            <input value={sessionID} onChange={(e) => setSessionID(e.target.value)} placeholder="session id" />
          </label>
          <label>
            Limit
            <select value={limit} onChange={(e) => setLimit(Number(e.target.value))}>
              {LIMIT_OPTIONS.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </label>
          <label>
            From
            <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} />
          </label>
          <label>
            To
            <input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} />
          </label>
        </div>
        <div className="actions-inline">
          <button onClick={() => void load()} disabled={loading}>
            {loading ? 'Searching...' : 'Search'}
          </button>
        </div>
      </section>

      {loading ? <p>Loading audit events...</p> : null}
      {error ? <p className="error">{error}</p> : null}

      {loading === false && error === null ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Time</th>
                <th>Type</th>
                <th>Action</th>
                <th>Outcome</th>
                <th>Actor</th>
                <th>Asset</th>
                <th>Session</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id}>
                  <td>
                    <Link to={`/admin/audit/events/${item.id}`}>{item.id}</Link>
                  </td>
                  <td>{new Date(item.event_time).toLocaleString()}</td>
                  <td>{item.event_type}</td>
                  <td>{item.action || '-'}</td>
                  <td>{item.outcome || '-'}</td>
                  <td>{item.actor_user?.username || item.actor_user?.id || '-'}</td>
                  <td>{item.asset?.name || item.asset?.id || '-'}</td>
                  <td>{item.session_id ? <Link to={`/sessions/${item.session_id}`}>{item.session_id}</Link> : '-'}</td>
                </tr>
              ))}
              {items.length === 0 ? (
                <tr>
                  <td colSpan={8} className="muted">
                    No audit events found for current filters.
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
