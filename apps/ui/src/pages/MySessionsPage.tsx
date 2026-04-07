import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getMySessions } from '../api'
import type { SessionSummary } from '../types'
import { Badge, Card, EmptyRow, ErrorState, LoadingState, PageHeader, Select, statusColor, Table, Td, Th } from '../components/ui'

const ACTION_OPTIONS = [
  { value: '', label: 'All actions' },
  { value: 'shell', label: 'Shell' },
  { value: 'dbeaver', label: 'DBeaver' },
  { value: 'sftp', label: 'SFTP' },
  { value: 'redis', label: 'Redis' },
]

const STATUS_OPTIONS = [
  { value: '', label: 'All statuses' },
  { value: 'pending', label: 'Pending' },
  { value: 'active', label: 'Active' },
  { value: 'completed', label: 'Completed' },
  { value: 'failed', label: 'Failed' },
  { value: 'terminated', label: 'Terminated' },
  { value: 'expired', label: 'Expired' },
]

export function MySessionsPage() {
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
  }, [status, action])

  return (
    <>
      <PageHeader title="My Sessions" />

      <Card className="mb-4">
        <div className="flex flex-wrap items-end gap-3 p-4">
          <div className="w-40">
            <Select label="Action" value={action} onChange={setAction} options={ACTION_OPTIONS} />
          </div>
          <div className="w-40">
            <Select label="Status" value={status} onChange={setStatus} options={STATUS_OPTIONS} />
          </div>
        </div>
      </Card>

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {loading && <LoadingState message="Loading sessions..." />}

      {!loading && !error && (
        <Card>
          <Table>
            <thead>
              <tr>
                <Th>Session</Th>
                <Th>Asset</Th>
                <Th>Type</Th>
                <Th>Action</Th>
                <Th>Launch</Th>
                <Th>Status</Th>
                <Th>Created</Th>
                <Th>Duration</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {items.map((item) => (
                <tr key={item.session_id} className="hover:bg-gray-50">
                  <Td mono>
                    <Link to={`/sessions/${item.session_id}`} className="text-indigo-600 hover:text-indigo-800">
                      {item.session_id.slice(0, 8)}...
                    </Link>
                  </Td>
                  <Td className="font-medium text-gray-900">{item.asset.name}</Td>
                  <Td><Badge>{item.asset.asset_type}</Badge></Td>
                  <Td><Badge color="indigo">{item.action}</Badge></Td>
                  <Td>{item.launch_type}</Td>
                  <Td><Badge color={statusColor(item.status)}>{item.status}</Badge></Td>
                  <Td>{new Date(item.created_at).toLocaleString()}</Td>
                  <Td>{item.duration_seconds === undefined ? '-' : `${item.duration_seconds}s`}</Td>
                </tr>
              ))}
              {items.length === 0 && <EmptyRow colSpan={8} message="No sessions found for the selected filters." />}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  )
}
