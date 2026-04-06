import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getAdminAuditEventDetail } from '../api'
import { useAuth } from '../auth'
import type { AdminAuditItem } from '../types'

export function AdminAuditEventDetailPage() {
  const { eventID = '' } = useParams<{ eventID: string }>()
  const { user, logout } = useAuth()
  const isAdmin = user?.roles.includes('admin') === true

  const [item, setItem] = useState<AdminAuditItem | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)

      const id = Number(eventID)
      if (!Number.isFinite(id) || id <= 0) {
        setError('invalid event id')
        setLoading(false)
        return
      }

      try {
        const response = await getAdminAuditEventDetail(id)
        if (!cancelled) {
          setItem(response.item)
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : 'failed to load audit event detail'
          setError(message)
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }

    void load()
    return () => {
      cancelled = true
    }
  }, [eventID])

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Audit Event Detail</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/admin/dashboard">Dashboard</Link>
          <Link to="/admin/audit/events">Audit Events</Link>
          <Link to="/admin/sessions">Sessions</Link>
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      {loading ? <p>Loading audit event detail...</p> : null}
      {error ? <p className="error">{error}</p> : null}

      {loading === false && error === null && item ? (
        <>
          <section className="card section-block">
            <h2>Event</h2>
            <p>
              <strong>ID:</strong> {item.id}
            </p>
            <p>
              <strong>Time:</strong> {new Date(item.event_time).toLocaleString()}
            </p>
            <p>
              <strong>Type:</strong> {item.event_type}
            </p>
            <p>
              <strong>Action:</strong> {item.action || '-'}
            </p>
            <p>
              <strong>Outcome:</strong> {item.outcome || '-'}
            </p>
          </section>

          <section className="card section-block">
            <h2>Correlations</h2>
            <p>
              <strong>Actor User:</strong>{' '}
              {item.actor_user?.id ? (
                isAdmin ? (
                  <Link to={`/admin/users/${item.actor_user.id}`}>
                    {item.actor_user.username || item.actor_user.id}
                  </Link>
                ) : (
                  item.actor_user.username || item.actor_user.id
                )
              ) : (
                '-'
              )}
            </p>
            <p>
              <strong>Asset:</strong>{' '}
              {item.asset?.id ? (
                isAdmin ? (
                  <Link to={`/admin/assets/${item.asset.id}`}>{item.asset.name || item.asset.id}</Link>
                ) : (
                  item.asset.name || item.asset.id
                )
              ) : (
                '-'
              )}
              {item.asset?.asset_type ? ` (${item.asset.asset_type})` : ''}
            </p>
            <p>
              <strong>Session:</strong>{' '}
              {item.session_id ? <Link to={`/sessions/${item.session_id}`}>{item.session_id}</Link> : '-'}
            </p>
            {item.session ? (
              <p>
                <strong>Session Summary:</strong> action={item.session.action || '-'} status={item.session.status || '-'} created=
                {item.session.created_at ? new Date(item.session.created_at).toLocaleString() : '-'}
              </p>
            ) : null}
          </section>

          <section className="card section-block">
            <h2>Metadata</h2>
            <div className="transcript-panel">
              <pre>{JSON.stringify(item.metadata ?? {}, null, 2)}</pre>
            </div>
          </section>
        </>
      ) : null}
    </main>
  )
}
