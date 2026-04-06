import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  adminGetAssetDetail,
  adminListAssetGrants,
  adminUpdateAsset,
  adminUpsertAssetCredential,
} from '../api'
import { useAuth } from '../auth'
import type { AdminAssetDetail, AdminGrant } from '../types'

const CREDENTIAL_TYPES: Array<'password' | 'ssh_key' | 'db_password'> = ['password', 'ssh_key', 'db_password']
const ASSET_TYPES: Array<'linux_vm' | 'database' | 'redis'> = ['linux_vm', 'database', 'redis']

export function AdminAssetDetailPage() {
  const { user, logout } = useAuth()
  const { assetID = '' } = useParams<{ assetID: string }>()
  const [detail, setDetail] = useState<AdminAssetDetail | null>(null)
  const [grants, setGrants] = useState<AdminGrant[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const [name, setName] = useState('')
  const [assetType, setAssetType] = useState<'linux_vm' | 'database' | 'redis'>('linux_vm')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('22')
  const [metadataText, setMetadataText] = useState('{}')

  const [credentialType, setCredentialType] = useState<'password' | 'ssh_key' | 'db_password'>('password')
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
    if (!assetID) {
      return
    }
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
        asset_type: assetType,
        host,
        port: parsedPort,
        metadata,
      })
      setMessage('asset updated')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to update asset')
    } finally {
      setSavingAsset(false)
    }
  }

  const saveCredential = async () => {
    if (!assetID) {
      return
    }
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
      await adminUpsertAssetCredential(assetID, credentialType, {
        username: credentialUsername,
        secret: credentialSecret,
        metadata,
      })
      setCredentialSecret('')
      setMessage('credential updated (secret is write-only and not shown)')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to update credential')
    } finally {
      setSavingCredential(false)
    }
  }

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Admin · Asset Detail</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/admin/dashboard">Dashboard</Link>
          <Link to="/admin/assets">Assets</Link>
          <Link to="/admin/sessions">Sessions</Link>
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      {loading ? <p>Loading asset detail...</p> : null}
      {error ? <p className="error">{error}</p> : null}
      {message ? <p className="status">{message}</p> : null}

      {loading === false && error === null && detail ? (
        <>
          <section className="card section-block">
            <h2>Asset Summary</h2>
            <p><strong>ID:</strong> {detail.id}</p>
            <p><strong>Endpoint:</strong> {detail.endpoint}</p>
          </section>

          <section className="card section-block">
            <h2>Edit Asset</h2>
            <div className="form-grid">
              <label>
                Name
                <input value={name} onChange={(e) => setName(e.target.value)} />
              </label>
              <label>
                Asset Type
                <select value={assetType} onChange={(e) => setAssetType(e.target.value as typeof assetType)}>
                  {ASSET_TYPES.map((item) => (
                    <option key={item} value={item}>{item}</option>
                  ))}
                </select>
              </label>
              <label>
                Host
                <input value={host} onChange={(e) => setHost(e.target.value)} />
              </label>
              <label>
                Port
                <input value={port} onChange={(e) => setPort(e.target.value)} />
              </label>
              <label className="full-width">
                Metadata (JSON object)
                <textarea rows={6} value={metadataText} onChange={(e) => setMetadataText(e.target.value)} />
              </label>
            </div>
            <button onClick={() => void saveAsset()} disabled={savingAsset}>
              {savingAsset ? 'Saving...' : 'Save Asset'}
            </button>
          </section>

          <section className="card section-block">
            <h2>Credential Metadata</h2>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Type</th>
                    <th>Username</th>
                    <th>Algorithm</th>
                    <th>Key ID</th>
                    <th>Rotated</th>
                  </tr>
                </thead>
                <tbody>
                  {detail.credentials.map((item) => (
                    <tr key={item.id}>
                      <td>{item.credential_type}</td>
                      <td>{item.username || '-'}</td>
                      <td>{item.algorithm}</td>
                      <td>{item.key_id}</td>
                      <td>{item.last_rotated_at ? new Date(item.last_rotated_at).toLocaleString() : '-'}</td>
                    </tr>
                  ))}
                  {detail.credentials.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="muted">No credential saved for this asset.</td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>

          <section className="card section-block">
            <h2>Update Credential</h2>
            <p className="muted">Secret values are write-only and are never returned after save.</p>
            <div className="form-grid">
              <label>
                Credential Type
                <select
                  value={credentialType}
                  onChange={(e) => setCredentialType(e.target.value as typeof credentialType)}
                >
                  {CREDENTIAL_TYPES.map((item) => (
                    <option key={item} value={item}>{item}</option>
                  ))}
                </select>
              </label>
              <label>
                Username (optional)
                <input value={credentialUsername} onChange={(e) => setCredentialUsername(e.target.value)} />
              </label>
              <label className="full-width">
                Secret
                <input
                  type="password"
                  value={credentialSecret}
                  onChange={(e) => setCredentialSecret(e.target.value)}
                  placeholder="enter new credential secret"
                />
              </label>
              <label className="full-width">
                Credential Metadata (JSON object)
                <textarea
                  rows={5}
                  value={credentialMetadataText}
                  onChange={(e) => setCredentialMetadataText(e.target.value)}
                />
              </label>
            </div>
            <button onClick={() => void saveCredential()} disabled={savingCredential}>
              {savingCredential ? 'Saving...' : 'Save Credential'}
            </button>
          </section>

          <section className="card section-block">
            <h2>Asset Access Summary</h2>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Subject</th>
                    <th>Type</th>
                    <th>Action</th>
                    <th>Effect</th>
                  </tr>
                </thead>
                <tbody>
                  {grants.map((item) => (
                    <tr key={`${item.subject_type}:${item.subject_id}:${item.action}`}>
                      <td>{item.subject_name}</td>
                      <td>{item.subject_type}</td>
                      <td>{item.action}</td>
                      <td>{item.effect}</td>
                    </tr>
                  ))}
                  {grants.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="muted">No grants for this asset.</td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>
        </>
      ) : null}
    </main>
  )
}
