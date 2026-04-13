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
import { Badge, Button, Card, CardBody, CardHeader, Checkbox, EmptyRow, ErrorState, InfoRow, Input, LoadingState, PageHeader, Select, SuccessState, Table, Td, TextArea, Th } from '../components/ui'

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

function defaultPortForAssetType(type: string): string {
  if (type === 'database') return '5432'
  if (type === 'redis') return '6379'
  return '22'
}

function suggestedDBPort(engine: string): string {
  if (engine === 'mysql' || engine === 'mariadb') return '3306'
  if (engine === 'mssql') return '1433'
  return '5432'
}

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
  const [sftpPath, setSftpPath] = useState('')
  const [dbEngine, setDbEngine] = useState('postgres')
  const [dbName, setDbName] = useState('')
  const [dbSSLMode, setDbSSLMode] = useState('prefer')
  const [redisDBIndex, setRedisDBIndex] = useState('0')
  const [redisTLS, setRedisTLS] = useState(false)
  const [redisInsecureSkipVerifyTLS, setRedisInsecureSkipVerifyTLS] = useState(false)
  const [showAdvancedMetadata, setShowAdvancedMetadata] = useState(false)
  const [advancedMetadataText, setAdvancedMetadataText] = useState('{}')

  const [credentialType, setCredentialType] = useState('password')
  const [credentialUsername, setCredentialUsername] = useState('')
  const [credentialSecret, setCredentialSecret] = useState('')
  const [showCredentialMetadata, setShowCredentialMetadata] = useState(false)
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
      const rawMetadata = { ...(detailResp.metadata ?? {}) }
      const metadata = { ...rawMetadata } as Record<string, unknown>
      if (detailResp.asset_type === 'linux_vm') {
        const path = typeof metadata.path === 'string' ? metadata.path : ''
        setSftpPath(path)
        delete metadata.path
      } else if (detailResp.asset_type === 'database') {
        const engineRaw = typeof metadata.engine === 'string' ? metadata.engine.trim().toLowerCase() : ''
        let normalizedEngine = 'postgres'
        if (engineRaw === 'mysql' || engineRaw === 'mariadb' || engineRaw === 'mssql') normalizedEngine = engineRaw
        if (engineRaw === 'postgresql') normalizedEngine = 'postgres'
        if (engineRaw === 'sqlserver' || engineRaw === 'sql_server') normalizedEngine = 'mssql'
        setDbEngine(normalizedEngine)
        setDbName(typeof metadata.database === 'string' ? metadata.database : '')
        const sslMode = typeof metadata.ssl_mode === 'string' && metadata.ssl_mode.trim() ? metadata.ssl_mode : (normalizedEngine === 'mssql' ? 'disable' : 'prefer')
        setDbSSLMode(sslMode)
        delete metadata.engine
        delete metadata.database
        delete metadata.ssl_mode
      } else if (detailResp.asset_type === 'redis') {
        const dbRaw = metadata.database
        const dbNum = typeof dbRaw === 'number' ? dbRaw : Number(dbRaw ?? 0)
        const safeDB = Number.isInteger(dbNum) && dbNum >= 0 ? dbNum : 0
        setRedisDBIndex(String(safeDB))
        const tls = Boolean(metadata.tls)
        setRedisTLS(tls)
        setRedisInsecureSkipVerifyTLS(tls && Boolean(metadata.insecure_skip_verify_tls))
        delete metadata.database
        delete metadata.tls
        delete metadata.insecure_skip_verify_tls
      }
      const hasAdvanced = Object.keys(metadata).length > 0
      setAdvancedMetadataText(JSON.stringify(metadata, null, 2))
      setShowAdvancedMetadata(hasAdvanced)
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
    const parsedPort = Number(port)
    if (!Number.isFinite(parsedPort) || parsedPort <= 0) {
      setError('port must be a valid number')
      return
    }
    let metadata: Record<string, unknown> = {}
    if (assetType === 'linux_vm') {
      const trimmedPath = sftpPath.trim()
      if (trimmedPath) metadata.path = trimmedPath
    } else if (assetType === 'database') {
      const trimmedDB = dbName.trim()
      metadata = {
        engine: dbEngine,
        ssl_mode: dbSSLMode,
      }
      if (trimmedDB) metadata.database = trimmedDB
    } else if (assetType === 'redis') {
      const parsedRedisDB = Number(redisDBIndex)
      if (!Number.isInteger(parsedRedisDB) || parsedRedisDB < 0) {
        setError('redis database index must be 0 or greater')
        return
      }
      metadata = {
        database: parsedRedisDB,
        tls: redisTLS,
      }
      if (redisTLS) metadata.insecure_skip_verify_tls = redisInsecureSkipVerifyTLS
    }
    let advanced: Record<string, unknown>
    try {
      advanced = JSON.parse(advancedMetadataText || '{}') as Record<string, unknown>
    } catch {
      setError('advanced metadata must be valid JSON')
      return
    }
    metadata = { ...metadata, ...advanced }

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
                <Select
                  label="Asset Type"
                  value={assetType}
                  onChange={(next) => {
                    setAssetType(next)
                    setPort(defaultPortForAssetType(next))
                  }}
                  options={ASSET_TYPE_OPTIONS}
                />
                <Input label="Host" value={host} onChange={setHost} />
                <Input label="Port" value={port} onChange={setPort} />
              </div>

              {assetType === 'linux_vm' && (
                <div className="mt-4 grid gap-4 sm:grid-cols-2">
                  <Input label="SFTP Start Path (optional)" value={sftpPath} onChange={setSftpPath} placeholder="/home/ops" />
                  <div className="rounded-lg border border-indigo-100 bg-indigo-50 px-3 py-2 text-xs text-indigo-700">
                    Suggested: leave empty for default home directory, or set a fixed path like `/var/www`.
                  </div>
                </div>
              )}

              {assetType === 'database' && (
                <div className="mt-4 grid gap-4 sm:grid-cols-3">
                  <Select
                    label="Database Engine"
                    value={dbEngine}
                    onChange={(next) => {
                      setDbEngine(next)
                      setPort(suggestedDBPort(next))
                    }}
                    options={[
                      { value: 'postgres', label: 'PostgreSQL (recommended)' },
                      { value: 'mysql', label: 'MySQL' },
                      { value: 'mariadb', label: 'MariaDB' },
                      { value: 'mssql', label: 'SQL Server' },
                    ]}
                  />
                  <Input label="Database Name (optional)" value={dbName} onChange={setDbName} placeholder="appdb" />
                  <Select
                    label="SSL Mode"
                    value={dbSSLMode}
                    onChange={setDbSSLMode}
                    options={[
                      { value: 'prefer', label: 'prefer (recommended)' },
                      { value: 'require', label: 'require' },
                      { value: 'verify-ca', label: 'verify-ca' },
                      { value: 'verify-full', label: 'verify-full' },
                      { value: 'disable', label: 'disable' },
                    ]}
                  />
                </div>
              )}

              {assetType === 'redis' && (
                <div className="mt-4 grid gap-4 sm:grid-cols-2">
                  <Input label="Redis Database Index" value={redisDBIndex} onChange={setRedisDBIndex} placeholder="0" />
                  <div className="space-y-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2">
                    <Checkbox
                      label="Use TLS"
                      checked={redisTLS}
                      onChange={(checked) => {
                        setRedisTLS(checked)
                        if (!checked) setRedisInsecureSkipVerifyTLS(false)
                      }}
                    />
                    <Checkbox
                      label="Skip TLS certificate verification"
                      hint="Use only for internal/self-signed test environments."
                      checked={redisInsecureSkipVerifyTLS}
                      onChange={setRedisInsecureSkipVerifyTLS}
                      disabled={!redisTLS}
                    />
                  </div>
                </div>
              )}

              <div className="mt-4 space-y-2">
                <Checkbox
                  label="Show advanced metadata JSON (optional)"
                  hint="Only needed for custom fields not covered above."
                  checked={showAdvancedMetadata}
                  onChange={setShowAdvancedMetadata}
                />
                {showAdvancedMetadata && (
                  <TextArea
                    label="Advanced Metadata (JSON)"
                    value={advancedMetadataText}
                    onChange={setAdvancedMetadataText}
                    rows={5}
                    placeholder='{"team":"it-ops"}'
                  />
                )}
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
                <Checkbox
                  label="Show credential metadata JSON (optional)"
                  hint="Keep this empty unless a custom integration needs it."
                  checked={showCredentialMetadata}
                  onChange={setShowCredentialMetadata}
                />
              </div>
              {showCredentialMetadata && (
                <div className="mt-4">
                  <TextArea label="Credential Metadata (JSON)" value={credentialMetadataText} onChange={setCredentialMetadataText} rows={4} />
                </div>
              )}
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
