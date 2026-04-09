import { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import { getConnectorReleaseMetadata, getConnectorVersion, issueConnectorBootstrapToken } from '../api'
import type { ConnectorReleaseMetadata } from '../types'

type HeaderProps = {
  onMenuClick: () => void
}

export function Header({ onMenuClick }: HeaderProps) {
  const { user, logout } = useAuth()
  const location = useLocation()
  const navigate = useNavigate()
  const [profileMenuOpen, setProfileMenuOpen] = useState(false)
  const profileMenuRef = useRef<HTMLDivElement | null>(null)
  const [connectorState, setConnectorState] = useState<{
    kind: 'hidden' | 'checking' | 'ready' | 'missing' | 'outdated'
    label?: string
    downloadURL?: string
  }>({ kind: 'hidden' })

  useEffect(() => {
    let cancelled = false
    if (location.pathname !== '/') {
      setConnectorState({ kind: 'hidden' })
      return () => {
        cancelled = true
      }
    }

    const detectPlatform = () => {
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
      let arch: 'arm64' | 'amd64' = isArm ? 'arm64' : 'amd64'
      if (isMac && !isArm && !isExplicitX64) {
        arch = 'arm64'
      }
      return {
        os: isWindows ? 'windows' : (isMac ? 'darwin' : (isLinux ? 'linux' : 'linux')),
        arch,
      } as const
    }

    const parseSemverPart = (part: string | undefined): number => {
      const match = (part ?? '').match(/^\d+/)
      return match ? Number(match[0]) : 0
    }

    const normalizeSemver = (raw: string): [number, number, number] => {
      const cleaned = raw.trim().replace(/^v/i, '').split('-')[0]
      const parts = cleaned.split('.')
      return [
        parseSemverPart(parts[0]),
        parseSemverPart(parts[1]),
        parseSemverPart(parts[2]),
      ]
    }

    const compareSemver = (a: string, b: string): number => {
      const left = normalizeSemver(a)
      const right = normalizeSemver(b)
      for (let i = 0; i < 3; i += 1) {
        if (left[i] > right[i]) return 1
        if (left[i] < right[i]) return -1
      }
      return 0
    }

    const packagePriority = (os: 'darwin' | 'linux' | 'windows', packageType: string): number => {
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

    const archPriority = (
      platform: ReturnType<typeof detectPlatform>,
      arch: 'arm64' | 'amd64',
    ): number => {
      if (platform.os === 'darwin') {
        return arch === 'arm64' ? 0 : 1
      }
      return arch === platform.arch ? 0 : 1
    }

    const artifactCandidatesForPlatform = (
      metadata: ConnectorReleaseMetadata | null,
      platform: ReturnType<typeof detectPlatform>,
    ) => {
      const sameArch = metadata?.artifacts.filter((a) => a.os === platform.os && a.arch === platform.arch) ?? []
      const sameOS = metadata?.artifacts.filter((a) => a.os === platform.os) ?? []
      const candidates = sameArch.length > 0 ? sameArch : sameOS
      return candidates
        .slice()
        .sort((a, b) => {
          const packageDelta = packagePriority(platform.os, a.package_type) - packagePriority(platform.os, b.package_type)
          if (packageDelta !== 0) return packageDelta
          const archDelta = archPriority(platform, a.arch) - archPriority(platform, b.arch)
          if (archDelta !== 0) return archDelta
          if (a.preferred !== b.preferred) return a.preferred ? -1 : 1
          return a.file_name.localeCompare(b.file_name)
        })
    }

    const isArtifactAvailable = async (url: string): Promise<boolean> => {
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

    const selectArtifactWithFallback = async (
      metadata: ConnectorReleaseMetadata | null,
      platform: ReturnType<typeof detectPlatform>,
    ) => {
      const candidates = artifactCandidatesForPlatform(metadata, platform)
      if (candidates.length === 0) return undefined
      for (const candidate of candidates) {
        if (await isArtifactAvailable(candidate.download_url)) {
          return candidate
        }
      }
      return candidates[0]
    }

    const triggerConnectorAutostart = async () => {
      const baseURL = (import.meta.env.VITE_CONNECTOR_AUTOSTART_URL as string | undefined)?.trim()
        || 'accessd-connector://start'
      const sep = baseURL.includes('?') ? '&' : '?'
      const origin = window.location.origin
      let url = origin ? `${baseURL}${sep}origin=${encodeURIComponent(origin)}` : baseURL
      try {
        const issued = await issueConnectorBootstrapToken(origin)
        if (issued.token) {
          const sep2 = url.includes('?') ? '&' : '?'
          url = `${url}${sep2}bootstrap_token=${encodeURIComponent(issued.token)}`
        }
      } catch {
        // keep origin-only fallback
      }
      try {
        const iframe = document.createElement('iframe')
        iframe.style.display = 'none'
        iframe.src = url
        document.body.appendChild(iframe)
        window.setTimeout(() => {
          try { document.body.removeChild(iframe) } catch { /* ignore */ }
        }, 1200)
      } catch {
        // ignore; fallback is manual download path
      }
    }

    const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))
    const waitForConnectorVersion = async (timeoutMs: number, intervalMs = 250): Promise<string | null> => {
      const deadline = Date.now() + timeoutMs
      while (Date.now() < deadline) {
        try {
          const version = await getConnectorVersion()
          if (version.trim() !== '') return version
        } catch {
          // keep polling
        }
        await wait(intervalMs)
      }
      return null
    }

    const run = async () => {
      setConnectorState({ kind: 'checking', label: 'Connector Checking' })
      const platform = detectPlatform()
      const metadata = await getConnectorReleaseMetadata().catch(() => null)
      let version: string | null = null
      try {
        version = await getConnectorVersion()
      } catch {
        await triggerConnectorAutostart()
        version = await waitForConnectorVersion(12000)
      }
      if (cancelled) return
      const artifact = await selectArtifactWithFallback(metadata, platform)
      if (cancelled) return

      if (!version) {
        setConnectorState({
          kind: 'missing',
          label: 'Connector Offline',
          downloadURL: artifact?.download_url,
        })
        return
      }
      if (metadata && compareSemver(version, metadata.minimum_version) < 0) {
        setConnectorState({
          kind: 'outdated',
          label: 'Connector Update Required',
          downloadURL: artifact?.download_url,
        })
        return
      }
      setConnectorState({
        kind: 'ready',
        label: `Connector Online (${version})`,
      })
    }

    void run()
    return () => {
      cancelled = true
    }
  }, [location.pathname])

  useEffect(() => {
    const onDocumentClick = (event: MouseEvent) => {
      if (!profileMenuRef.current) return
      if (!profileMenuRef.current.contains(event.target as Node)) {
        setProfileMenuOpen(false)
      }
    }
    const onEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setProfileMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', onDocumentClick)
    document.addEventListener('keydown', onEscape)
    return () => {
      document.removeEventListener('mousedown', onDocumentClick)
      document.removeEventListener('keydown', onEscape)
    }
  }, [])

  const canChangePassword = (user?.auth_provider ?? 'local') === 'local'

  return (
    <header className="sticky top-0 z-30 flex h-16 items-center justify-between border-b border-gray-200 bg-white px-4 sm:px-6">
      <button
        onClick={onMenuClick}
        className="rounded-lg p-2 text-gray-500 hover:bg-gray-100 lg:hidden"
        aria-label="Open sidebar"
      >
        <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
        </svg>
      </button>

      <div className="hidden lg:block" />

      <div className="flex items-center gap-4">
        {connectorState.kind !== 'hidden' && (
          <div className="hidden items-center gap-2 md:flex">
            <span
              className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium ${
                connectorState.kind === 'ready'
                  ? 'bg-emerald-50 text-emerald-700'
                  : connectorState.kind === 'checking'
                    ? 'bg-amber-50 text-amber-700'
                    : 'bg-red-50 text-red-700'
              }`}
            >
              {connectorState.kind === 'ready' ? (
                <StatusOnlineIcon />
              ) : connectorState.kind === 'checking' ? (
                <StatusCheckingIcon />
              ) : (
                <StatusOfflineIcon />
              )}
              {connectorState.label}
            </span>
            {(connectorState.kind === 'missing' || connectorState.kind === 'outdated') && connectorState.downloadURL && (
              <button
                onClick={() => window.open(connectorState.downloadURL, '_blank', 'noopener,noreferrer')}
                className="inline-flex items-center gap-1 rounded-lg border border-indigo-600 bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-700"
              >
                <DownloadIcon />
                <span>Download Connector</span>
              </button>
            )}
            {(connectorState.kind === 'missing' || connectorState.kind === 'outdated') && (
              <button
                onClick={() => void navigate('/connector/versions')}
                className="inline-flex items-center gap-1 rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50"
              >
                <VersionsIcon />
                <span>View Versions</span>
              </button>
            )}
          </div>
        )}
        <div className="text-right">
          <p className="text-sm font-medium text-gray-900">{user?.display_name || user?.username}</p>
          <p className="text-xs text-gray-500">
            {user?.roles.join(', ') || 'user'}
          </p>
        </div>
        <div className="relative" ref={profileMenuRef}>
          <button
            onClick={() => setProfileMenuOpen((open) => !open)}
            className="flex h-9 w-9 items-center justify-center rounded-full bg-indigo-100 text-sm font-semibold text-indigo-700 hover:bg-indigo-200"
            aria-label="Open profile menu"
            aria-expanded={profileMenuOpen}
          >
            {(user?.username ?? 'U')[0].toUpperCase()}
          </button>
          {profileMenuOpen && (
            <div className="absolute right-0 top-11 z-50 w-56 rounded-xl border border-gray-200 bg-white p-1 shadow-lg">
              <div className="px-3 py-2">
                <p className="truncate text-sm font-semibold text-gray-900">{user?.display_name || user?.username}</p>
                <p className="truncate text-xs text-gray-500">{user?.auth_provider ?? 'local'} auth</p>
              </div>
              <div className="h-px bg-gray-100" />
              {canChangePassword && (
                <button
                  onClick={() => {
                    setProfileMenuOpen(false)
                    void navigate('/account')
                  }}
                  className="mt-1 flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-50"
                >
                  <PasswordIcon />
                  Change Password
                </button>
              )}
              <button
                onClick={() => {
                  setProfileMenuOpen(false)
                  void logout()
                }}
                className="mt-1 flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-red-700 hover:bg-red-50"
              >
                <LogoutIcon />
                Logout
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  )
}

function DownloadIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="h-4 w-4" aria-hidden="true">
      <path d="M10 2a1 1 0 0 1 1 1v7.586l1.293-1.293a1 1 0 1 1 1.414 1.414l-3 3a1 1 0 0 1-1.414 0l-3-3a1 1 0 1 1 1.414-1.414L9 10.586V3a1 1 0 0 1 1-1Z" />
      <path d="M3 13a1 1 0 0 1 1 1v1a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-1a1 1 0 1 1 2 0v1a3 3 0 0 1-3 3H5a3 3 0 0 1-3-3v-1a1 1 0 0 1 1-1Z" />
    </svg>
  )
}

function VersionsIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path d="M10.362 1.093a1 1 0 0 0-.724 0l-7 2.625a1 1 0 0 0 0 1.874l7 2.625a1 1 0 0 0 .724 0l7-2.625a1 1 0 0 0 0-1.874l-7-2.625Z" />
      <path d="M3.638 8.618a1 1 0 0 0-1.276.618 1 1 0 0 0 .618 1.276l6.3 2.362a2 2 0 0 0 1.44 0l6.3-2.362a1 1 0 1 0-.658-1.888l-6.3 2.362-6.424-2.368Z" />
      <path d="M3.638 12.618a1 1 0 0 0-1.276.618 1 1 0 0 0 .618 1.276l6.3 2.362a2 2 0 0 0 1.44 0l6.3-2.362a1 1 0 1 0-.658-1.888l-6.3 2.362-6.424-2.368Z" />
    </svg>
  )
}

function StatusOnlineIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path fillRule="evenodd" d="M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm3.78-9.03a.75.75 0 0 0-1.06-1.06L9.25 11.38 7.28 9.41a.75.75 0 0 0-1.06 1.06l2.5 2.5a.75.75 0 0 0 1.06 0l4-4Z" clipRule="evenodd" />
    </svg>
  )
}

function StatusCheckingIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5 animate-spin" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 0 1 8-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  )
}

function StatusOfflineIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path fillRule="evenodd" d="M18 10A8 8 0 1 1 2 10a8 8 0 0 1 16 0Zm-8-4a.75.75 0 0 1 .75.75v3.19l2.28 2.28a.75.75 0 1 1-1.06 1.06L9.47 10.8A.75.75 0 0 1 9.25 10V6.75A.75.75 0 0 1 10 6Z" clipRule="evenodd" />
    </svg>
  )
}

function PasswordIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path fillRule="evenodd" d="M10 2a3 3 0 0 0-3 3v2H6a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-1V5a3 3 0 0 0-3-3Zm1.5 5V5a1.5 1.5 0 1 0-3 0v2h3Z" clipRule="evenodd" />
    </svg>
  )
}

function LogoutIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path fillRule="evenodd" d="M3 4.75A1.75 1.75 0 0 1 4.75 3h5.5A1.75 1.75 0 0 1 12 4.75V6a.75.75 0 0 1-1.5 0V4.75a.25.25 0 0 0-.25-.25h-5.5a.25.25 0 0 0-.25.25v10.5c0 .138.112.25.25.25h5.5a.25.25 0 0 0 .25-.25V14a.75.75 0 0 1 1.5 0v1.25A1.75 1.75 0 0 1 10.25 17h-5.5A1.75 1.75 0 0 1 3 15.25V4.75Z" clipRule="evenodd" />
      <path fillRule="evenodd" d="M19 10a.75.75 0 0 1-.22.53l-2.5 2.5a.75.75 0 1 1-1.06-1.06l1.22-1.22H8a.75.75 0 0 1 0-1.5h8.44l-1.22-1.22a.75.75 0 1 1 1.06-1.06l2.5 2.5c.14.14.22.33.22.53Z" clipRule="evenodd" />
    </svg>
  )
}
