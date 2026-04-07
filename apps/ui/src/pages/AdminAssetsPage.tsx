import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { adminCreateAsset, adminListAssetGrants, adminListAssets } from '../api'
import type { AdminAsset, AdminGrant } from '../types'
import { Badge, Button, Card, CardBody, CardHeader, EmptyRow, ErrorState, Input, LoadingState, PageHeader, Select, Table, Td, TextArea, Th } from '../components/ui'

const ASSET_TYPE_OPTIONS = [
  { value: 'linux_vm', label: 'Linux VM' },
  { value: 'database', label: 'Database' },
  { value: 'redis', label: 'Redis' },
]

export function AdminAssetsPage() {
  const [items, setItems] = useState<AdminAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedAssetID, setSelectedAssetID] = useState<string | null>(null)
  const [assetGrants, setAssetGrants] = useState<AdminGrant[]>([])
  const [grantsLoading, setGrantsLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [assetType, setAssetType] = useState('linux_vm')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('22')
  const [metadataText, setMetadataText] = useState('{}')

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await adminListAssets()
      setItems(response.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : 'failed to load assets'
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
        const response = await adminListAssets()
        if (!cancelled) setItems(response.items)
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'failed to load assets')
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => { cancelled = true }
  }, [])

  const inspectAsset = async (assetID: string) => {
    setSelectedAssetID(assetID)
    setGrantsLoading(true)
    setError(null)
    try {
      const response = await adminListAssetGrants(assetID)
      setAssetGrants(response.items)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load asset grants')
      setAssetGrants([])
    } finally {
      setGrantsLoading(false)
    }
  }

  const createAsset = async () => {
    setError(null)
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
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to create asset')
    } finally {
      setCreating(false)
    }
  }

  return (
    <>
      <PageHeader title="Assets" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}

      <Card className="mb-4">
        <CardHeader title="Create Asset" />
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
              {creating ? 'Creating...' : 'Create Asset'}
            </Button>
          </div>
        </CardBody>
      </Card>

      {loading && <LoadingState message="Loading assets..." />}

      {!loading && !error && (
        <>
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
                  <Th>Inspect</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {items.map((item) => (
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
                    <Td>
                      <Button size="sm" variant="ghost" onClick={() => void inspectAsset(item.id)}>
                        View grants
                      </Button>
                    </Td>
                  </tr>
                ))}
                {items.length === 0 && <EmptyRow colSpan={7} message="No assets found." />}
              </tbody>
            </Table>
          </Card>

          {selectedAssetID !== null && (
            <Card className="mt-4">
              <CardHeader title="Asset Grants" />
              {grantsLoading ? (
                <LoadingState message="Loading grants..." />
              ) : (
                <Table>
                  <thead>
                    <tr>
                      <Th>Subject</Th>
                      <Th>Subject Type</Th>
                      <Th>Action</Th>
                      <Th>Effect</Th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {assetGrants.map((grant) => (
                      <tr key={grant.subject_type + ':' + grant.subject_id + ':' + grant.action} className="hover:bg-gray-50">
                        <Td className="font-medium text-gray-900">{grant.subject_name}</Td>
                        <Td><Badge>{grant.subject_type}</Badge></Td>
                        <Td><Badge color="indigo">{grant.action}</Badge></Td>
                        <Td><Badge color={grant.effect === 'allow' ? 'green' : 'red'}>{grant.effect}</Badge></Td>
                      </tr>
                    ))}
                    {assetGrants.length === 0 && <EmptyRow colSpan={4} message="No grants found for this asset." />}
                  </tbody>
                </Table>
              )}
            </Card>
          )}
        </>
      )}
    </>
  )
}
