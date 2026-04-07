import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { adminListUsers } from '../api'
import type { AdminUser } from '../types'
import { Badge, Card, EmptyRow, ErrorState, LoadingState, PageHeader, Table, Td, Th } from '../components/ui'

export function AdminUsersPage() {
  const [items, setItems] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)
      try {
        const response = await adminListUsers()
        if (!cancelled) {
          setItems(response.items)
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : 'failed to load users'
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
  }, [])

  return (
    <>
      <PageHeader title="Users" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {loading && <LoadingState message="Loading users..." />}

      {!loading && !error && (
        <Card>
          <Table>
            <thead>
              <tr>
                <Th>Username</Th>
                <Th>Email</Th>
                <Th>Roles</Th>
                <Th>Status</Th>
                <Th>Detail</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {items.map((item) => (
                <tr key={item.id} className="hover:bg-gray-50">
                  <Td className="font-medium text-gray-900">{item.username}</Td>
                  <Td>{item.email || '-'}</Td>
                  <Td>
                    <div className="flex gap-1">
                      {item.roles.length > 0
                        ? item.roles.map((r) => <Badge key={r} color="indigo">{r}</Badge>)
                        : <span className="text-gray-400">-</span>}
                    </div>
                  </Td>
                  <Td>
                    <Badge color={item.is_active ? 'green' : 'red'}>
                      {item.is_active ? 'Active' : 'Inactive'}
                    </Badge>
                  </Td>
                  <Td>
                    <Link to={`/admin/users/${item.id}`} className="text-indigo-600 hover:text-indigo-800 text-sm font-medium">
                      Open
                    </Link>
                  </Td>
                </tr>
              ))}
              {items.length === 0 && <EmptyRow colSpan={5} message="No users found." />}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  )
}
