import { useEffect, useState } from 'react'
import {
  createSessionLaunch,
  getMyAccess,
  handoffDBeaverToConnector,
  handoffRedisToConnector,
  handoffSFTPToConnector,
  handoffShellToConnector,
  recordSessionEvent,
} from '../api'
import { useAuth } from '../auth'
import type { AccessPoint, DBeaverLaunchConnection, RedisLaunchConnection, SFTPLaunchConnection, ShellLaunchConnection } from '../types'

export function AccessPage() {
  const { user, logout } = useAuth()
  const canReadAdmin = user?.roles.includes('admin') || user?.roles.includes('auditor')
  const [items, setItems] = useState<AccessPoint[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [launchingAssetID, setLaunchingAssetID] = useState<string | null>(null)
  const [launchMessage, setLaunchMessage] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)
      try {
        const response = await getMyAccess()
        if (!cancelled) {
          setItems(response.items)
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : 'failed to load access'
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

  const launchAsset = async (item: AccessPoint, action: 'shell' | 'sftp' | 'dbeaver' | 'redis') => {
    setLaunchMessage(null)
    setLaunchingAssetID(item.asset_id)
    let sessionID: string | null = null

    try {
      const session = await createSessionLaunch({
        asset_id: item.asset_id,
        action,
      })
      sessionID = session.session_id

      await recordSessionEvent(session.session_id, 'connector_launch_requested', {
        connector_action: session.launch_type,
      })

      const successMetadata: Record<string, unknown> = {
        connector_action: session.launch_type,
      }

      if (session.launch_type === 'shell') {
        const result = await handoffShellToConnector({
          session_id: session.session_id,
          asset_id: item.asset_id,
          asset_name: item.asset_name,
          launch: session.launch as ShellLaunchConnection,
        })
        successMetadata.token_copied = result.tokenCopied
        if (result.hint) {
          successMetadata.instructions = result.hint
        }
        setLaunchMessage(
          result.tokenCopied
            ? `Shell launch started for ${item.asset_name}. Launch token copied to clipboard; paste it at the SSH prompt.`
            : `Shell launch started for ${item.asset_name}. Paste the launch token shown in your terminal when prompted.`,
        )
      } else if (session.launch_type === 'sftp') {
        const result = await handoffSFTPToConnector({
          session_id: session.session_id,
          asset_id: item.asset_id,
          asset_name: item.asset_name,
          launch: session.launch as SFTPLaunchConnection,
        })
        if (result.hint) {
          successMetadata.instructions = result.hint
        }
        if (result.diagnostics) {
          successMetadata.diagnostics = result.diagnostics
        }
        setLaunchMessage(`SFTP launch requested for ${item.asset_name}.`)
      } else if (session.launch_type === 'dbeaver') {
        const result = await handoffDBeaverToConnector({
          session_id: session.session_id,
          asset_id: item.asset_id,
          asset_name: item.asset_name,
          launch: session.launch as DBeaverLaunchConnection,
        })
        if (result.hint) {
          successMetadata.instructions = result.hint
        }
        if (result.diagnostics) {
          successMetadata.diagnostics = result.diagnostics
        }
        const cleanupSeconds = Number((result.diagnostics?.cleanup_after_seconds as number | undefined) ?? 0)
        if (cleanupSeconds > 0) {
          const mins = Math.ceil(cleanupSeconds / 60)
          setLaunchMessage(`DBeaver launch requested for ${item.asset_name}. Local temp material will auto-clean in about ${mins} minute(s).`)
        } else {
          setLaunchMessage(`DBeaver launch requested for ${item.asset_name}.`)
        }
      } else {
        const result = await handoffRedisToConnector({
          session_id: session.session_id,
          asset_id: item.asset_id,
          asset_name: item.asset_name,
          launch: session.launch as RedisLaunchConnection,
        })
        if (result.hint) {
          successMetadata.instructions = result.hint
        }
        if (result.diagnostics) {
          successMetadata.diagnostics = result.diagnostics
        }
        setLaunchMessage(`Redis CLI launch requested for ${item.asset_name}.`)
      }

      try {
        await recordSessionEvent(session.session_id, 'connector_launch_succeeded', successMetadata)
      } catch {
        setLaunchMessage(
          `Launched ${session.launch_type} for ${item.asset_name}, but backend metadata update failed.`,
        )
        return
      }

      // launch message is set immediately after connector handoff to include richer instructions.
    } catch (err) {
      let message = err instanceof Error ? err.message : 'failed to launch asset'
      if (message.includes('connector handoff failed')) {
        message += '. Ensure the local connector is running and reachable at /connector.'
      }
      if (sessionID) {
        try {
          await recordSessionEvent(sessionID, 'connector_launch_failed', {
            connector_action: action,
            error: message,
          })
        } catch {
          // Keep user flow simple; launch error is still surfaced below.
        }
      }
      setLaunchMessage(`Launch failed for ${item.asset_name}: ${message}`)
    } finally {
      setLaunchingAssetID(null)
    }
  }

  const onLogout = async () => {
    await logout()
  }

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>My Access</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <a href="/sessions">My Sessions</a>
          {canReadAdmin ? <a href="/admin/dashboard">Admin Dashboard</a> : null}
          {canReadAdmin ? <a href="/admin/sessions">Admin Sessions</a> : null}
          {user?.roles.includes('admin') ? <a href="/admin/users">Admin Users</a> : null}
          {user?.roles.includes('admin') ? <a href="/admin/assets">Admin Assets</a> : null}
          <button onClick={() => void onLogout()}>Logout</button>
        </div>
      </header>

      {loading && <p>Loading access...</p>}
      {error && <p className="error">{error}</p>}
      {launchMessage && <p className="status">{launchMessage}</p>}

      {!loading && !error && (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Asset</th>
                <th>Type</th>
                <th>Endpoint</th>
                <th>Allowed Actions</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => {
                const canShell = item.asset_type === 'linux_vm' && item.allowed_actions.includes('shell')
                const canSFTP = item.asset_type === 'linux_vm' && item.allowed_actions.includes('sftp')
                const canDBeaver = item.asset_type === 'database' && item.allowed_actions.includes('dbeaver')
                const canRedis = item.asset_type === 'redis' && item.allowed_actions.includes('redis')
                const isLaunching = launchingAssetID === item.asset_id

                return (
                  <tr key={item.asset_id}>
                    <td>{item.asset_name}</td>
                    <td>{item.asset_type}</td>
                    <td>{item.endpoint}</td>
                    <td>{item.allowed_actions.join(', ')}</td>
                    <td>
                      {(canShell || canSFTP || canDBeaver || canRedis) ? (
                        <div className="actions-inline">
                          {canShell ? (
                            <button disabled={isLaunching} onClick={() => void launchAsset(item, 'shell')}>
                              {isLaunching ? 'Launching...' : 'Shell'}
                            </button>
                          ) : null}
                          {canSFTP ? (
                            <button disabled={isLaunching} onClick={() => void launchAsset(item, 'sftp')}>
                              {isLaunching ? 'Launching...' : 'SFTP'}
                            </button>
                          ) : null}
                          {canDBeaver ? (
                            <button disabled={isLaunching} onClick={() => void launchAsset(item, 'dbeaver')}>
                              {isLaunching ? 'Launching...' : 'DBeaver'}
                            </button>
                          ) : null}
                          {canRedis ? (
                            <button disabled={isLaunching} onClick={() => void launchAsset(item, 'redis')}>
                              {isLaunching ? 'Launching...' : 'Redis CLI'}
                            </button>
                          ) : null}
                        </div>
                      ) : (
                        <span className="muted">-</span>
                      )}
                    </td>
                  </tr>
                )
              })}
              {items.length === 0 && (
                <tr>
                  <td colSpan={5} className="muted">
                    No access points assigned.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </main>
  )
}
