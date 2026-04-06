import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { adminCreateAsset, adminListAssetGrants, adminListAssets } from "../api"
import { useAuth } from "../auth"
import type { AdminAsset, AdminGrant } from "../types"

const ASSET_TYPES: Array<"linux_vm" | "database" | "redis"> = ["linux_vm", "database", "redis"]

export function AdminAssetsPage() {
  const { user, logout } = useAuth()
  const [items, setItems] = useState<AdminAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selectedAssetID, setSelectedAssetID] = useState<string | null>(null)
  const [assetGrants, setAssetGrants] = useState<AdminGrant[]>([])
  const [grantsLoading, setGrantsLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState("")
  const [assetType, setAssetType] = useState<"linux_vm" | "database" | "redis">("linux_vm")
  const [host, setHost] = useState("")
  const [port, setPort] = useState("22")
  const [metadataText, setMetadataText] = useState("{}")

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await adminListAssets()
      setItems(response.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : "failed to load assets"
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
        if (cancelled === false) {
          setItems(response.items)
        }
      } catch (err) {
        if (cancelled === false) {
          const message = err instanceof Error ? err.message : "failed to load assets"
          setError(message)
        }
      } finally {
        if (cancelled === false) {
          setLoading(false)
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  const inspectAsset = async (assetID: string) => {
    setSelectedAssetID(assetID)
    setGrantsLoading(true)
    setError(null)
    try {
      const response = await adminListAssetGrants(assetID)
      setAssetGrants(response.items)
    } catch (err) {
      const message = err instanceof Error ? err.message : "failed to load asset grants"
      setError(message)
      setAssetGrants([])
    } finally {
      setGrantsLoading(false)
    }
  }

  const createAsset = async () => {
    setError(null)
    let metadata: Record<string, unknown>
    try {
      metadata = JSON.parse(metadataText || "{}") as Record<string, unknown>
    } catch {
      setError("metadata must be valid JSON")
      return
    }
    const parsedPort = Number(port)
    if (!Number.isFinite(parsedPort) || parsedPort <= 0) {
      setError("port must be a valid number")
      return
    }

    setCreating(true)
    try {
      await adminCreateAsset({
        name,
        asset_type: assetType,
        host,
        port: parsedPort,
        metadata,
      })
      setName("")
      setHost("")
      setPort("22")
      setMetadataText("{}")
      await load()
    } catch (err) {
      const message = err instanceof Error ? err.message : "failed to create asset"
      setError(message)
    } finally {
      setCreating(false)
    }
  }

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Admin · Assets</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/admin/dashboard">Dashboard</Link>
          <Link to="/sessions">My Sessions</Link>
          <Link to="/admin/users">Users</Link>
          <Link to="/admin/sessions">Sessions</Link>
          <button onClick={() => void logout()}>Logout</button>
        </div>
      </header>

      {loading ? <p>Loading assets...</p> : null}
      {error === null ? null : <p className="error">{error}</p>}

      <section className="card section-block">
        <h2>Create Asset</h2>
        <div className="form-grid">
          <label>
            Name
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="asset name" />
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
            <input value={host} onChange={(e) => setHost(e.target.value)} placeholder="10.0.0.10" />
          </label>
          <label>
            Port
            <input value={port} onChange={(e) => setPort(e.target.value)} />
          </label>
          <label className="full-width">
            Metadata (JSON object)
            <textarea rows={4} value={metadataText} onChange={(e) => setMetadataText(e.target.value)} />
          </label>
        </div>
        <button onClick={() => void createAsset()} disabled={creating}>
          {creating ? "Creating..." : "Create Asset"}
        </button>
      </section>

      {loading === false && error === null ? (
        <>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                  <th>Endpoint</th>
                  <th>Grant Count</th>
                  <th>Credential Count</th>
                  <th>Detail</th>
                  <th>Inspect</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.id}>
                    <td>{item.name}</td>
                    <td>{item.asset_type}</td>
                    <td>{item.endpoint}</td>
                    <td>{item.grant_count}</td>
                    <td>{item.credential_count}</td>
                    <td>
                      <Link to={`/admin/assets/${item.id}`}>Open</Link>
                    </td>
                    <td>
                      <button onClick={() => void inspectAsset(item.id)}>View grants</button>
                    </td>
                  </tr>
                ))}
                {items.length === 0 ? (
                  <tr>
                    <td colSpan={7} className="muted">
                      No assets found.
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>

          {selectedAssetID === null ? null : (
            <section className="section-block">
              <h2>Asset Grants</h2>
              {grantsLoading ? <p>Loading grants...</p> : null}
              {grantsLoading === false ? (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Subject</th>
                        <th>Subject Type</th>
                        <th>Action</th>
                        <th>Effect</th>
                      </tr>
                    </thead>
                    <tbody>
                      {assetGrants.map((grant) => (
                        <tr key={grant.subject_type + ":" + grant.subject_id + ":" + grant.action}>
                          <td>{grant.subject_name}</td>
                          <td>{grant.subject_type}</td>
                          <td>{grant.action}</td>
                          <td>{grant.effect}</td>
                        </tr>
                      ))}
                      {assetGrants.length === 0 ? (
                        <tr>
                          <td colSpan={4} className="muted">
                            No grants found for this asset.
                          </td>
                        </tr>
                      ) : null}
                    </tbody>
                  </table>
                </div>
              ) : null}
            </section>
          )}
        </>
      ) : null}
    </main>
  )
}
