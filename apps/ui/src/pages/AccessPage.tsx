import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ConnectorHandoffError,
  connectorTokenForHandoff,
  createSessionLaunch,
  getConnectorDebugInfo,
  getConnectorHealth,
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
  ConnectorReleaseArtifact,
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
const CONNECTOR_AUTOSTART_BASE_URL = (import.meta.env.VITE_CONNECTOR_AUTOSTART_URL as string | undefined)?.trim()
  || 'accessd-connector://start'

function connectorAutostartURL(): string {
  const base = CONNECTOR_AUTOSTART_BASE_URL
  const origin = typeof window !== 'undefined' ? window.location.origin : ''
  if (!origin) return base
  const sep = base.includes('?') ? '&' : '?'
  return `${base}${sep}origin=${encodeURIComponent(origin)}`
}

function detectPlatform(): PlatformKey {
  const ua = navigator.userAgent.toLowerCase()
  const platform = (navigator.platform || '').toLowerCase()
  const uaData = (navigator as Navigator & {
    userAgentData?: { architecture?: string; platform?: string }
  }).userAgentData
  const uaArch = (uaData?.architecture || '').toLowerCase()
  const isWindows = platform.includes('win') || ua.includes('windows')
  const isMac = platform.includes('mac') || ua.includes('mac os')
  const isLinux = !isWindows && !isMac
  const isArm = uaArch.includes('arm') || ua.includes('arm64') || ua.includes('aarch64') || ua.includes('arm')
  const isExplicitX64 = uaArch.includes('x86') || uaArch.includes('amd64') || ua.includes('x86_64') || ua.includes('amd64')
  let arch: PlatformKey['arch'] = isArm ? 'arm64' : 'amd64'
  if (isMac && !isArm && !isExplicitX64) {
    // Browser UA on Apple Silicon can appear as MacIntel; prefer arm64 when ambiguous.
    arch = 'arm64'
  }
  return {
    os: isWindows ? 'windows' : (isMac ? 'darwin' : (isLinux ? 'linux' : 'linux')),
    arch,
  }
}

function normalizeSemver(raw: string): [number, number, number] {
  const cleaned = raw.trim().replace(/^v/i, '').split('-')[0]
  const parts = cleaned.split('.')
  const parsePart = (part: string | undefined): number => {
    const match = (part ?? '').match(/^\d+/)
    return match ? Number(match[0]) : 0
  }
  const major = parsePart(parts[0])
  const minor = parsePart(parts[1])
  const patch = parsePart(parts[2])
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

function iconDownload() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path d="M10 2a1 1 0 0 1 1 1v7.586l1.293-1.293a1 1 0 1 1 1.414 1.414l-3 3a1 1 0 0 1-1.414 0l-3-3a1 1 0 1 1 1.414-1.414L9 10.586V3a1 1 0 0 1 1-1Z" />
      <path d="M3 13a1 1 0 0 1 1 1v1a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-1a1 1 0 1 1 2 0v1a3 3 0 0 1-3 3H5a3 3 0 0 1-3-3v-1a1 1 0 0 1 1-1Z" />
    </svg>
  )
}

function iconRefresh() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path fillRule="evenodd" d="M15.312 11.424a.75.75 0 0 1 1.06.014A7.5 7.5 0 1 1 17.5 10a.75.75 0 0 1-1.5 0A6 6 0 1 0 14.78 13.5a.75.75 0 0 1 .532-2.076Z" clipRule="evenodd" />
      <path fillRule="evenodd" d="M13.53 9.53a.75.75 0 0 1 1.06 0l2.22 2.22a.75.75 0 0 1 0 1.06l-2.22 2.22a.75.75 0 0 1-1.06-1.06l1.69-1.69-1.69-1.69a.75.75 0 0 1 0-1.06Z" clipRule="evenodd" />
    </svg>
  )
}

