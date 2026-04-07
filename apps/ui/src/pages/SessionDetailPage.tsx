import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { getSessionDetail, getSessionEvents, getSessionReplay } from '../api'
import type { SessionDetail, SessionEvent, SessionReplayChunk, SessionReplayResponse } from '../types'
import { Badge, Button, Card, CardBody, CardHeader, EmptyRow, ErrorState, InfoRow, LoadingState, PageHeader, statusColor, Table, Td, Th } from '../components/ui'

type SessionTab = 'timeline' | 'transcript' | 'replay'

type TranscriptChunk = {
  id: number
  event_time: string
  source: 'in' | 'out'
  text: string
  stream?: string
  size?: number
}

const REPLAY_SPEEDS = [0.5, 1, 2, 4]

export function SessionDetailPage() {
  const { sessionID = '' } = useParams<{ sessionID: string }>()
  const [detail, setDetail] = useState<SessionDetail | null>(null)
  const [events, setEvents] = useState<SessionEvent[]>([])
  const [replay, setReplay] = useState<SessionReplayResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [tab, setTab] = useState<SessionTab>('timeline')
  const [search, setSearch] = useState('')
  const [sourceFilter, setSourceFilter] = useState<'all' | 'in' | 'out'>('all')
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState(1)
  const [cursor, setCursor] = useState(0)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)
      try {
        const [detailResp, eventsResp, replayResp] = await Promise.all([
          getSessionDetail(sessionID),
          getSessionEvents(sessionID, { limit: 1000 }),
          getSessionReplay(sessionID, { limit: 1000 }),
        ])
        if (!cancelled) {
          setDetail(detailResp)
          setEvents(eventsResp.items)
          setReplay(replayResp)
          setTab(detailResp.action === 'shell' ? 'transcript' : 'timeline')
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : 'failed to load session detail'
          setError(message)
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }

    if (sessionID !== '') {
      void load()
    } else {
      setLoading(false)
      setError('missing session id')
    }

    return () => {
      cancelled = true
    }
  }, [sessionID])

  const shellSession = detail?.action === 'shell'

  const transcriptChunks = useMemo(() => {
    const chunks: TranscriptChunk[] = []
    for (const event of events) {
      if (event.event_type !== 'data_in' && event.event_type !== 'data_out') {
        continue
      }
      const source = event.event_type === 'data_in' ? 'in' : 'out'
      const text = event.transcript?.text ?? decodeEventText(event.payload)
      if (text === '') {
        continue
      }
      chunks.push({
        id: event.id,
        event_time: event.event_time,
        source,
        text,
        stream: event.transcript?.stream,
        size: event.transcript?.size,
      })
    }
    return chunks
  }, [events])

  const filteredChunks = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return transcriptChunks.filter((chunk) => {
      if (sourceFilter !== 'all' && chunk.source !== sourceFilter) {
        return false
      }
      if (needle === '') {
        return true
      }
      return chunk.text.toLowerCase().includes(needle)
    })
  }, [transcriptChunks, search, sourceFilter])

  const replayChunks = useMemo(() => {
    if (replay?.supported === true && replay.items.length > 0) {
      return replay.items
    }
    return transcriptChunks.map((chunk) => ({
      event_id: chunk.id,
      event_time: chunk.event_time,
      event_type: chunk.source === 'in' ? 'input' : 'output',
      direction: chunk.source,
      stream: chunk.stream,
      size: chunk.size,
      text: chunk.text,
      offset_sec: 0,
      delay_sec: 0,
    })) as SessionReplayChunk[]
  }, [replay, transcriptChunks])

  useEffect(() => {
    if (!playing || replayChunks.length === 0) {
      return
    }
    if (cursor >= replayChunks.length) {
      setPlaying(false)
      return
    }
    const delaySec = replayChunks[cursor]?.delay_sec ?? 0
    const timeoutMs = Math.max(20, Math.min(2000, Math.floor((delaySec * 1000) / Math.max(speed, 0.1))))
    const timer = window.setTimeout(() => {
      setCursor((prev) => {
        if (prev + 1 >= replayChunks.length) {
          setPlaying(false)
          return replayChunks.length
        }
        return prev + 1
      })
    }, timeoutMs)
    return () => {
      window.clearTimeout(timer)
    }
  }, [playing, speed, cursor, replayChunks])

  useEffect(() => {
    setPlaying(false)
    setCursor(0)
  }, [sessionID])

  const replayText = useMemo(() => {
    return replayChunks
      .slice(0, cursor)
      .map((chunk) => chunk.text ?? '')
      .join('')
  }, [cursor, replayChunks])

  const resizeCount = useMemo(() => replayChunks.filter((chunk) => chunk.event_type === 'resize').length, [replayChunks])

  const connectorEvents = useMemo(() => {
    return events.filter((event) => event.event_type.startsWith('connector_launch_'))
  }, [events])

  const finalLifecycleEvent = useMemo(() => {
    return [...events]
      .reverse()
      .find((event) => event.event_type === 'session_ended' || event.event_type === 'session_failed')
  }, [events])

  return (
    <>
      <PageHeader title="Session Detail">
        {detail?.session_id && (
          <>
            <a
              href={`/api/sessions/${detail.session_id}/export/summary`}
              className="rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              Export JSON
            </a>
            {detail.action === 'shell' && (
              <a
                href={`/api/sessions/${detail.session_id}/export/transcript`}
                className="rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                Export Transcript
              </a>
            )}
          </>
        )}
      </PageHeader>

      {loading && <LoadingState message="Loading session detail..." />}
      {error && <ErrorState message={error} />}

      {!loading && !error && detail && (
        <div className="space-y-4">
          <Card>
            <CardHeader title="Summary" />
            <CardBody>
              <div className="grid gap-x-8 gap-y-1 sm:grid-cols-2">
                <InfoRow label="Session" value={<span className="font-mono text-xs">{detail.session_id}</span>} />
                <InfoRow label="User" value={detail.user.username} />
                <InfoRow label="Asset" value={`${detail.asset.name} (${detail.asset.asset_type})`} />
                <InfoRow label="Action" value={<Badge color="indigo">{detail.action} ({detail.launch_type})</Badge>} />
                <InfoRow label="Status" value={<Badge color={statusColor(detail.status)}>{detail.status}</Badge>} />
                <InfoRow label="Created" value={new Date(detail.created_at).toLocaleString()} />
                <InfoRow label="Started" value={detail.started_at ? new Date(detail.started_at).toLocaleString() : '-'} />
                <InfoRow label="Ended" value={detail.ended_at ? new Date(detail.ended_at).toLocaleString() : '-'} />
                <InfoRow label="Duration" value={detail.duration_seconds === undefined ? '-' : `${detail.duration_seconds}s`} />
                <InfoRow label="Events" value={String(detail.lifecycle.event_count ?? 0)} />
              </div>
            </CardBody>
          </Card>

          {shellSession ? (
            <Card>
              <CardHeader title="Shell Review">
                <p className="text-xs text-gray-400">Timed stream replay with terminal resize events (asciicast-style shaping)</p>
              </CardHeader>
              <CardBody>
                <div className="mb-4 flex gap-1">
                  {(['transcript', 'replay', 'timeline'] as const).map((t) => (
                    <button
                      key={t}
                      onClick={() => setTab(t)}
                      className={`rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
                        tab === t
                          ? 'bg-indigo-600 text-white'
                          : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
                      }`}
                    >
                      {t.charAt(0).toUpperCase() + t.slice(1)}
                    </button>
                  ))}
                </div>

                {tab === 'transcript' && (
                  <>
                    <div className="mb-3 flex flex-wrap items-end gap-3">
                      <label className="block">
                        <span className="mb-1 block text-xs font-medium text-gray-500">Search</span>
                        <input
                          value={search}
                          onChange={(e) => setSearch(e.target.value)}
                          placeholder="filter text..."
                          className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                        />
                      </label>
                      <label className="block">
                        <span className="mb-1 block text-xs font-medium text-gray-500">Source</span>
                        <select
                          value={sourceFilter}
                          onChange={(e) => setSourceFilter(e.target.value as 'all' | 'in' | 'out')}
                          className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                        >
                          <option value="all">All</option>
                          <option value="in">In</option>
                          <option value="out">Out</option>
                        </select>
                      </label>
                    </div>

                    <Table>
                      <thead>
                        <tr>
                          <Th>Time</Th>
                          <Th>Source</Th>
                          <Th>Text</Th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-100">
                        {filteredChunks.map((chunk) => (
                          <tr key={chunk.id} className="hover:bg-gray-50">
                            <Td>{new Date(chunk.event_time).toLocaleString()}</Td>
                            <Td><Badge color={chunk.source === 'in' ? 'blue' : 'green'}>{chunk.source}</Badge></Td>
                            <Td mono className="max-w-md truncate">{trimText(chunk.text)}</Td>
                          </tr>
                        ))}
                        {filteredChunks.length === 0 && <EmptyRow colSpan={3} message="No shell transcript chunks found." />}
                      </tbody>
                    </Table>
                  </>
                )}

                {tab === 'replay' && (
                  <>
                    <div className="mb-3 flex flex-wrap items-center gap-3">
                      <Button size="sm" onClick={() => setPlaying((prev) => !prev)}>
                        {playing ? 'Pause' : 'Play'}
                      </Button>
                      <Button size="sm" variant="secondary" onClick={() => { setPlaying(false); setCursor(0) }}>
                        Reset
                      </Button>
                      <label className="flex items-center gap-2 text-sm text-gray-600">
                        Speed
                        <select
                          value={speed}
                          onChange={(e) => setSpeed(Number(e.target.value))}
                          className="rounded border border-gray-300 px-2 py-1 text-sm"
                        >
                          {REPLAY_SPEEDS.map((v) => <option key={v} value={v}>{v}x</option>)}
                        </select>
                      </label>
                      <input
                        type="range"
                        min={0}
                        max={replayChunks.length}
                        value={cursor}
                        onChange={(e) => setCursor(Number(e.target.value))}
                        className="w-40"
                      />
                      <span className="text-xs text-gray-400">{cursor}/{replayChunks.length}</span>
                      <span className="text-xs text-gray-400">resize events: {resizeCount}</span>
                    </div>

                    <div className="rounded-lg border border-gray-200 bg-gray-900 p-4">
                      <pre className="max-h-64 overflow-auto font-mono text-sm text-green-400 whitespace-pre-wrap">
                        {replayText || '(no replay output yet)'}
                      </pre>
                    </div>
                  </>
                )}
              </CardBody>
            </Card>
          ) : (
            <Card>
              <CardHeader title={`${detail.action.toUpperCase()} Review`}>
                <p className="text-xs text-gray-400">Launch lifecycle and final outcome</p>
              </CardHeader>
              <CardBody>
                <div className="mb-4 grid gap-x-8 gap-y-1 sm:grid-cols-2">
                  <InfoRow label="Connector Requested" value={detail.lifecycle.connector_requested ? 'Yes' : 'No'} />
                  <InfoRow label="Connector Succeeded" value={detail.lifecycle.connector_succeeded ? 'Yes' : 'No'} />
                  <InfoRow label="Connector Failed" value={detail.lifecycle.connector_failed ? 'Yes' : 'No'} />
                  <InfoRow label="Final Outcome" value={finalLifecycleEvent?.event_type ?? detail.status} />
                </div>

                <Table>
                  <thead>
                    <tr>
                      <Th>Time</Th>
                      <Th>Event</Th>
                      <Th>Metadata</Th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {connectorEvents.map((event) => (
                      <tr key={event.id} className="hover:bg-gray-50">
                        <Td>{new Date(event.event_time).toLocaleString()}</Td>
                        <Td><Badge>{event.event_type}</Badge></Td>
                        <Td mono className="max-w-md truncate">{summarizePayload(event)}</Td>
                      </tr>
                    ))}
                    {connectorEvents.length === 0 && <EmptyRow colSpan={3} message="No connector launch metadata events recorded." />}
                  </tbody>
                </Table>
              </CardBody>
            </Card>
          )}

          {(tab === 'timeline' || !shellSession) && (
            <Card>
              <CardHeader title="Event Timeline" />
              <Table>
                <thead>
                  <tr>
                    <Th>ID</Th>
                    <Th>Time</Th>
                    <Th>Event</Th>
                    <Th>Actor</Th>
                    <Th>Payload</Th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {events.map((event) => (
                    <tr key={event.id} className="hover:bg-gray-50">
                      <Td>{event.id}</Td>
                      <Td>{new Date(event.event_time).toLocaleString()}</Td>
                      <Td><Badge>{event.event_type}</Badge></Td>
                      <Td>{event.actor_user?.username || '-'}</Td>
                      <Td mono className="max-w-md truncate">{summarizePayload(event)}</Td>
                    </tr>
                  ))}
                  {events.length === 0 && <EmptyRow colSpan={5} message="No events recorded." />}
                </tbody>
              </Table>
            </Card>
          )}
        </div>
      )}
    </>
  )
}

function decodeEventText(payload: Record<string, unknown>): string {
  const encoded = payload.data
  if (typeof encoded !== 'string') {
    return ''
  }
  try {
    return atob(encoded)
  } catch {
    return ''
  }
}

function trimText(value: string): string {
  if (value.length <= 240) {
    return value
  }
  return value.slice(0, 240) + '...'
}

function summarizePayload(event: SessionEvent): string {
  if (event.transcript?.text) {
    const preview = trimText(event.transcript.text)
    return `${event.transcript.direction} ${preview}`
  }
  if (event.event_type === 'data_in' || event.event_type === 'data_out') {
    const stream = typeof event.payload.stream === 'string' ? event.payload.stream : ''
    const size = typeof event.payload.size === 'number' ? String(event.payload.size) : '?'
    return `stream=${stream || '-'} size=${size}`
  }

  try {
    const raw = JSON.stringify(event.payload)
    if (!raw) {
      return '{}'
    }
    if (raw.length <= 160) {
      return raw
    }
    return raw.slice(0, 160) + '...'
  } catch {
    return '[payload]'
  }
}
