import { useEffect, useMemo, useState } from 'react'
import { getConnectorReleaseVersions } from '../api'
import type { ConnectorReleaseArtifact, ConnectorReleaseVersionsResponse } from '../types'
import { Badge, Button, Card, ErrorState, LoadingState, PageHeader } from '../components/ui'

function artifactOrder(a: ConnectorReleaseArtifact, b: ConnectorReleaseArtifact): number {
  const osRank = (os: ConnectorReleaseArtifact['os']) => {
    if (os === 'darwin') return 0
    if (os === 'linux') return 1
    return 2
  }
  const packageRank = (pkg: ConnectorReleaseArtifact['package_type']) => {
    if (pkg === 'pkg') return 0
    if (pkg === 'msi') return 1
    if (pkg === 'deb') return 2
    if (pkg === 'rpm') return 3
    return 4
  }
  const osDelta = osRank(a.os) - osRank(b.os)
  if (osDelta !== 0) return osDelta
  const archDelta = a.arch.localeCompare(b.arch)
  if (archDelta !== 0) return archDelta
  const pkgDelta = packageRank(a.package_type) - packageRank(b.package_type)
  if (pkgDelta !== 0) return pkgDelta
  return a.file_name.localeCompare(b.file_name)
}

function iconDownload() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path d="M10 2a1 1 0 0 1 1 1v7.586l1.293-1.293a1 1 0 1 1 1.414 1.414l-3 3a1 1 0 0 1-1.414 0l-3-3a1 1 0 1 1 1.414-1.414L9 10.586V3a1 1 0 0 1 1-1Z" />
      <path d="M3 13a1 1 0 0 1 1 1v1a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-1a1 1 0 1 1 2 0v1a3 3 0 0 1-3 3H5a3 3 0 0 1-3-3v-1a1 1 0 0 1 1-1Z" />
    </svg>
  )
}

export function ConnectorVersionsPage() {
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [data, setData] = useState<ConnectorReleaseVersionsResponse | null>(null)

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await getConnectorReleaseVersions()
      setData(response)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load connector versions')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  const versions = useMemo(() => data?.versions ?? [], [data])

  return (
    <>
      <PageHeader title="Connector Versions">
        <Button size="sm" variant="secondary" onClick={() => void load()}>
          Refresh
        </Button>
      </PageHeader>

      {loading && <LoadingState message="Loading connector versions..." />}
      {error && <ErrorState message={error} />}

      {!loading && !error && (
        <div className="space-y-4">
          {versions.length === 0 && (
            <Card className="p-5 text-sm text-gray-500">
              No published connector versions found.
            </Card>
          )}

          {versions.map((version) => {
            const artifacts = version.artifacts.slice().sort(artifactOrder)
            return (
              <Card key={version.tag}>
                <div className="border-b border-gray-100 px-5 py-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-lg font-semibold text-gray-900">{version.version}</h2>
                    {version.version === data?.latest_version && <Badge color="green">latest</Badge>}
                    {version.version === data?.minimum_version && <Badge color="yellow">minimum</Badge>}
                  </div>
                </div>

                <div className="px-5 py-4">
                  <div className="space-y-2">
                    {artifacts.map((artifact) => (
                      <div key={artifact.file_name} className="flex flex-col gap-2 rounded-lg border border-gray-200 p-3 sm:flex-row sm:items-center sm:justify-between">
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium text-gray-900">{artifact.file_name}</p>
                          <div className="mt-1 flex flex-wrap items-center gap-1.5">
                            <Badge color="indigo">{artifact.os}</Badge>
                            <Badge color="gray">{artifact.arch}</Badge>
                            <Badge color="blue">{artifact.package_type}</Badge>
                          </div>
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                          <Button size="sm" onClick={() => window.open(artifact.download_url, '_blank', 'noopener,noreferrer')}>
                            <span className="mr-1">{iconDownload()}</span>
                            Download
                          </Button>
                          {artifact.signature_url && (
                            <Button size="sm" variant="ghost" onClick={() => window.open(artifact.signature_url, '_blank', 'noopener,noreferrer')}>
                              Signature
                            </Button>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>

                  <div className="mt-3 flex flex-wrap items-center gap-2">
                    {version.checksum_file_url && (
                      <Button size="sm" variant="ghost" onClick={() => window.open(version.checksum_file_url, '_blank', 'noopener,noreferrer')}>
                        Checksums
                      </Button>
                    )}
                    {version.checksum_sig_url && (
                      <Button size="sm" variant="ghost" onClick={() => window.open(version.checksum_sig_url, '_blank', 'noopener,noreferrer')}>
                        Checksum Sig
                      </Button>
                    )}
                  </div>
                </div>
              </Card>
            )
          })}
        </div>
      )}
    </>
  )
}
