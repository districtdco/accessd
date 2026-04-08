import { useEffect, useState } from 'react'
import {
  ConnectorHandoffError,
  connectorTokenForHandoff,
  createSessionLaunch,
  getMyAccess,
  handoffDBeaverToConnector,
  handoffRedisToConnector,
  handoffSFTPToConnector,
  handoffShellToConnector,
  recordSessionEvent,
} from '../api'
import type { AccessPoint, DBeaverLaunchConnection, RedisLaunchConnection, SFTPLaunchConnection, ShellLaunchConnection } from '../types'
import { Badge, Button, Card, EmptyRow, ErrorState, LoadingState, PageHeader, SuccessState, Table, Td, Th } from '../components/ui'

function displayIdentity(
  assetName: string,
  launch?: Partial<ShellLaunchConnection & SFTPLaunchConnection & DBeaverLaunchConnection>,
): string {
  if (!launch) return assetName
  const upstream = (launch.upstream_username || launch.username || '').trim()
  const target = (launch.target_asset_name || assetName || '').trim()
  const host = (launch.target_host || '').trim()
  if (!upstream && !target) return assetName
  const base = upstream && target ? `${upstream}@${target}` : (upstream || target)
  if (host && target && host !== target) {
    return `${base} (${host})`
  }
  return base
}

