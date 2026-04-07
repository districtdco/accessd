import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getAdminAuditEventDetail } from '../api'
import { useAuth } from '../auth'
import type { AdminAuditItem } from '../types'
import { Badge, Card, CardBody, CardHeader, ErrorState, InfoRow, LoadingState, PageHeader } from '../components/ui'

export function AdminAuditEventDetailPage() {
  const { eventID = '' } = useParams<{ eventID: string }>()
  const { user } = useAuth()
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
          setError(err instanceof Error ? err.message : 'failed to load audit event detail')
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
    <>
      <div className="mb-2 flex items-center gap-2 text-sm text-gray-500">
        <Link to="/admin/audit/events" className="hover:text-gray-700">Audit Events</Link>
        <span>/</span>
        <span className="text-gray-700">{eventID || 'detail'}</span>
      </div>
      <PageHeader title="Audit Event Detail" />

      {loading && <LoadingState message="Loading audit event detail..." />}
      {error && <ErrorState message={error} />}

      {!loading && !error && item && (
        <div className="space-y-4">
          <Card>
            <CardHeader title="Event" />
            <CardBody>
              <div className="grid gap-x-8 gap-y-1 sm:grid-cols-2">
                <InfoRow label="ID" value={String(item.id)} />
                <InfoRow label="Time" value={new Date(item.event_time).toLocaleString()} />
                <InfoRow label="Type" value={<Badge>{item.event_type}</Badge>} />
                <InfoRow label="Action" value={item.action || '-'} />
                <InfoRow label="Outcome" value={item.outcome || '-'} />
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Correlations" />
            <CardBody>
              <div className="space-y-1">
                <InfoRow
                  label="Actor User"
                  value={
                    item.actor_user?.id ? (
                      isAdmin ? (
                        <Link to={`/admin/users/${item.actor_user.id}`} className="text-indigo-600 hover:text-indigo-800">
                          {item.actor_user.username || item.actor_user.id}
                        </Link>
                      ) : (
                        item.actor_user.username || item.actor_user.id
                      )
                    ) : '-'
                  }
                />
                <InfoRow
                  label="Asset"
                  value={
                    item.asset?.id ? (
                      <>
                        {isAdmin ? (
                          <Link to={`/admin/assets/${item.asset.id}`} className="text-indigo-600 hover:text-indigo-800">
                            {item.asset.name || item.asset.id}
                          </Link>
                        ) : (
                          item.asset.name || item.asset.id
                        )}
                        {item.asset.asset_type && (
                          <> <Badge>{item.asset.asset_type}</Badge></>
                        )}
                      </>
                    ) : '-'
                  }
                />
                <InfoRow
                  label="Session"
                  value={
                    item.session_id ? (
                      <Link to={`/sessions/${item.session_id}`} className="font-mono text-xs text-indigo-600 hover:text-indigo-800">
                        {item.session_id}
                      </Link>
                    ) : '-'
                  }
                />
                {item.session && (
                  <InfoRow
                    label="Session Summary"
                    value={`action=${item.session.action || '-'} status=${item.session.status || '-'} created=${item.session.created_at ? new Date(item.session.created_at).toLocaleString() : '-'}`}
                  />
                )}
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Metadata" />
            <CardBody>
              <div className="rounded-lg border border-gray-200 bg-gray-900 p-4">
                <pre className="max-h-64 overflow-auto font-mono text-sm text-green-400 whitespace-pre-wrap">
                  {JSON.stringify(item.metadata ?? {}, null, 2)}
                </pre>
              </div>
            </CardBody>
          </Card>
        </div>
      )}
    </>
  )
}