function iconLayers() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path d="M10.362 1.093a1 1 0 0 0-.724 0l-7 2.625a1 1 0 0 0 0 1.874l7 2.625a1 1 0 0 0 .724 0l7-2.625a1 1 0 0 0 0-1.874l-7-2.625Z" />
      <path d="M3.638 8.618a1 1 0 0 0-1.276.618 1 1 0 0 0 .618 1.276l6.3 2.362a2 2 0 0 0 1.44 0l6.3-2.362a1 1 0 1 0-.658-1.888l-6.3 2.362-6.424-2.368Z" />
      <path d="M3.638 12.618a1 1 0 0 0-1.276.618 1 1 0 0 0 .618 1.276l6.3 2.362a2 2 0 0 0 1.44 0l6.3-2.362a1 1 0 1 0-.658-1.888l-6.3 2.362-6.424-2.368Z" />
    </svg>
  )
}

function triggerConnectorAutostart(): boolean {
  if (typeof window === 'undefined' || typeof document === 'undefined') {
    return false
  }
  const autostartURL = connectorAutostartURL()
  try {
    // Keep this synchronous so browsers treat it as a user gesture launch.
    const link = document.createElement('a')
    link.href = autostartURL
    link.style.display = 'none'
    document.body.appendChild(link)
    link.click()
    window.setTimeout(() => {
      try {
        document.body.removeChild(link)
      } catch {
        // ignore
      }
    }, 500)
    return true
  } catch {
    try {
      window.location.href = autostartURL
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

async function waitForConnectorHealth(timeoutMs: number, intervalMs = 250): Promise<boolean> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      if (await getConnectorHealth()) {
        return true
      }
    } catch {
      // keep polling until timeout
    }
    await sleep(intervalMs)
  }
  return false
}

function packagePriorityForPlatform(
  os: PlatformKey['os'],
  packageType: string,
): number {
  if (os === 'darwin') {
    if (packageType === 'pkg') return 0
    if (packageType === 'archive') return 1
  }
  if (os === 'windows') {
    if (packageType === 'msi') return 0
    if (packageType === 'archive') return 1
  }
  if (os === 'linux') {
    if (packageType === 'deb') return 0
    if (packageType === 'rpm') return 1
    if (packageType === 'archive') return 2
  }
  return 99
}

function archPriorityForPlatform(
  platform: PlatformKey,
  arch: ConnectorReleaseArtifact['arch'],
): number {
  if (platform.os === 'darwin') {
    return arch === 'arm64' ? 0 : 1
  }
  return arch === platform.arch ? 0 : 1
}

function artifactCandidatesForPlatform(
  metadata: ConnectorReleaseMetadata | null,
  platform: PlatformKey,
) {
  const sameArch = metadata?.artifacts.filter((a) => a.os === platform.os && a.arch === platform.arch) ?? []
  const sameOS = metadata?.artifacts.filter((a) => a.os === platform.os) ?? []
  const candidates = sameArch.length > 0 ? sameArch : sameOS
  return candidates
    .slice()
    .sort((a, b) => {
      const packageDelta = packagePriorityForPlatform(platform.os, a.package_type) - packagePriorityForPlatform(platform.os, b.package_type)
      if (packageDelta !== 0) return packageDelta
      const archDelta = archPriorityForPlatform(platform, a.arch) - archPriorityForPlatform(platform, b.arch)
      if (archDelta !== 0) return archDelta
      if (a.preferred !== b.preferred) return a.preferred ? -1 : 1
      return a.file_name.localeCompare(b.file_name)
    })
}

async function isArtifactAvailable(url: string): Promise<boolean> {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 1500)
  try {
    const resp = await fetch(url, {
      method: 'HEAD',
      cache: 'no-store',
      signal: controller.signal,
    })
    return resp.ok
  } catch {
    return false
  } finally {
    window.clearTimeout(timeout)
  }
}

