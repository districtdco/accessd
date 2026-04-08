import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { adminCreateAsset, adminListAssets } from '../api'
import type { AdminAsset } from '../types'
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
  PaginationControls,
  Select,
  Table,
  TabNav,
  Td,
  TextArea,
  Th,
} from '../components/ui'

const ASSET_TYPE_OPTIONS = [
  { value: 'linux_vm', label: 'Linux VM' },
  { value: 'database', label: 'Database' },
  { value: 'redis', label: 'Redis' },
]

const typeFilterOptions = [
  { value: 'all', label: 'All types' },
  ...ASSET_TYPE_OPTIONS,
]

const PAGE_SIZE = 10

export function AdminAssetsPage() {
  const [mode, setMode] = useState<'inventory' | 'single' | 'bulk'>('inventory')
  const [items, setItems] = useState<AdminAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [bulkCreating, setBulkCreating] = useState(false)

  const [query, setQuery] = useState('')
  const [assetTypeFilter, setAssetTypeFilter] = useState('all')
  const [page, setPage] = useState(1)

  const [name, setName] = useState('')
  const [assetType, setAssetType] = useState('linux_vm')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('22')
  const [metadataText, setMetadataText] = useState('{}')
  const [bulkInput, setBulkInput] = useState('')

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await adminListAssets()
      setItems(response.items)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load assets')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  const createAsset = async () => {
    setError(null)
    setMessage(null)
    let metadata: Record<string, unknown>
    try {
      metadata = JSON.parse(metadataText || '{}') as Record<string, unknown>
    } catch {
      setError('metadata must be valid JSON')
      return
    }
    const parsedPort = Number(port)
    if (!Number.isFinite(parsedPort) || parsedPort <= 0) {
      setError('port must be a valid number')
      return
    }

    setCreating(true)
    try {
      await adminCreateAsset({
        name,
        asset_type: assetType as 'linux_vm' | 'database' | 'redis',
        host,
        port: parsedPort,
        metadata,
      })
      setName('')
      setHost('')
      setPort('22')
      setMetadataText('{}')
      setMessage('Asset created')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to create asset')
    } finally {
      setCreating(false)
    }
  }

  const createAssetsBulk = async () => {
    setError(null)
    setMessage(null)
    const lines = bulkInput
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line !== '' && !line.startsWith('#'))
    if (lines.length === 0) {
      setError('Enter at least one server row for bulk create')
      return
    }

    type Parsed = { name: string; asset_type: 'linux_vm' | 'database' | 'redis'; host: string; port: number; metadata: Record<string, unknown> }
    const parsedRows: Parsed[] = []
    for (const line of lines) {
      const parts = line.split('|').map((p) => p.trim())
      if (parts.length < 4) {
        setError(`Invalid row: "${line}"`)
        return
      }
      const parsedPort = Number(parts[3])
      if (!Number.isFinite(parsedPort) || parsedPort <= 0) {
        setError(`Invalid port in row: "${line}"`)
        return
      }
      const type = parts[1]
      if (type !== 'linux_vm' && type !== 'database' && type !== 'redis') {
        setError(`Invalid asset_type in row: "${line}"`)
        return
      }
      let metadata: Record<string, unknown> = {}
      if (parts[4]) {
        try {
          metadata = JSON.parse(parts.slice(4).join('|')) as Record<string, unknown>
        } catch {
          setError(`Invalid metadata JSON in row: "${line}"`)
          return
        }
      }
      parsedRows.push({
        name: parts[0],
        asset_type: type,
        host: parts[2],
        port: parsedPort,
        metadata,
      })
    }

    setBulkCreating(true)
    const results = await Promise.allSettled(parsedRows.map((row) => adminCreateAsset(row)))
    const success = results.filter((r) => r.status === 'fulfilled').length
    const failed = results.length - success
    if (failed === 0) {
      setMessage(`Created ${success} server${success === 1 ? '' : 's'} successfully`)
      setBulkInput('')
    } else {
      setError(`Created ${success}, failed ${failed}. Fix duplicates/invalid rows and retry.`)
    }
    await load()
    setBulkCreating(false)
  }

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    const rows = items.filter((item) => {
      if (assetTypeFilter !== 'all' && item.asset_type !== assetTypeFilter) return false
      if (!q) return true
      return item.name.toLowerCase().includes(q)
        || item.host.toLowerCase().includes(q)
        || item.endpoint.toLowerCase().includes(q)
    })
    rows.sort((a, b) => a.name.localeCompare(b.name))
    return rows
  }, [items, query, assetTypeFilter])

  useEffect(() => {
    setPage(1)
  }, [query, assetTypeFilter])

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const currentPage = Math.min(page, totalPages)
  const paged = filtered.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE)

  return (
    <>
      <PageHeader title="Assets" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {message && <div className="mb-4 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700">{message}</div>}

      <TabNav
        tabs={[
          { id: 'inventory', label: 'Server Inventory' },
          { id: 'single', label: 'Add One Server' },
          { id: 'bulk', label: 'Add Multiple Servers' },
        ]}
        active={mode}
        onChange={(id) => setMode(id as 'inventory' | 'single' | 'bulk')}
      />

      {mode === 'single' && (
        <Card className="mb-4">
          <CardHeader title="Create Server" />
          <CardBody>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <Input label="Name" value={name} onChange={setName} placeholder="asset name" />
              <Select label="Asset Type" value={assetType} onChange={setAssetType} options={ASSET_TYPE_OPTIONS} />
              <Input label="Host" value={host} onChange={setHost} placeholder="10.0.0.10" />
              <Input label="Port" value={port} onChange={setPort} />
            </div>
            <div className="mt-4">
              <TextArea label="Metadata (JSON)" value={metadataText} onChange={setMetadataText} rows={3} />
            </div>
            <div className="mt-4">
              <Button disabled={creating} onClick={() => void createAsset()}>
                {creating ? 'Creating...' : 'Create Server'}
              </Button>
            </div>
          </CardBody>
        </Card>
      )}

      {mode === 'bulk' && (
        <Card className="mb-4">
          <CardHeader title="Bulk Create Servers">
            <span className="text-xs text-gray-500">One row per server: `name|asset_type|host|port|metadata_json(optional)`</span>
          </CardHeader>
          <CardBody>
            <TextArea
              value={bulkInput}
              onChange={setBulkInput}
              rows={10}
              placeholder={`linux-app-01|linux_vm|10.0.0.11|22|{\"env\":\"prod\"}\ndb-main-01|database|10.0.1.12|5432|{\"engine\":\"postgres\"}\ncache-01|redis|10.0.2.13|6379|{\"tls\":true}`}
            />
            <div className="mt-4">
              <Button disabled={bulkCreating} onClick={() => void createAssetsBulk()}>
                {bulkCreating ? 'Creating...' : 'Create All Servers'}
              </Button>
            </div>
          </CardBody>
        </Card>
      )}

      {mode === 'inventory' && (
        <Card className="mb-4">
          <CardHeader title="Browse Assets" />
          <CardBody>
            <div className="grid gap-3 md:grid-cols-2">
              <Input label="Search" value={query} onChange={setQuery} placeholder="name, host, endpoint" />
              <Select label="Type" value={assetTypeFilter} onChange={setAssetTypeFilter} options={typeFilterOptions} />
            </div>
          </CardBody>
        </Card>
      )}

      {loading && <LoadingState message="Loading assets..." />}

      {!loading && !error && mode === 'inventory' && (
        <Card>
          <Table>
            <thead>
              <tr>
                <Th>Name</Th>
                <Th>Type</Th>
                <Th>Endpoint</Th>
                <Th>Grants</Th>
                <Th>Credentials</Th>
                <Th>Detail</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {paged.map((item) => (
                <tr key={item.id} className="hover:bg-gray-50">
                  <Td className="font-medium text-gray-900">{item.name}</Td>
                  <Td><Badge>{item.asset_type}</Badge></Td>
                  <Td mono>{item.endpoint}</Td>
                  <Td>{item.grant_count}</Td>
                  <Td>{item.credential_count}</Td>
                  <Td>
                    <Link to={`/admin/assets/${item.id}`} className="text-indigo-600 hover:text-indigo-800 text-sm font-medium">
                      Open
                    </Link>
                  </Td>
                </tr>
              ))}
              {paged.length === 0 && <EmptyRow colSpan={6} message="No assets match current filters." />}
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
