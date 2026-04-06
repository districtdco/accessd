import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getAdminAuditRecent, getAdminSessionsActive, getAdminSummary } from '../api'
import { useAuth } from '../auth'
import type { AdminAuditItem, AdminSummaryResponse, SessionSummary } from '../types'

const DEFAULT_WINDOW_DAYS = 7

export function AdminDashboardPage() {
  const { user, logout } = useAuth()
  const isAdmin = user?.roles.includes('admin') === true
  const [windowDays, setWindowDays] = useState(DEFAULT_WINDOW_DAYS)
  const [summary, setSummary] = useState<AdminSummaryResponse | null>(null)
  const [auditItems, setAuditItems] = useState<AdminAuditItem[]>([])
  const [activeSessions, setActiveSessions] = useState<SessionSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = async (nextWindowDays: number) => {
    setLoading(true)
    setError(null)
    try {
      const [summaryResponse, auditResponse, activeResponse] = await Promise.all([
        getAdminSummary(nextWindowDays),
        getAdminAuditRecent(25),
        getAdminSessionsActive(50),
      ])
      setSummary(summaryResponse)
      setAuditItems(auditResponse.items)
      setActiveSessions(activeResponse.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'failed to load admin dashboard'
      setError(message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load(windowDays)
  }, [windowDays])

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Admin Dashboard</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/sessions">My Sessions</Link>
          <Link to="/admin/audit/events">Audit Events</Link>
          {isAdmin ? <Link to="/admin/users">Users</Link> : null}
          {isAdmin ? <Link to="/admin/assets">Assets</Link> : null}
          <Link to="/admin/sessions">All Sessions</Link>
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      <section className="card section-block">
        <div className="actions-inline">
          <label>
            Summary Window{' '}
            <select value={windowDays} onChange={(e) => setWindowDays(Number(e.target.value))}>
              <option value={1}>1 day</option>
              <option value={7}>7 days</option>
              <option value={14}>14 days</option>
              <option value={30}>30 days</option>
            </select>
          </label>
          <button onClick={() => void load(windowDays)} disabled={loading}>
            {loading ? 'Refreshing...' : 'Refresh'}
          </button>
        </div>
      </section>

      {loading ? <p>Loading dashboard...</p> : null}
      {error ? <p className="error">{error}</p> : null}

      {loading === false && error === null && summary ? (
        <>
          <section className="summary-grid">
            <article className="card summary-card">
              <h2>Recent Sessions</h2>
              <p className="summary-value">{summary.metrics.recent_sessions}</p>
              <p className="muted">Last {summary.window_days} days</p>
            </article>
            <article className="card summary-card">
              <h2>Active Sessions</h2>
              <p className="summary-value">{summary.metrics.active_sessions}</p>
              <p className="muted">Current status=active</p>
            </article>
            <article className="card summary-card">
              <h2>Failed Sessions</h2>
              <p className="summary-value">{summary.metrics.failed_sessions}</p>
              <p className="muted">Last {summary.window_days} days</p>
            </article>
            <article className="card summary-card">
              <h2>Shell Launches</h2>
              <p className="summary-value">{summary.metrics.shell_launches}</p>
              <p className="muted">Last {summary.window_days} days</p>
            </article>
            <article className="card summary-card">
              <h2>DBeaver Launches</h2>
              <p className="summary-value">{summary.metrics.dbeaver_launches}</p>
              <p className="muted">Last {summary.window_days} days</p>
            </article>
          </section>

          <section className="card section-block">
            <h2>Sessions by Action</h2>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Action</th>
                    <th>Count</th>
                  </tr>
                </thead>
                <tbody>
                  {summary.by_action.map((item) => (
                    <tr key={item.action}>
                      <td>{item.action}</td>
                      <td>{item.count}</td>
                    </tr>
                  ))}
                  {summary.by_action.length === 0 ? (
                    <tr>
                      <td colSpan={2} className="muted">
                        No recent actions in this window.
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>

          <section className="card section-block">
            <div className="topbar">
              <h2>Active Sessions</h2>
              <Link to="/admin/sessions">Open full session history</Link>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Session</th>
                    <th>User</th>
                    <th>Asset</th>
                    <th>Action</th>
                    <th>Started</th>
                    <th>Duration (s)</th>
                  </tr>
                </thead>
                <tbody>
                  {activeSessions.map((item) => (
                    <tr key={item.session_id}>
                      <td>
                        <Link to={`/sessions/${item.session_id}`}>{item.session_id}</Link>
                      </td>
                      <td>{item.user.username}</td>
                      <td>{item.asset.name}</td>
                      <td>{item.action}</td>
                      <td>{item.started_at ? new Date(item.started_at).toLocaleString() : '-'}</td>
                      <td>{item.duration_seconds ?? '-'}</td>
                    </tr>
                  ))}
                  {activeSessions.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="muted">
                        No active sessions right now.
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>

          <section className="card section-block">
            <div className="topbar">
              <h2>Recent Audit Activity</h2>
              <Link to="/admin/audit/events">Open audit search</Link>
            </div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>ID</th>
                    <th>Time</th>
                    <th>Event</th>
                    <th>Action</th>
                    <th>Outcome</th>
                    <th>User</th>
                    <th>Asset</th>
                    <th>Session</th>
                  </tr>
                </thead>
                <tbody>
                  {auditItems.map((item) => (
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
                      <td>
                        {item.session_id ? <Link to={`/sessions/${item.session_id}`}>{item.session_id}</Link> : '-'}
                      </td>
                    </tr>
                  ))}
                  {auditItems.length === 0 ? (
                    <tr>
                      <td colSpan={8} className="muted">
                        No recent audit events found.
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>
        </>
      ) : null}
    </main>
  )
}
