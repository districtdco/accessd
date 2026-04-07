import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  adminDeleteAsset,
  adminGetAssetDetail,
  adminListAssetGrants,
  adminUpdateAsset,
  adminUpsertAssetCredential,
} from '../api'
import type { AdminAssetDetail, AdminGrant } from '../types'
import { Badge, Button, Card, CardBody, CardHeader, EmptyRow, ErrorState, InfoRow, Input, LoadingState, PageHeader, Select, SuccessState, Table, Td, TextArea, Th } from '../components/ui'

const CREDENTIAL_TYPE_OPTIONS = [
  { value: 'password', label: 'Password' },
  { value: 'ssh_key', label: 'SSH Key' },
  { value: 'db_password', label: 'DB Password' },
]

const ASSET_TYPE_OPTIONS = [
  { value: 'linux_vm', label: 'Linux VM' },
  { value: 'database', label: 'Database' },
  { value: 'redis', label: 'Redis' },
]

export function AdminAssetDetailPage() {
  const { assetID = '' } = useParams<{ assetID: string }>()
  const navigate = useNavigate()
  const [detail, setDetail] = useState<AdminAssetDetail | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [grants, setGrants] = useState<AdminGrant[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const [name, setName] = useState('')
  const [assetType, setAssetType] = useState('linux_vm')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('22')
  const [metadataText, setMetadataText] = useState('{}')

  const [credentialType, setCredentialType] = useState('password')
  const [credentialUsername, setCredentialUsername] = useState('')
  const [credentialSecret, setCredentialSecret] = useState('')
  const [credentialMetadataText, setCredentialMetadataText] = useState('{}')
  const [savingAsset, setSavingAsset] = useState(false)
  const [savingCredential, setSavingCredential] = useState(false)

  const load = async () => {
    if (!assetID) {
      setError('missing asset id')
      return
    }
    setLoading(true)
    setError(null)
    try {
      const [detailResp, grantsResp] = await Promise.all([
        adminGetAssetDetail(assetID),
        adminListAssetGrants(assetID),
      ])
      setDetail(detailResp)
      setGrants(grantsResp.items)
      setName(detailResp.name)
      setAssetType(detailResp.asset_type)
      setHost(detailResp.host)
      setPort(String(detailResp.port))
      setMetadataText(JSON.stringify(detailResp.metadata ?? {}, null, 2))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load asset detail')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [assetID])

  const saveAsset = async () => {
    if (!assetID) return
    setMessage(null)
    setError(null)
    let metadata: Record<string, unknown>
    try {
      metadata = JSON.parse(metadataText || '{}') as Record<string, unknown>
    } catch {
      setError('asset metadata must be valid JSON')
      return
    }
    const parsedPort = Number(port)
    if (!Number.isFinite(parsedPort) || parsedPort <= 0) {
      setError('port must be a valid number')
      return
    }

    setSavingAsset(true)
    try {
      await adminUpdateAsset(assetID, {
        name,
        asset_type: assetType as 'linux_vm' | 'database' | 'redis',
        host,
        port: parsedPort,
        metadata,
      })
      setMessage('Asset updated')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to update asset')
    } finally {
      setSavingAsset(false)
    }
  }

  const deleteAsset = async () => {
    if (!assetID || !detail) return
    if (!window.confirm(`Delete asset "${detail.name}"? This will also remove all grants and credentials.`)) return
    setDeleting(true)
    setError(null)
    try {
      await adminDeleteAsset(assetID)
      navigate('/admin/assets')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to delete asset')
      setDeleting(false)
    }
  }

  const saveCredential = async () => {
    if (!assetID) return
    setMessage(null)
    setError(null)
    let metadata: Record<string, unknown>
    try {
      metadata = JSON.parse(credentialMetadataText || '{}') as Record<string, unknown>
    } catch {
      setError('credential metadata must be valid JSON')
      return
    }

    setSavingCredential(true)
    try {
      await adminUpsertAssetCredential(assetID, credentialType as 'password' | 'ssh_key' | 'db_password', {
        username: credentialUsername,
        secret: credentialSecret,
        metadata,
      })
      setCredentialSecret('')
      setMessage('Credential updated (secret is write-only and not shown)')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to update credential')
    } finally {
      setSavingCredential(false)
    }
  }

  return (
    <>
      <div className="mb-2 flex items-center gap-2 text-sm text-gray-500">
        <Link to="/admin/assets" className="hover:text-gray-700">Assets</Link>
        <span>/</span>
        <span className="text-gray-700">{detail?.name || assetID || 'detail'}</span>
      </div>
      <PageHeader title="Asset Detail" />

      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {message && <div className="mb-4"><SuccessState message={message} /></div>}
      {loading && <LoadingState message="Loading asset detail..." />}

      {!loading && !error && detail && (
        <div className="space-y-4">
          <Card>
            <CardHeader title="Asset Summary" />
            <CardBody>
              <div className="grid gap-x-8 gap-y-1 sm:grid-cols-2 mb-4">
                <InfoRow label="ID" value={<span className="font-mono text-xs">{detail.id}</span>} />
                <InfoRow label="Endpoint" value={<span className="font-mono text-xs">{detail.endpoint}</span>} />
              </div>
              <Button variant="danger" disabled={deleting} onClick={() => void deleteAsset()}>
                {deleting ? 'Deleting...' : 'Delete Asset'}
              </Button>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Edit Asset" />
            <CardBody>
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <Input label="Name" value={name} onChange={setName} />
                <Select label="Asset Type" value={assetType} onChange={setAssetType} options={ASSET_TYPE_OPTIONS} />
                <Input label="Host" value={host} onChange={setHost} />
                <Input label="Port" value={port} onChange={setPort} />
              </div>
              <div className="mt-4">
                <TextArea label="Metadata (JSON)" value={metadataText} onChange={setMetadataText} rows={5} />
              </div>
              <div className="mt-4">
                <Button disabled={savingAsset} onClick={() => void saveAsset()}>
                  {savingAsset ? 'Saving...' : 'Save Asset'}
                </Button>
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Credential Metadata" />
            <Table>
              <thead>
                <tr>
                  <Th>Type</Th>
                  <Th>Username</Th>
                  <Th>Algorithm</Th>
                  <Th>Key ID</Th>
                  <Th>Rotated</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {detail.credentials.map((item) => (
                  <tr key={item.id} className="hover:bg-gray-50">
                    <Td><Badge>{item.credential_type}</Badge></Td>
                    <Td>{item.username || '-'}</Td>
                    <Td mono>{item.algorithm}</Td>
                    <Td mono>{item.key_id}</Td>
                    <Td>{item.last_rotated_at ? new Date(item.last_rotated_at).toLocaleString() : '-'}</Td>
                  </tr>
                ))}
                {detail.credentials.length === 0 && <EmptyRow colSpan={5} message="No credential saved for this asset." />}
              </tbody>
            </Table>
          </Card>

          <Card>
            <CardHeader title="Update Credential">
              <span className="text-xs text-gray-400">Secret values are write-only</span>
            </CardHeader>
            <CardBody>
              <div className="grid gap-4 sm:grid-cols-2">
                <Select label="Credential Type" value={credentialType} onChange={setCredentialType} options={CREDENTIAL_TYPE_OPTIONS} />
                <Input label="Username (optional)" value={credentialUsername} onChange={setCredentialUsername} />
              </div>
              <div className="mt-4">
                <Input label="Secret" value={credentialSecret} onChange={setCredentialSecret} type="password" placeholder="enter new credential secret" />
              </div>
              <div className="mt-4">
                <TextArea label="Credential Metadata (JSON)" value={credentialMetadataText} onChange={setCredentialMetadataText} rows={4} />
              </div>
              <div className="mt-4">
                <Button disabled={savingCredential} onClick={() => void saveCredential()}>
                  {savingCredential ? 'Saving...' : 'Save Credential'}
                </Button>
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Asset Access Summary" />
            <Table>
              <thead>
                <tr>
                  <Th>Subject</Th>
                  <Th>Type</Th>
                  <Th>Action</Th>
                  <Th>Effect</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {grants.map((item) => (
                  <tr key={`${item.subject_type}:${item.subject_id}:${item.action}`} className="hover:bg-gray-50">
                    <Td className="font-medium text-gray-900">{item.subject_name}</Td>
                    <Td><Badge>{item.subject_type}</Badge></Td>
                    <Td><Badge color="indigo">{item.action}</Badge></Td>
                    <Td><Badge color={item.effect === 'allow' ? 'green' : 'red'}>{item.effect}</Badge></Td>
                  </tr>
                ))}
                {grants.length === 0 && <EmptyRow colSpan={4} message="No grants for this asset." />}
              </tbody>
            </Table>
          </Card>
        </div>
      )}
    </>
  )
}
