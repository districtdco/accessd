import { useEffect, useState } from 'react'
import {
  ConnectorHandoffError,
  connectorTokenForHandoff,
  createSessionLaunch,
  getConnectorReleaseMetadata,
  getConnectorVersion,
  getMyAccess,
  handoffDBeaverToConnector,
  handoffRedisToConnector,
  handoffSFTPToConnector,
  handoffShellToConnector,
  recordSessionEvent,
} from '../api'
import type {
  AccessPoint,
  ConnectorReleaseMetadata,
  DBeaverLaunchConnection,
  RedisLaunchConnection,
  SFTPLaunchConnection,
  ShellLaunchConnection,
} from '../types'
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

type PlatformKey = { os: 'darwin' | 'linux' | 'windows'; arch: 'amd64' | 'arm64' }
const CONNECTOR_AUTOSTART_URL = (import.meta.env.VITE_CONNECTOR_AUTOSTART_URL as string | undefined)?.trim()
  || 'accessd-connector://start'

function detectPlatform(): PlatformKey {
  const ua = navigator.userAgent.toLowerCase()
  const platform = (navigator.platform || '').toLowerCase()
  const isWindows = platform.includes('win') || ua.includes('windows')
  const isMac = platform.includes('mac') || ua.includes('mac os')
  const isLinux = !isWindows && !isMac
  const isArm = ua.includes('arm64') || ua.includes('aarch64') || ua.includes('arm')
  return {
    os: isWindows ? 'windows' : (isMac ? 'darwin' : (isLinux ? 'linux' : 'linux')),
    arch: isArm ? 'arm64' : 'amd64',
  }
}

function normalizeSemver(raw: string): [number, number, number] {
  const cleaned = raw.trim().replace(/^v/i, '').split('-')[0]
  const parts = cleaned.split('.')
  const major = Number(parts[0] || 0)
  const minor = Number(parts[1] || 0)
  const patch = Number(parts[2] || 0)
  return [major, minor, patch]
}

function compareSemver(a: string, b: string): number {
  const left = normalizeSemver(a)
  const right = normalizeSemver(b)
  for (let i = 0; i < 3; i += 1) {
    if (left[i] > right[i]) return 1
    if (left[i] < right[i]) return -1
  }
  return 0
}

function platformLabel(os: PlatformKey['os']): string {
  if (os === 'darwin') return 'macOS'
  if (os === 'windows') return 'Windows'
  return 'Linux'
}

function installGuidanceForConnectorCode(
  code: string | undefined,
  action: 'shell' | 'sftp' | 'dbeaver' | 'redis',
  platform: PlatformKey,
): string | null {
  const os = platformLabel(platform.os)
  switch (code) {
    case 'dbeaver_not_installed':
      return `DBeaver is not installed on this ${os} machine. Install DBeaver and retry.`
    case 'filezilla_not_installed':
      return `FileZilla is not installed on this ${os} machine. Install FileZilla and retry.`
    case 'putty_not_installed':
      return `PuTTY is not installed on this Windows machine. Install PuTTY and retry.`
    case 'redis_cli_not_found':
    case 'redis_cli_not_installed':
      return `redis-cli is not installed on this ${os} machine. Install Redis CLI tools and retry.`
    case 'ssh_not_installed':
      return `OpenSSH client is not installed on this ${os} machine. Install OpenSSH and retry.`
    case 'terminal_not_installed':
      return `No supported terminal launcher was found on this ${os} machine. Install a terminal app and retry.`
    default:
      break
  }
  if (action === 'shell' && platform.os === 'windows') {
    return 'Shell launch requires PuTTY on Windows. Install PuTTY and retry.'
  }
  if (action === 'shell') {
    return 'Shell launch requires OpenSSH client. Install OpenSSH and retry.'
  }
  if (action === 'sftp') {
    return 'SFTP launch requires FileZilla. Install FileZilla and retry.'
  }
  if (action === 'dbeaver') {
    return 'Database launch requires DBeaver. Install DBeaver and retry.'
  }
  if (action === 'redis') {
    return 'Redis launch requires redis-cli. Install Redis CLI tools and retry.'
  }
  return null
}

function connectorStartHint(platform: PlatformKey): string {
  if (platform.os === 'windows') {
    return 'Start connector: accessd-connector.exe'
  }
  return 'Start connector: accessd-connector'
}

