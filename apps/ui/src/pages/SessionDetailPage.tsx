import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getSessionDetail, getSessionEvents, getSessionReplay } from '../api'
import { useAuth } from '../auth'
import type { SessionDetail, SessionEvent, SessionReplayChunk, SessionReplayResponse } from '../types'

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
  const { user } = useAuth()

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
      direction: chunk.source,
      stream: chunk.stream,
      size: chunk.size,
      text: chunk.text,
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

    const interval = Math.max(60, Math.floor(350 / speed))
    const timer = window.setInterval(() => {
      setCursor((prev) => {
        if (prev + 1 >= replayChunks.length) {
          window.clearInterval(timer)
          setPlaying(false)
          return replayChunks.length
        }
        return prev + 1
      })
    }, interval)

    return () => {
      window.clearInterval(timer)
    }
  }, [playing, speed, cursor, replayChunks.length])

  useEffect(() => {
    setPlaying(false)
    setCursor(0)
  }, [sessionID])

  const replayText = useMemo(() => {
    return replayChunks
      .slice(0, cursor)
      .map((chunk) => chunk.text)
      .join('')
  }, [cursor, replayChunks])

  const connectorEvents = useMemo(() => {
    return events.filter((event) => event.event_type.startsWith('connector_launch_'))
  }, [events])

  const finalLifecycleEvent = useMemo(() => {
    return [...events]
      .reverse()
      .find((event) => event.event_type === 'session_ended' || event.event_type === 'session_failed')
  }, [events])

  return (
    <main className="page-shell">
      <header className="topbar">
        <div>
          <h1>Session Detail</h1>
          <p className="muted">
            Signed in as <strong>{user?.username}</strong>
          </p>
        </div>
        <div className="actions-inline">
          <Link to="/">My Access</Link>
          <Link to="/sessions">My Sessions</Link>
          {detail?.session_id ? (
            <a href={`/api/sessions/${detail.session_id}/export/summary`}>Export Summary JSON</a>
          ) : null}
          {detail?.action === 'shell' ? (
            <a href={`/api/sessions/${detail.session_id}/export/transcript`}>Export Transcript TXT</a>
          ) : null}
          {user?.roles.includes('admin') || user?.roles.includes('auditor') ? (
            <Link to="/admin/sessions">Admin Sessions</Link>
          ) : null}
        </div>
      </header>

      {loading ? <p>Loading session detail...</p> : null}
      {error === null ? null : <p className="error">{error}</p>}

      {loading === false && error === null && detail !== null ? (
        <>
          <section className="card section-block">
            <h2>Summary</h2>
            <p>
              <strong>Session:</strong> {detail.session_id}
            </p>
            <p>
              <strong>User:</strong> {detail.user.username}
            </p>
            <p>
              <strong>Asset:</strong> {detail.asset.name} ({detail.asset.asset_type})
            </p>
            <p>
              <strong>Action:</strong> {detail.action} ({detail.launch_type})
            </p>
            <p>
              <strong>Status:</strong> {detail.status}
            </p>
            <p>
              <strong>Created:</strong> {new Date(detail.created_at).toLocaleString()}
            </p>
            <p>
              <strong>Started:</strong>{' '}
              {detail.started_at ? new Date(detail.started_at).toLocaleString() : '-'}
            </p>
            <p>
              <strong>Ended:</strong> {detail.ended_at ? new Date(detail.ended_at).toLocaleString() : '-'}
            </p>
            <p>
              <strong>Duration (s):</strong>{' '}
              {detail.duration_seconds === undefined ? '-' : detail.duration_seconds}
            </p>
            <p>
              <strong>Lifecycle:</strong>{' '}
              started={String(detail.lifecycle.started)} ended={String(detail.lifecycle.ended)}
              {' '}failed={String(detail.lifecycle.failed === true)}
              {' '}events={detail.lifecycle.event_count ?? 0}
            </p>
            {detail.lifecycle.first_event_at ? (
              <p>
                <strong>First Event:</strong> {new Date(detail.lifecycle.first_event_at).toLocaleString()}
              </p>
            ) : null}
            {detail.lifecycle.last_event_at ? (
              <p>
                <strong>Last Event:</strong> {new Date(detail.lifecycle.last_event_at).toLocaleString()}
              </p>
            ) : null}
          </section>

          {shellSession ? (
            <section className="card section-block">
              <h2>Shell Review</h2>
              <p className="muted">
                Replay is approximate and event-based. This is not terminal-perfect emulation yet.
              </p>

              <div className="actions-inline tab-row">
                <button
                  className={tab === 'transcript' ? 'button-secondary' : 'button-ghost'}
                  onClick={() => setTab('transcript')}
                >
                  Transcript
                </button>
                <button
                  className={tab === 'replay' ? 'button-secondary' : 'button-ghost'}
                  onClick={() => setTab('replay')}
                >
                  Replay
                </button>
                <button
                  className={tab === 'timeline' ? 'button-secondary' : 'button-ghost'}
                  onClick={() => setTab('timeline')}
                >
                  Timeline
                </button>
              </div>

              {tab === 'transcript' ? (
                <>
                  <div className="actions-inline">
                    <label>
                      Search{' '}
                      <input
                        value={search}
                        onChange={(e) => setSearch(e.target.value)}
                        placeholder="filter transcript text"
                      />
                    </label>
                    <label>
                      Source{' '}
                      <select
                        value={sourceFilter}
                        onChange={(e) => setSourceFilter(e.target.value as 'all' | 'in' | 'out')}
                      >
                        <option value="all">all</option>
                        <option value="in">in</option>
                        <option value="out">out</option>
                      </select>
                    </label>
                  </div>

                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>Time</th>
                          <th>Source</th>
                          <th>Text</th>
                        </tr>
                      </thead>
                      <tbody>
                        {filteredChunks.map((chunk) => (
                          <tr key={chunk.id}>
                            <td>{new Date(chunk.event_time).toLocaleString()}</td>
                            <td>{chunk.source}</td>
                            <td className="mono-cell">{trimText(chunk.text)}</td>
                          </tr>
                        ))}
                        {filteredChunks.length === 0 ? (
                          <tr>
                            <td colSpan={3} className="muted">
                              No shell transcript chunks found.
                            </td>
                          </tr>
                        ) : null}
                      </tbody>
                    </table>
                  </div>
                </>
              ) : null}

              {tab === 'replay' ? (
                <>
                  <div className="actions-inline">
                    <button onClick={() => setPlaying((prev) => !prev)}>
                      {playing ? 'Pause' : 'Play'}
                    </button>
                    <button
                      onClick={() => {
                        setPlaying(false)
                        setCursor(0)
                      }}
                    >
                      Reset
                    </button>
                    <label>
                      Speed{' '}
                      <select value={speed} onChange={(e) => setSpeed(Number(e.target.value))}>
                        {REPLAY_SPEEDS.map((value) => (
                          <option key={value} value={value}>
                            {value}x
                          </option>
                        ))}
                      </select>
                    </label>
                    <label>
                      Position{' '}
                      <input
                        type="range"
                        min={0}
                        max={replayChunks.length}
                        value={cursor}
                        onChange={(e) => setCursor(Number(e.target.value))}
                      />
                    </label>
                    <span className="muted">
                      {cursor}/{replayChunks.length}
                    </span>
                  </div>

                  <div className="transcript-panel">
                    <h3>Replay Output</h3>
                    <pre>{replayText || '(no replay output yet)'}</pre>
                  </div>
                </>
              ) : null}
            </section>
          ) : (
            <section className="card section-block">
              <h2>{detail.action.toUpperCase()} Review</h2>
              <p className="muted">
                {detail.action.toUpperCase()} sessions are metadata-focused in this slice: launch lifecycle and final outcome.
              </p>
              <p>
                <strong>Connector Requested:</strong>{' '}
                {detail.lifecycle.connector_requested ? 'yes' : 'no'}
              </p>
              <p>
                <strong>Connector Succeeded:</strong>{' '}
                {detail.lifecycle.connector_succeeded ? 'yes' : 'no'}
              </p>
              <p>
                <strong>Connector Failed:</strong>{' '}
                {detail.lifecycle.connector_failed ? 'yes' : 'no'}
              </p>
              <p>
                <strong>Final Outcome:</strong> {finalLifecycleEvent?.event_type ?? detail.status}
              </p>

              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Event</th>
                      <th>Metadata</th>
                    </tr>
                  </thead>
                  <tbody>
                    {connectorEvents.map((event) => (
                      <tr key={event.id}>
                        <td>{new Date(event.event_time).toLocaleString()}</td>
                        <td>{event.event_type}</td>
                        <td className="mono-cell">{summarizePayload(event)}</td>
                      </tr>
                    ))}
                    {connectorEvents.length === 0 ? (
                      <tr>
                        <td colSpan={3} className="muted">
                          No connector launch metadata events recorded.
                        </td>
                      </tr>
                    ) : null}
                  </tbody>
                </table>
              </div>
            </section>
          )}

          {(tab === 'timeline' || shellSession === false) ? (
            <section className="card section-block">
              <h2>Event Timeline</h2>
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>ID</th>
                      <th>Time</th>
                      <th>Event</th>
                      <th>Actor</th>
                      <th>Payload Summary</th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.map((event) => (
                      <tr key={event.id}>
                        <td>{event.id}</td>
                        <td>{new Date(event.event_time).toLocaleString()}</td>
                        <td>{event.event_type}</td>
                        <td>{event.actor_user?.username || '-'}</td>
                        <td className="mono-cell">{summarizePayload(event)}</td>
                      </tr>
                    ))}
                    {events.length === 0 ? (
                      <tr>
                        <td colSpan={5} className="muted">
                          No events recorded.
                        </td>
                      </tr>
                    ) : null}
                  </tbody>
                </table>
              </div>
            </section>
          ) : null}
        </>
      ) : null}
    </main>
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