async function artifactForPlatform(
  metadata: ConnectorReleaseMetadata | null,
  platform: PlatformKey,
) {
  const candidates = artifactCandidatesForPlatform(metadata, platform)
  if (candidates.length === 0) return undefined
  for (const candidate of candidates) {
    if (await isArtifactAvailable(candidate.download_url)) {
      return candidate
    }
  }
  return candidates[0]
}

async function preflightConnector(attemptAutostart: boolean): Promise<ConnectorStatus> {
  const platform = detectPlatform()
  const metadata = await getConnectorReleaseMetadata().catch(() => null)
  let connectorVersion: string | null = null
  let healthy = false

  try {
    healthy = await getConnectorHealth()
  } catch {
    healthy = false
  }

  if (!healthy && attemptAutostart) {
    triggerConnectorAutostart()
    healthy = await waitForConnectorHealth(12000)
  }

  if (healthy) {
    try {
      connectorVersion = await getConnectorVersion()
    } catch {
      connectorVersion = await waitForConnectorVersion(3000)
    }
  }

  if (!connectorVersion) {
    const artifact = await artifactForPlatform(metadata, platform)
    const downloadHint = artifact?.download_url ? ` Download: ${artifact.download_url}` : ''
    const docsHint = metadata?.install_docs_url ? ` Install guide: ${metadata.install_docs_url}` : ''
    const startHint = ` ${connectorStartHint(platform)}`
    return {
      kind: 'missing',
      message: `AccessD connector is not running on this machine.${startHint}${downloadHint}${docsHint}`,
      downloadURL: artifact?.download_url,
      installDocsURL: metadata?.install_docs_url,
    }
  }

  if (metadata && compareSemver(connectorVersion, metadata.minimum_version) < 0) {
    const artifact = await artifactForPlatform(metadata, platform)
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
  const navigate = useNavigate()
  const [items, setItems] = useState<AccessPoint[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [launchingAssetID, setLaunchingAssetID] = useState<string | null>(null)
  const [launchMessage, setLaunchMessage] = useState<string | null>(null)
  const [launchMessageKind, setLaunchMessageKind] = useState<'success' | 'error'>('success')
  const [connectorStatus, setConnectorStatus] = useState<ConnectorStatus>({ kind: 'checking' })
  const [connectorDebug, setConnectorDebug] = useState(getConnectorDebugInfo())
  const launchInFlightRef = useRef(false)
  const platform = detectPlatform()

  const refreshConnectorDebug = () => {
    setConnectorDebug(getConnectorDebugInfo())
  }

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)
      setConnectorStatus({ kind: 'checking' })
      try {
        const [response, status] = await Promise.all([
          getMyAccess(),
          preflightConnector(false),
        ])
        if (!cancelled) {
          setItems(response.items)
          setConnectorStatus(status)
          refreshConnectorDebug()
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

  const refreshConnectorStatus = async () => {
    setConnectorStatus({ kind: 'checking' })
    const status = await preflightConnector(false)
    setConnectorStatus(status)
    refreshConnectorDebug()
  }

  const startConnectorNow = async () => {
    triggerConnectorAutostart()
    setConnectorStatus({ kind: 'checking' })
    const status = await preflightConnector(false)
    setConnectorStatus(status)
    refreshConnectorDebug()
  }

  const launchAsset = async (item: AccessPoint, action: 'shell' | 'sftp' | 'dbeaver' | 'redis') => {
    if (launchInFlightRef.current) {
      return
    }
    launchInFlightRef.current = true
    setLaunchMessage(null)
    setLaunchMessageKind('success')
    setLaunchingAssetID(item.asset_id)
    let sessionID: string | null = null

    try {
      const status = await preflightConnector(connectorStatus.kind !== 'ready')
      if (status.kind === 'ready') {
        setConnectorStatus(status)
        refreshConnectorDebug()
      } else if (status.kind === 'missing' || status.kind === 'outdated' || status.kind === 'error') {
        setConnectorStatus(status)
        refreshConnectorDebug()
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
        message += '. Ensure the local connector is running and reachable at https://127.0.0.1:9494.'
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
      launchInFlightRef.current = false
      refreshConnectorDebug()
    }
  }

  return (
    <>
      <PageHeader title="My Access" />

      {connectorStatus.kind === 'checking' && (
        <div className="mb-4"><LoadingState message="Checking AccessD connector status..." /></div>
      )}
      {(connectorStatus.kind === 'missing' || connectorStatus.kind === 'outdated' || connectorStatus.kind === 'ready') && (
        <Card className="mb-4">
          <div className="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <div className="mb-1 flex items-center gap-2">
                <Badge
                  color={
                    connectorStatus.kind === 'ready'
                      ? 'green'
                      : connectorStatus.kind === 'outdated'
                        ? 'yellow'
                        : 'red'
                  }
                >
                  {connectorStatus.kind === 'ready'
                    ? `Connector Online (${connectorStatus.version})`
                    : connectorStatus.kind === 'outdated'
                      ? 'Connector Update Needed'
                      : 'Connector Offline'}
                </Badge>
              </div>
              <p className="text-sm text-gray-600">
                {connectorStatus.kind === 'ready'
                  ? `Connector is reachable at local endpoint for ${platformLabel(platform.os)} (${platform.arch}).`
                  : connectorStatus.message}
              </p>
              <p className="mt-1 text-xs text-gray-500">
                Debug endpoint: probe={connectorDebug.lastProbeBase ?? '-'} | launch={connectorDebug.lastLaunchBase ?? '-'} | preferred={connectorDebug.preferredBase ?? '-'}
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {(connectorStatus.kind === 'missing' || connectorStatus.kind === 'outdated') && connectorStatus.downloadURL && (
                <Button size="sm" onClick={() => window.open(connectorStatus.downloadURL, '_blank', 'noopener,noreferrer')}>
                  <span className="mr-1">{iconDownload()}</span>
                  Download
                </Button>
              )}
              {(connectorStatus.kind === 'missing' || connectorStatus.kind === 'outdated') && (
                <Button size="sm" variant="secondary" onClick={() => void navigate('/connector/versions')}>
                  <span className="mr-1">{iconLayers()}</span>
                  View Versions
                </Button>
              )}
              {(connectorStatus.kind === 'missing' || connectorStatus.kind === 'outdated') && connectorStatus.installDocsURL && (
                <Button size="sm" variant="ghost" onClick={() => window.open(connectorStatus.installDocsURL, '_blank', 'noopener,noreferrer')}>
                  Install Guide
                </Button>
              )}
              {(connectorStatus.kind === 'missing' || connectorStatus.kind === 'outdated') && (
                <Button size="sm" onClick={() => void startConnectorNow()}>
                  Start Connector
                </Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => void refreshConnectorStatus()}>
                <span className="mr-1">{iconRefresh()}</span>
                Recheck
              </Button>
            </div>
          </div>
        </Card>
      )}
      {connectorStatus.kind === 'error' && (
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
                const launchInProgress = launchingAssetID !== null
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
                            <Button size="sm" disabled={launchInProgress} onClick={() => void launchAsset(item, 'shell')}>
                              {isLaunching ? 'Launching...' : 'Shell'}
                            </Button>
                          )}
                          {canSFTP && (
                            <Button size="sm" variant="secondary" disabled={launchInProgress} onClick={() => void launchAsset(item, 'sftp')}>
                              {isLaunching ? 'Launching...' : 'SFTP'}
                            </Button>
                          )}
                          {canDBeaver && (
                            <Button size="sm" disabled={launchInProgress} onClick={() => void launchAsset(item, 'dbeaver')}>
                              {isLaunching ? 'Launching...' : 'DBeaver'}
                            </Button>
                          )}
                          {canRedis && (
                            <Button size="sm" variant="secondary" disabled={launchInProgress} onClick={() => void launchAsset(item, 'redis')}>
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
