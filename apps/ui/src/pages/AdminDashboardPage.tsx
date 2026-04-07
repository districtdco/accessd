import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getAdminAuditRecent, getAdminSessionsActive, getAdminSummary } from '../api'
import type { AdminAuditItem, AdminSummaryResponse, SessionSummary } from '../types'
import { Badge, Button, Card, CardHeader, EmptyRow, ErrorState, LoadingState, PageHeader, Select, StatCard, Table, Td, Th } from '../components/ui'

const WINDOW_OPTIONS = [
  { value: '1', label: '1 day' },
  { value: '7', label: '7 days' },
  { value: '14', label: '14 days' },
  { value: '30', label: '30 days' },
]

export function AdminDashboardPage() {
  const [windowDays, setWindowDays] = useState(7)
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
    <>
      <PageHeader title="Dashboard">
        <div className="flex items-end gap-2">
          <div className="w-32">
            <Select
              value={String(windowDays)}
              onChange={(v) => setWindowDays(Number(v))}
              options={WINDOW_OPTIONS}
            />
          </div>
          <Button variant="secondary" disabled={loading} onClick={() => void load(windowDays)}>
            {loading ? 'Refreshing...' : 'Refresh'}
          </Button>
        </div>
      </PageHeader>

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {loading && <LoadingState message="Loading dashboard..." />}

      {!loading && !error && summary && (
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
            <StatCard label="Recent Sessions" value={summary.metrics.recent_sessions} subtitle={`Last ${summary.window_days} days`} />
            <StatCard label="Active Sessions" value={summary.metrics.active_sessions} subtitle="Current" />
            <StatCard label="Failed Sessions" value={summary.metrics.failed_sessions} subtitle={`Last ${summary.window_days} days`} />
            <StatCard label="Shell Launches" value={summary.metrics.shell_launches} subtitle={`Last ${summary.window_days} days`} />
            <StatCard label="DBeaver Launches" value={summary.metrics.dbeaver_launches} subtitle={`Last ${summary.window_days} days`} />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader title="Sessions by Action" />
              <Table>
                <thead>
                  <tr>
                    <Th>Action</Th>
                    <Th>Count</Th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {summary.by_action.map((item) => (
                    <tr key={item.action} className="hover:bg-gray-50">
                      <Td><Badge color="indigo">{item.action}</Badge></Td>
                      <Td className="font-semibold">{item.count}</Td>
                    </tr>
                  ))}
                  {summary.by_action.length === 0 && <EmptyRow colSpan={2} message="No recent actions in this window." />}
                </tbody>
              </Table>
            </Card>

            <Card>
              <CardHeader title="Active Sessions">
                <Link to="/admin/sessions" className="text-sm text-indigo-600 hover:text-indigo-800">View all</Link>
              </CardHeader>
              <Table>
                <thead>
                  <tr>
                    <Th>Session</Th>
                    <Th>User</Th>
                    <Th>Asset</Th>
                    <Th>Action</Th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {activeSessions.map((item) => (
                    <tr key={item.session_id} className="hover:bg-gray-50">
                      <Td mono>
                        <Link to={`/sessions/${item.session_id}`} className="text-indigo-600 hover:text-indigo-800">
                          {item.session_id.slice(0, 8)}...
                        </Link>
                      </Td>
                      <Td>{item.user.username}</Td>
                      <Td>{item.asset.name}</Td>
                      <Td><Badge color="indigo">{item.action}</Badge></Td>
                    </tr>
                  ))}
                  {activeSessions.length === 0 && <EmptyRow colSpan={4} message="No active sessions right now." />}
                </tbody>
              </Table>
            </Card>
          </div>

          <Card>
            <CardHeader title="Recent Audit Activity">
              <Link to="/admin/audit/events" className="text-sm text-indigo-600 hover:text-indigo-800">Search audit</Link>
            </CardHeader>
            <Table>
              <thead>
                <tr>
                  <Th>ID</Th>
                  <Th>Time</Th>
                  <Th>Event</Th>
                  <Th>Action</Th>
                  <Th>Outcome</Th>
                  <Th>User</Th>
                  <Th>Asset</Th>
                  <Th>Session</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {auditItems.map((item) => (
                  <tr key={item.id} className="hover:bg-gray-50">
                    <Td>
                      <Link to={`/admin/audit/events/${item.id}`} className="text-indigo-600 hover:text-indigo-800">{item.id}</Link>
                    </Td>
                    <Td>{new Date(item.event_time).toLocaleString()}</Td>
                    <Td><Badge>{item.event_type}</Badge></Td>
                    <Td>{item.action || '-'}</Td>
                    <Td>{item.outcome || '-'}</Td>
                    <Td>{item.actor_user?.username || item.actor_user?.id || '-'}</Td>
                    <Td>{item.asset?.name || item.asset?.id || '-'}</Td>
                    <Td mono>
                      {item.session_id ? (
                        <Link to={`/sessions/${item.session_id}`} className="text-indigo-600 hover:text-indigo-800">
                          {item.session_id.slice(0, 8)}...
                        </Link>
                      ) : '-'}
                    </Td>
                  </tr>
                ))}
                {auditItems.length === 0 && <EmptyRow colSpan={8} message="No recent audit events found." />}
              </tbody>
            </Table>
          </Card>
        </div>
      )}
    </>
  )
}