type ConnectorStatus =
  | { kind: 'checking' }
  | { kind: 'ready'; version: string; metadata: ConnectorReleaseMetadata | null }
  | { kind: 'missing'; message: string; downloadURL?: string; installDocsURL?: string }
  | { kind: 'outdated'; message: string; downloadURL?: string; installDocsURL?: string }
  | { kind: 'error'; message: string }

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function triggerConnectorAutostart(): boolean {
  if (typeof window === 'undefined' || typeof document === 'undefined') {
    return false
  }
  try {
    const iframe = document.createElement('iframe')
    iframe.style.display = 'none'
    iframe.setAttribute('aria-hidden', 'true')
    iframe.src = CONNECTOR_AUTOSTART_URL
    document.body.appendChild(iframe)
    window.setTimeout(() => {
      try {
        document.body.removeChild(iframe)
      } catch {
        // ignore
      }
    }, 1200)
    return true
  } catch {
    try {
      const link = document.createElement('a')
      link.href = CONNECTOR_AUTOSTART_URL
      link.style.display = 'none'
      document.body.appendChild(link)
      link.click()
      window.setTimeout(() => {
        try {
          document.body.removeChild(link)
        } catch {
          // ignore
        }
      }, 1200)
      return true
    } catch {
      return false
    }
  }
}

async function waitForConnectorVersion(timeoutMs: number, intervalMs = 250): Promise<string | null> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const version = await getConnectorVersion()
      if (version.trim() !== '') {
        return version
      }
    } catch {
      // keep polling until timeout
    }
    await sleep(intervalMs)
  }
  return null
}

function artifactForPlatform(
  metadata: ConnectorReleaseMetadata | null,
  platform: PlatformKey,
) {
  return metadata?.artifacts.find((a) => a.os === platform.os && a.arch === platform.arch)
    ?? metadata?.artifacts.find((a) => a.os === platform.os)
}

async function preflightConnector(attemptAutostart: boolean): Promise<ConnectorStatus> {
  const platform = detectPlatform()
  const metadata = await getConnectorReleaseMetadata().catch(() => null)
  let connectorVersion: string | null = null

  try {
    connectorVersion = await getConnectorVersion()
  } catch {
    if (attemptAutostart) {
      triggerConnectorAutostart()
      connectorVersion = await waitForConnectorVersion(12000)
    }
  }

  if (!connectorVersion) {
    const artifact = artifactForPlatform(metadata, platform)
    const downloadHint = artifact?.download_url ? ` Download: ${artifact.download_url}` : ''
    const docsHint = metadata?.install_docs_url ? ` Install guide: ${metadata.install_docs_url}` : ''
    const startHint = ` ${connectorStartHint(platform)}`
    return {
      kind: 'missing',
      message: `AccessD connector not installed or not running on this machine. Auto-start was attempted but connector is still unavailable.${startHint}${downloadHint}${docsHint}`,
      downloadURL: artifact?.download_url,
      installDocsURL: metadata?.install_docs_url,
    }
  }

  if (metadata && compareSemver(connectorVersion, metadata.minimum_version) < 0) {
    const artifact = artifactForPlatform(metadata, platform)
    const downloadHint = artifact?.download_url
      ? ` Download update: ${artifact.download_url}`
      : ''
    return {
      kind: 'outdated',
      message: `AccessD connector update available. Installed ${connectorVersion}, minimum supported ${metadata.minimum_version}.${downloadHint}`,
      downloadURL: artifact?.download_url,
      installDocsURL: metadata?.install_docs_url,
    }
  }

  return {
    kind: 'ready',
    version: connectorVersion,
    metadata,
  }
}

export function AccessPage() {
  const [items, setItems] = useState<AccessPoint[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [launchingAssetID, setLaunchingAssetID] = useState<string | null>(null)
  const [launchMessage, setLaunchMessage] = useState<string | null>(null)
  const [launchMessageKind, setLaunchMessageKind] = useState<'success' | 'error'>('success')
  const [connectorStatus, setConnectorStatus] = useState<ConnectorStatus>({ kind: 'checking' })

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)
      setConnectorStatus({ kind: 'checking' })
      try {
        const [response, status] = await Promise.all([
          getMyAccess(),
          preflightConnector(true),
        ])
        if (!cancelled) {
          setItems(response.items)
          setConnectorStatus(status)
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : 'failed to load access'
          setError(message)
          setConnectorStatus({ kind: 'error', message: 'Connector preflight could not be completed. Try refreshing this page.' })
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
      const status = await preflightConnector(true)
      if (status.kind === 'ready') {
        setConnectorStatus(status)
      } else if (status.kind === 'missing' || status.kind === 'outdated' || status.kind === 'error') {
        setConnectorStatus(status)
        throw new Error(status.message)
      }

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
        message += '. Ensure the local connector is running and reachable at http://127.0.0.1:9494.'
      }
      if (connectorError?.code) {
        const installGuidance = installGuidanceForConnectorCode(connectorError.code, action, detectPlatform())
        if (installGuidance) {
          message = `${installGuidance} ${message}`
        }
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

      {connectorStatus.kind === 'checking' && (
        <div className="mb-4"><LoadingState message="Checking AccessD connector status..." /></div>
      )}
      {(connectorStatus.kind === 'missing' || connectorStatus.kind === 'outdated' || connectorStatus.kind === 'error') && (
        <div className="mb-4">
          <ErrorState message={connectorStatus.message} />
        </div>
      )}
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
