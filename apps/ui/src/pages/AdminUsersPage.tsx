import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { adminCreateUser, adminListUsers } from '../api'
import type { AdminUser } from '../types'
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyRow,
  ErrorState,
  Input,
  LoadingState,
  PageHeader,
  Select,
  SuccessState,
  Table,
  TabNav,
  Td,
  Th,
  PaginationControls,
} from '../components/ui'

const PAGE_SIZE = 10

const statusFilterOptions = [
  { value: 'all', label: 'All statuses' },
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
]

const providerFilterOptions = [
  { value: 'all', label: 'All providers' },
  { value: 'local', label: 'Local' },
  { value: 'ldap', label: 'LDAP' },
]

export function AdminUsersPage() {
  const [mode, setMode] = useState<'directory' | 'create'>('directory')
  const [items, setItems] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [providerFilter, setProviderFilter] = useState('all')
  const [page, setPage] = useState(1)

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
      setError(err instanceof Error ? err.message : 'failed to load users')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
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

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    const rows = items.filter((item) => {
      if (statusFilter === 'active' && !item.is_active) return false
      if (statusFilter === 'inactive' && item.is_active) return false
      if (providerFilter !== 'all' && item.auth_provider !== providerFilter) return false
      if (!q) return true
      return item.username.toLowerCase().includes(q)
        || (item.email || '').toLowerCase().includes(q)
        || (item.display_name || '').toLowerCase().includes(q)
    })
    rows.sort((a, b) => a.username.localeCompare(b.username))
    return rows
  }, [items, query, statusFilter, providerFilter])

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const currentPage = Math.min(page, totalPages)
  const paged = filtered.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE)

  useEffect(() => {
    setPage(1)
  }, [query, statusFilter, providerFilter])

  return (
    <>
      <PageHeader title="Users" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {message && <div className="mb-4"><SuccessState message={message} /></div>}

      <TabNav
        tabs={[
          { id: 'directory', label: 'User Directory' },
          { id: 'create', label: 'Create User' },
        ]}
        active={mode}
        onChange={(id) => setMode(id as 'directory' | 'create')}
      />

      {mode === 'create' && (
        <Card className="mb-4">
          <CardHeader title="Create Local User" />
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
      )}

      {mode === 'directory' && (
        <Card className="mb-4">
          <CardHeader title="Browse Users" />
          <CardBody>
            <div className="grid gap-3 md:grid-cols-3">
              <Input label="Search" value={query} onChange={setQuery} placeholder="username, email, display name" />
              <Select label="Status" value={statusFilter} onChange={setStatusFilter} options={statusFilterOptions} />
              <Select label="Provider" value={providerFilter} onChange={setProviderFilter} options={providerFilterOptions} />
            </div>
          </CardBody>
        </Card>
      )}

      {loading && <LoadingState message="Loading users..." />}

      {!loading && !error && mode === 'directory' && (
        <Card>
          <Table>
            <thead>
              <tr>
                <Th>Username</Th>
                <Th>Email</Th>
                <Th>Provider</Th>
                <Th>Roles</Th>
                <Th>Status</Th>
                <Th>Detail</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {paged.map((item) => (
                <tr key={item.id} className="hover:bg-gray-50">
                  <Td className="font-medium text-gray-900">{item.username}</Td>
                  <Td>{item.email || '-'}</Td>
                  <Td><Badge color={item.auth_provider === 'ldap' ? 'blue' : 'gray'}>{item.auth_provider}</Badge></Td>
                  <Td>
                    <div className="flex flex-wrap gap-1">
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
              {paged.length === 0 && <EmptyRow colSpan={6} message="No users match current filters." />}
            </tbody>
          </Table>
          <PaginationControls
            page={currentPage}
            totalPages={totalPages}
            totalItems={filtered.length}
            pageSize={PAGE_SIZE}
            onPageChange={setPage}
          />
        </Card>
      )}
    </>
  )
}
