import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { getAdminAuditEvents } from '../api'
import { useAuth } from '../auth'
import type { AdminAuditItem } from '../types'
import { Badge, Button, Card, CardBody, CardHeader, EmptyRow, ErrorState, Input, LoadingState, PageHeader, PaginationControls, Select, Table, Td, Th } from '../components/ui'

// Format protocol-aware audit action labels for display
function formatAuditAction(action: string, metadata?: Record<string, any>): string {
  // Check for protocol-aware metadata first
  if (metadata?.protocol_action) {
    return formatProtocolAction(metadata.protocol_action, metadata)
  }

  // Fallback to direct action formatting
  return formatProtocolAction(action, metadata)
}

function formatProtocolAction(action: string, metadata?: Record<string, any>): string {
  const mappings: Record<string, string> = {
    // Session lifecycle
    'shell_session': 'Shell Session',
    'shell_session_end': 'Shell Session Ended',
    'sftp_session': 'SFTP Session',
    'sftp_session_end': 'SFTP Session Ended',
    'database_session': 'Database Session',
    'database_session_end': 'Database Session Ended',
    'redis_session': 'Redis Session',
    'redis_session_end': 'Redis Session Ended',
    'session_start': 'Session Start',
    'session_end': 'Session End',
    // Upstream auth (protocol-aware)
    'ssh_upstream_auth': 'Shell Upstream Auth',
    'sftp_upstream_auth': 'SFTP Upstream Auth',
    'database_upstream_auth': 'DB Upstream Auth',
    'redis_upstream_auth': 'Redis Upstream Auth',
    'upstream_auth': 'Upstream Auth',
    // Credential usage (protocol-aware)
    'shell_proxy_upstream_auth': 'Shell Upstream Auth',
    'sftp_proxy_upstream_auth': 'SFTP Upstream Auth',
    'database_proxy_upstream_auth': 'DB Upstream Auth',
    'redis_proxy_upstream_auth': 'Redis Upstream Auth',
    'shell_launch_prepare': 'Shell Launch Prepare',
    'sftp_launch_prepare': 'SFTP Launch Prepare',
    'database_launch_prepare': 'DB Launch Prepare',
    'redis_launch_prepare': 'Redis Launch Prepare',
    'credential_usage': 'Credential Usage',
    // Action end labels
    'shell_end': 'Shell Session Ended',
    'sftp_end': 'SFTP Session Ended',
    'dbeaver_end': 'DB Session Ended',
    'redis_end': 'Redis Session Ended',
  }

  const base = mappings[action] || action.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
  const asset = typeof metadata?.target_asset_name === 'string' ? metadata.target_asset_name : ''
  const upstream = typeof metadata?.upstream_username === 'string' ? metadata.upstream_username : ''
  const proto = typeof metadata?.protocol === 'string' ? metadata.protocol : ''
  if (!asset && !upstream) {
    return base
  }
  let suffix = ''
  if (asset) {
    suffix = ` to ${asset}`
  }
  if (upstream) {
    suffix += `${asset ? ' as' : ' as'} ${upstream}`
  }
  if (proto) {
    suffix += ` (${proto})`
  }
  return `${base}${suffix}`
}

const LIMIT_OPTIONS = [
  { value: '25', label: '25' },
  { value: '50', label: '50' },
  { value: '100', label: '100' },
  { value: '200', label: '200' },
]