export function AccessPage() {
  const [items, setItems] = useState<AccessPoint[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [launchingAssetID, setLaunchingAssetID] = useState<string | null>(null)
  const [launchMessage, setLaunchMessage] = useState<string | null>(null)
  const [launchMessageKind, setLaunchMessageKind] = useState<'success' | 'error'>('success')

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
    setLaunchMessageKind('success')
    setLaunchingAssetID(item.asset_id)
    let sessionID: string | null = null

    try {
      const session = await createSessionLaunch({
        asset_id: item.asset_id,
        action,
      })
      sessionID = session.session_id
      const connectorToken = connectorTokenForHandoff(session)
      const connectorBase = {
        session_id: session.session_id,
        asset_id: item.asset_id,
        asset_name: item.asset_name,
        connector_token: connectorToken,
      }

      await recordSessionEvent(session.session_id, 'connector_launch_requested', {
        connector_action: session.launch_type,
      })

      const successMetadata: Record<string, unknown> = {
        connector_action: session.launch_type,
      }

      if (session.launch_type === 'shell') {
        const shellLaunch = session.launch as ShellLaunchConnection
        const result = await handoffShellToConnector({
          ...connectorBase,
          launch: shellLaunch,
        })
        if (result.hint) {
          successMetadata.instructions = result.hint
        }
        setLaunchMessage(`Shell launch started for ${displayIdentity(item.asset_name, shellLaunch)}. You are being authenticated automatically.`)
      } else if (session.launch_type === 'sftp') {
        const sftpLaunch = session.launch as SFTPLaunchConnection
        const result = await handoffSFTPToConnector({
          ...connectorBase,
          launch: sftpLaunch,
        })
        if (result.hint) {
          successMetadata.instructions = result.hint
        }
        if (result.diagnostics) {
          successMetadata.diagnostics = result.diagnostics
        }
        setLaunchMessage(`SFTP launch requested for ${displayIdentity(item.asset_name, sftpLaunch)}.`)
      } else if (session.launch_type === 'dbeaver') {
        const dbLaunch = session.launch as DBeaverLaunchConnection
        const result = await handoffDBeaverToConnector({
          ...connectorBase,
          launch: dbLaunch,
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
          setLaunchMessage(`DBeaver launch requested for ${displayIdentity(item.asset_name, dbLaunch)}. Local temp material will auto-clean in about ${mins} minute(s).`)
        } else {
          setLaunchMessage(`DBeaver launch requested for ${displayIdentity(item.asset_name, dbLaunch)}.`)
        }
      } else {
        const result = await handoffRedisToConnector({
          ...connectorBase,
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
    } catch (err) {
      setLaunchMessageKind('error')
      let message = err instanceof Error ? err.message : 'failed to launch asset'
      const connectorError = err instanceof ConnectorHandoffError ? err : null
      if (message.includes('connector handoff failed') || message.includes('Connector rejected launch authorization')) {
        message += '. Ensure the local connector is running and reachable at /connector.'
      }
      if (connectorError?.hint) {
        message += ` Hint: ${connectorError.hint}.`
      }
      if (sessionID) {
        const metadata: Record<string, unknown> = {
          connector_action: action,
          error: message,
        }
        if (connectorError?.code) {
          metadata.code = connectorError.code
        }
        if (connectorError?.details) {
          metadata.details = connectorError.details
        }
        try {
          await recordSessionEvent(sessionID, 'connector_launch_failed', metadata)
        } catch {
          // Keep user flow simple; launch error is still surfaced below.
        }
      }
      setLaunchMessage(`Launch failed for ${item.asset_name}: ${message}`)
    } finally {
      setLaunchingAssetID(null)
    }
  }

  return (
    <>
      <PageHeader title="My Access" />

      {launchMessage && (
        <div className="mb-4">
          {launchMessageKind === 'error' ? <ErrorState message={launchMessage} /> : <SuccessState message={launchMessage} />}
        </div>
      )}
      {error && <div className="mb-4"><ErrorState message={error} /></div>}
      {loading && <LoadingState message="Loading access..." />}

      {!loading && !error && (
        <Card>
          <Table>
            <thead>
              <tr>
                <Th>Asset</Th>
                <Th>Type</Th>
                <Th>Endpoint</Th>
                <Th>Allowed Actions</Th>
                <Th>Action</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {items.map((item) => {
                const canShell = item.asset_type === 'linux_vm' && item.allowed_actions.includes('shell')
                const canSFTP = item.asset_type === 'linux_vm' && item.allowed_actions.includes('sftp')
                const canDBeaver = item.asset_type === 'database' && item.allowed_actions.includes('dbeaver')
                const canRedis = item.asset_type === 'redis' && item.allowed_actions.includes('redis')
                const isLaunching = launchingAssetID === item.asset_id

                return (
                  <tr key={item.asset_id} className="hover:bg-gray-50">
                    <Td className="font-medium text-gray-900">{item.asset_name}</Td>
                    <Td><Badge>{item.asset_type}</Badge></Td>
                    <Td mono>{item.endpoint}</Td>
                    <Td>
                      <div className="flex gap-1">
                        {item.allowed_actions.map((a) => <Badge key={a} color="indigo">{a}</Badge>)}
                      </div>
                    </Td>
                    <Td>
                      {(canShell || canSFTP || canDBeaver || canRedis) ? (
                        <div className="flex gap-1.5">
                          {canShell && (
                            <Button size="sm" disabled={isLaunching} onClick={() => void launchAsset(item, 'shell')}>
                              {isLaunching ? 'Launching...' : 'Shell'}
                            </Button>
                          )}
                          {canSFTP && (
                            <Button size="sm" variant="secondary" disabled={isLaunching} onClick={() => void launchAsset(item, 'sftp')}>
                              {isLaunching ? 'Launching...' : 'SFTP'}
                            </Button>
                          )}
                          {canDBeaver && (
                            <Button size="sm" disabled={isLaunching} onClick={() => void launchAsset(item, 'dbeaver')}>
                              {isLaunching ? 'Launching...' : 'DBeaver'}
                            </Button>
                          )}
                          {canRedis && (
                            <Button size="sm" variant="secondary" disabled={isLaunching} onClick={() => void launchAsset(item, 'redis')}>
                              {isLaunching ? 'Launching...' : 'Redis CLI'}
                            </Button>
                          )}
                        </div>
                      ) : (
                        <span className="text-gray-400">-</span>
                      )}
                    </Td>
                  </tr>
                )
              })}
              {items.length === 0 && <EmptyRow colSpan={5} message="No access points assigned." />}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  )
}
