import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { adminCreateUser, adminListUsers } from '../api'
import type { AdminUser } from '../types'
import { Badge, Button, Card, CardBody, CardHeader, EmptyRow, ErrorState, Input, LoadingState, PageHeader, SuccessState, Table, Td, Th } from '../components/ui'

export function AdminUsersPage() {
  const [items, setItems] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [email, setEmail] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [creating, setCreating] = useState(false)

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await adminListUsers()
      setItems(response.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'failed to load users'
      setError(message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    let cancelled = false
    void (async () => {
      setLoading(true)
      setError(null)
      try {
        const response = await adminListUsers()
        if (!cancelled) setItems(response.items)
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'failed to load users')
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => { cancelled = true }
  }, [])

  const createUser = async () => {
    setError(null)
    setMessage(null)
    if (!username.trim() || !password.trim()) {
      setError('Username and password are required')
      return
    }
    setCreating(true)
    try {
      await adminCreateUser({
        username: username.trim(),
        password,
        email: email.trim() || undefined,
        display_name: displayName.trim() || undefined,
      })
      setUsername('')
      setPassword('')
      setEmail('')
      setDisplayName('')
      setMessage('User created')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to create user')
    } finally {
      setCreating(false)
    }
  }

  return (
    <>
      <PageHeader title="Users" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {message && <div className="mb-4"><SuccessState message={message} /></div>}

      <Card className="mb-4">
        <CardHeader title="Create User" />
        <CardBody>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Input label="Username" value={username} onChange={setUsername} placeholder="jdoe" />
            <Input label="Password" value={password} onChange={setPassword} type="password" placeholder="min 8 characters" />
            <Input label="Email (optional)" value={email} onChange={setEmail} placeholder="jdoe@example.com" />
            <Input label="Display Name (optional)" value={displayName} onChange={setDisplayName} placeholder="Jane Doe" />
          </div>
          <div className="mt-4">
            <Button disabled={creating} onClick={() => void createUser()}>
              {creating ? 'Creating...' : 'Create User'}
            </Button>
          </div>
        </CardBody>
      </Card>

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