export function AdminAuditEventsPage() {
  const { user } = useAuth()
  const isAdmin = user?.roles.includes('admin') === true
  const [items, setItems] = useState<AdminAuditItem[]>([])
  const [eventType, setEventType] = useState('')
  const [action, setAction] = useState('')
  const [userID, setUserID] = useState('')
  const [assetID, setAssetID] = useState('')
  const [sessionID, setSessionID] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [limit, setLimit] = useState('100')
  const [orderBy, setOrderBy] = useState<'time' | 'id'>('time')
  const [orderDir, setOrderDir] = useState<'desc' | 'asc'>('desc')
  const [page, setPage] = useState(1)
  const pageSize = 20
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
        limit: Number(limit),
      })
      setItems(response.items)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load audit events')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  const orderedItems = useMemo(() => {
    const rows = [...items]
    rows.sort((a, b) => {
      if (orderBy === 'id') {
        return a.id - b.id
      }
      return new Date(a.event_time).getTime() - new Date(b.event_time).getTime()
    })
    if (orderDir === 'desc') {
      rows.reverse()
    }
    return rows
  }, [items, orderBy, orderDir])

  useEffect(() => {
    setPage(1)
  }, [eventType, action, userID, assetID, sessionID, from, to, limit, orderBy, orderDir, items.length])

  const totalPages = Math.max(1, Math.ceil(orderedItems.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const pagedItems = useMemo(() => {
    return orderedItems.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  }, [orderedItems, currentPage])

  return (
    <>
      <PageHeader title="Audit Events" />

      <Card className="mb-4">
        <CardHeader title="Filters" />
        <CardBody>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Input label="Event Type" value={eventType} onChange={setEventType} placeholder="session_start" />
            <Input label="Action" value={action} onChange={setAction} placeholder="shell_end" />
            <Input label="User ID" value={userID} onChange={setUserID} placeholder="actor user id" />
            <Input label="Asset ID" value={assetID} onChange={setAssetID} placeholder="asset id" />
            <Input label="Session ID" value={sessionID} onChange={setSessionID} placeholder="session id" />
            <Select label="Limit" value={limit} onChange={setLimit} options={LIMIT_OPTIONS} />
            <Select
              label="Order By"
              value={orderBy}
              onChange={(v) => setOrderBy(v as 'time' | 'id')}
              options={[
                { value: 'time', label: 'Time' },
                { value: 'id', label: 'Event ID' },
              ]}
            />
            <Select
              label="Direction"
              value={orderDir}
              onChange={(v) => setOrderDir(v as 'desc' | 'asc')}
              options={[
                { value: 'desc', label: 'Newest first' },
                { value: 'asc', label: 'Oldest first' },
              ]}
            />
            <label className="block">
              <span className="mb-1 block text-sm font-medium text-gray-700">From</span>
              <input
                type="datetime-local"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
                className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              />
            </label>
            <label className="block">
              <span className="mb-1 block text-sm font-medium text-gray-700">To</span>
              <input
                type="datetime-local"
                value={to}
                onChange={(e) => setTo(e.target.value)}
                className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              />
            </label>
          </div>
          <div className="mt-4">
            <Button disabled={loading} onClick={() => void load()}>
              {loading ? 'Searching...' : 'Search'}
            </Button>
          </div>
        </CardBody>
      </Card>

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {loading && <LoadingState message="Loading audit events..." />}

      {!loading && !error && (
        <Card>
          <Table>
            <thead>
              <tr>
                <Th>ID</Th>
                <Th>Time</Th>
                <Th>Type</Th>
                <Th>Action</Th>
                <Th>Outcome</Th>
                <Th>Actor</Th>
                <Th>Asset</Th>
                <Th>Session</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {pagedItems.map((item) => (
                <tr key={item.id} className="hover:bg-gray-50">
                  <Td>
                    <Link to={`/admin/audit/events/${item.id}`} className="text-indigo-600 hover:text-indigo-800">{item.id}</Link>
                  </Td>
                  <Td>{new Date(item.event_time).toLocaleString()}</Td>
                  <Td><Badge>{item.event_type}</Badge></Td>
                  <Td>{formatAuditAction(item.action || '', item.metadata) || '-'}</Td>
                  <Td>{item.outcome || '-'}</Td>
                  <Td>
                    {item.actor_user?.id && isAdmin ? (
                      <Link to={`/admin/users/${item.actor_user.id}`} className="text-indigo-600 hover:text-indigo-800">
                        {item.actor_user.username || item.actor_user.id}
                      </Link>
                    ) : (item.actor_user?.username || item.actor_user?.id || '-')}
                  </Td>
                  <Td>
                    {item.asset?.id && isAdmin ? (
                      <Link to={`/admin/assets/${item.asset.id}`} className="text-indigo-600 hover:text-indigo-800">
                        {item.asset.name || item.asset.id}
                      </Link>
                    ) : (item.asset?.name || item.asset?.id || '-')}
                  </Td>
                  <Td mono>
                    {item.session_id ? (
                      <Link to={`/sessions/${item.session_id}`} className="text-indigo-600 hover:text-indigo-800">
                        {item.session_id.slice(0, 8)}...
                      </Link>
                    ) : '-'}
                  </Td>
                </tr>
              ))}
              {pagedItems.length === 0 && <EmptyRow colSpan={8} message="No audit events found for current filters." />}
            </tbody>
          </Table>
          <PaginationControls
            page={currentPage}
            totalPages={totalPages}
            totalItems={orderedItems.length}
            pageSize={pageSize}
            onPageChange={setPage}
          />
        </Card>
      )}
    </>
  )
}
