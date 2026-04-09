import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useAuth } from '../auth'
import { getConnectorReleaseMetadata, getConnectorVersion } from '../api'
import type { ConnectorReleaseMetadata } from '../types'

type HeaderProps = {
  onMenuClick: () => void
}

export function Header({ onMenuClick }: HeaderProps) {
  const { user, logout } = useAuth()
  const location = useLocation()
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
      const isWindows = platform.includes('win') || ua.includes('windows')
      const isMac = platform.includes('mac') || ua.includes('mac os')
      const isLinux = !isWindows && !isMac
      const isArm = ua.includes('arm64') || ua.includes('aarch64') || ua.includes('arm')
      return {
        os: isWindows ? 'windows' : (isMac ? 'darwin' : (isLinux ? 'linux' : 'linux')),
        arch: isArm ? 'arm64' : 'amd64',
      } as const
    }

    const normalizeSemver = (raw: string): [number, number, number] => {
      const cleaned = raw.trim().replace(/^v/i, '').split('-')[0]
      const parts = cleaned.split('.')
      return [Number(parts[0] || 0), Number(parts[1] || 0), Number(parts[2] || 0)]
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

    const artifactForPlatform = (
      metadata: ConnectorReleaseMetadata | null,
      platform: ReturnType<typeof detectPlatform>,
    ) => metadata?.artifacts.find((a) => a.os === platform.os && a.arch === platform.arch)
      ?? metadata?.artifacts.find((a) => a.os === platform.os)

    const triggerConnectorAutostart = () => {
      const url = (import.meta.env.VITE_CONNECTOR_AUTOSTART_URL as string | undefined)?.trim()
        || 'accessd-connector://start'
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
        triggerConnectorAutostart()
        version = await waitForConnectorVersion(12000)
      }
      if (cancelled) return
      const artifact = artifactForPlatform(metadata, platform)

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
              className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                connectorState.kind === 'ready'
                  ? 'bg-emerald-50 text-emerald-700'
                  : connectorState.kind === 'checking'
                    ? 'bg-amber-50 text-amber-700'
                    : 'bg-red-50 text-red-700'
              }`}
            >
              {connectorState.label}
            </span>
            {(connectorState.kind === 'missing' || connectorState.kind === 'outdated') && connectorState.downloadURL && (
              <button
                onClick={() => window.open(connectorState.downloadURL, '_blank', 'noopener,noreferrer')}
                className="inline-flex items-center gap-1 rounded-lg border border-indigo-600 bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-700"
              >
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="h-4 w-4" aria-hidden="true">
                  <path d="M10 2a1 1 0 0 1 1 1v7.586l1.293-1.293a1 1 0 1 1 1.414 1.414l-3 3a1 1 0 0 1-1.414 0l-3-3a1 1 0 1 1 1.414-1.414L9 10.586V3a1 1 0 0 1 1-1Z" />
                  <path d="M3 13a1 1 0 0 1 1 1v1a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-1a1 1 0 1 1 2 0v1a3 3 0 0 1-3 3H5a3 3 0 0 1-3-3v-1a1 1 0 0 1 1-1Z" />
                </svg>
                <span>Download Connector</span>
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
        <div className="flex h-9 w-9 items-center justify-center rounded-full bg-indigo-100 text-sm font-semibold text-indigo-700">
          {(user?.username ?? 'U')[0].toUpperCase()}
        </div>
        <button
          onClick={() => void logout()}
          className="rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 transition-colors"
        >
          Logout
        </button>
      </div>
    </header>
  )
}
