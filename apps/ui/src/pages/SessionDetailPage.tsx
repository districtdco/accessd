import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getSessionDetail, getSessionEvents, getSessionReplay } from '../api'
import type { SessionDetail, SessionEvent, SessionReplayChunk, SessionReplayResponse } from '../types'
import { Badge, Button, Card, CardBody, CardHeader, EmptyRow, ErrorState, InfoRow, LoadingState, PageHeader, Table, Td, Th, statusColor } from '../components/ui'
import { TerminalReplay } from '../components/TerminalReplay'

type SessionTab = 'timeline' | 'transcript' | 'replay' | 'queries' | 'commands' | 'files'

type TranscriptChunk = {
  id: string
  event_time: string
  source: 'in' | 'out'
  text: string
}

type DBQueryItem = {
  id: number
  event_time: string
  query: string
  engine: string
  protocol_type: string
  prepared: boolean
}

type RedisCommandItem = {
  id: number
  event_time: string
  command: string
  args_summary: string[]
  dangerous: boolean
}

type SFTPOperationItem = {
  id: number
  event_time: string
  operation: string
  path: string
  path_to?: string
  size?: number
}

const REPLAY_SPEEDS = [0.5, 1, 2, 4]
const PAGE_SIZE = 200

export function SessionDetailPage() {
  const { sessionID = '' } = useParams<{ sessionID: string }>()
  const [detail, setDetail] = useState<SessionDetail | null>(null)
  const [events, setEvents] = useState<SessionEvent[]>([])
  const [eventsNextAfter, setEventsNextAfter] = useState<number | undefined>(undefined)
  const [replay, setReplay] = useState<SessionReplayResponse | null>(null)
  const [replayNextAfter, setReplayNextAfter] = useState<number | undefined>(undefined)
  const [loading, setLoading] = useState(true)
  const [loadingMoreEvents, setLoadingMoreEvents] = useState(false)
  const [loadingMoreReplay, setLoadingMoreReplay] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [tab, setTab] = useState<SessionTab>('timeline')
  const [search, setSearch] = useState('')
  const [sourceFilter, setSourceFilter] = useState<'all' | 'in' | 'out'>('all')
  const [queryProtocolFilter, setQueryProtocolFilter] = useState<'all' | 'simple' | 'extended' | 'prepared' | 'rpc'>('all')
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState(1)
  const [cursor, setCursor] = useState(0)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      setLoading(true)
      setError(null)
      setEvents([])
      setReplay(null)
      setEventsNextAfter(undefined)
      setReplayNextAfter(undefined)
      try {
        const detailResp = await getSessionDetail(sessionID)
        const eventsResp = await getSessionEvents(sessionID, { limit: PAGE_SIZE })
        if (cancelled) {
          return
        }

        setDetail(detailResp)
        setEvents(eventsResp.items)
        setEventsNextAfter(eventsResp.next_after_id)

        if (detailResp.action === 'shell') {
          const replayResp = await getSessionReplay(sessionID, { limit: PAGE_SIZE })
          if (cancelled) {
            return
          }
          setReplay(replayResp)
          setReplayNextAfter(replayResp.next_after_id)
        }
        setTab(defaultTab(detailResp.action))
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

  const loadMoreEvents = async () => {
    if (!eventsNextAfter || loadingMoreEvents) {
      return
    }
    setLoadingMoreEvents(true)
    try {
      const resp = await getSessionEvents(sessionID, { after_id: eventsNextAfter, limit: PAGE_SIZE })
      setEvents((prev) => appendEvents(prev, resp.items))
      setEventsNextAfter(resp.next_after_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load more events')
    } finally {
      setLoadingMoreEvents(false)
    }
  }

  const loadMoreReplay = async () => {
    if (!replayNextAfter || loadingMoreReplay) {
      return
    }
    setLoadingMoreReplay(true)
    try {
      const resp = await getSessionReplay(sessionID, { after_id: replayNextAfter, limit: PAGE_SIZE })
      setReplay((prev) => ({
        session_id: prev?.session_id || resp.session_id,
        supported: prev?.supported ?? resp.supported,
        approximate: prev?.approximate ?? resp.approximate,
        items: appendReplay(prev?.items ?? [], resp.items),
        next_after_id: resp.next_after_id,
      }))
      setReplayNextAfter(resp.next_after_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load more replay chunks')
    } finally {
      setLoadingMoreReplay(false)
    }
  }

  const shellSession = detail?.action === 'shell'

  const transcriptChunks = useMemo(() => buildNormalizedTranscript(events), [events])

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
    return events
      .filter((event) => event.event_type === 'data_in' || event.event_type === 'data_out' || event.event_type === 'terminal_resize')
      .map((event) => {
        if (event.event_type === 'terminal_resize') {
          return {
            event_id: event.id,
            event_time: event.event_time,
            event_type: 'resize',
            offset_sec: 0,
            delay_sec: 0,
            cols: readNumber(event.payload.cols),
            rows: readNumber(event.payload.rows),
          } as SessionReplayChunk
        }
        const source = event.event_type === 'data_in' ? 'in' : 'out'
        return {
          event_id: event.id,
          event_time: event.event_time,
          event_type: source === 'in' ? 'input' : 'output',
          direction: source,
          text: decodeEventText(event.payload),
          offset_sec: 0,
          delay_sec: 0,
        } as SessionReplayChunk
      })
  }, [replay, events])

  const dbQueries = useMemo(() => {
    const rows: DBQueryItem[] = []
    for (const event of events) {
      if (event.event_type !== 'db_query') {
        continue
      }
      const query = readString(event.payload.query)
      if (!query) {
        continue
      }
      rows.push({
        id: event.id,
        event_time: readString(event.payload.event_time) || event.event_time,
        query,
        engine: readString(event.payload.engine) || 'database',
        protocol_type: readString(event.payload.protocol_type) || '-',
        prepared: event.payload.prepared === true,
      })
    }
    return rows
  }, [events])

  const filteredQueries = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return dbQueries.filter((item) => {
      if (queryProtocolFilter === 'prepared' && !item.prepared) {
        return false
      }
      if (queryProtocolFilter !== 'all' && queryProtocolFilter !== 'prepared' && item.protocol_type !== queryProtocolFilter) {
        return false
      }
      if (!needle) {
        return true
      }
      return item.query.toLowerCase().includes(needle)
    })
  }, [dbQueries, search, queryProtocolFilter])

  const redisCommands = useMemo(() => {
    const rows: RedisCommandItem[] = []
    for (const event of events) {
      if (event.event_type !== 'redis_command') {
        continue
      }
      rows.push({
        id: event.id,
        event_time: readString(event.payload.event_time) || event.event_time,
        command: readString(event.payload.command) || '-',
        args_summary: readStringArray(event.payload.args_summary),
        dangerous: event.payload.dangerous === true,
      })
    }
    return rows
  }, [events])

  const filteredRedisCommands = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return redisCommands.filter((item) => {
      if (!needle) {
        return true
      }
      const joined = [item.command, ...item.args_summary].join(' ').toLowerCase()
      return joined.includes(needle)
    })
  }, [redisCommands, search])

  const sftpOps = useMemo(() => {
    const rows: SFTPOperationItem[] = []
    for (const event of events) {
      if (event.event_type !== 'file_operation') {
        continue
      }
      rows.push({
        id: event.id,
        event_time: readString(event.payload.event_time) || event.event_time,
        operation: readString(event.payload.operation) || '-',
        path: readString(event.payload.path) || '-',
        path_to: readString(event.payload.path_to),
        size: readNumber(event.payload.size),
      })
    }
    return rows
  }, [events])

  const filteredSFTPOps = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return sftpOps.filter((item) => {
      if (!needle) {
        return true
      }
      return [item.operation, item.path, item.path_to ?? ''].join(' ').toLowerCase().includes(needle)
    })
  }, [sftpOps, search])

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
    setSearch('')
    setSourceFilter('all')
    setQueryProtocolFilter('all')
  }, [sessionID])

  const resizeCount = useMemo(() => replayChunks.filter((chunk) => chunk.event_type === 'resize').length, [replayChunks])

  const connectorEvents = useMemo(() => {
    return events.filter((event) => event.event_type.startsWith('connector_launch_'))
  }, [events])

  const finalLifecycleEvent = useMemo(() => {
    return [...events]
      .reverse()
      .find((event) => event.event_type === 'session_ended' || event.event_type === 'session_failed')
  }, [events])

  const failureReason = useMemo(() => {
    if (!finalLifecycleEvent || finalLifecycleEvent.event_type !== 'session_failed') {
      return ''
    }
    const reason = readString(finalLifecycleEvent.payload.reason)
    if (reason === 'launch_materialization_timeout') {
      return 'Launch accepted but no client/proxy connection materialized before timeout.'
    }
    return reason
  }, [finalLifecycleEvent])

  return (
    <>
      <div className="mb-2 flex items-center gap-2 text-sm text-gray-500">
        <Link to="/sessions" className="hover:text-gray-700">Sessions</Link>
        <span>/</span>
        <span className="font-mono text-xs text-gray-700">{sessionID || 'detail'}</span>
      </div>
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
                <InfoRow label="Events Loaded" value={`${events.length}${eventsNextAfter ? ' (partial)' : ''}`} />
                {failureReason !== '' && <InfoRow label="Failure Reason" value={failureReason} />}
              </div>
            </CardBody>
          </Card>

          {shellSession && (
            <Card>
              <CardHeader title="Shell Review">
                <p className="text-xs text-gray-400">Terminal transcript and replay, loaded in pages.</p>
              </CardHeader>
              <CardBody>
                <div className="mb-4 flex gap-1">
                  {(['transcript', 'replay', 'timeline'] as const).map((t) => (
                    <button
                      key={t}
                      onClick={() => setTab(t)}
                      className={`rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
                        tab === t ? 'bg-indigo-600 text-white' : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
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
                        {filteredChunks.length === 0 && <EmptyRow colSpan={3} message="No shell transcript chunks found in loaded pages." />}
                      </tbody>
                    </Table>
                    {eventsNextAfter && (
                      <div className="mt-3">
                        <Button size="sm" variant="secondary" disabled={loadingMoreEvents} onClick={() => void loadMoreEvents()}>
                          {loadingMoreEvents ? 'Loading more...' : 'Load More Transcript'}
                        </Button>
                      </div>
                    )}
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

                    <TerminalReplay chunks={replayChunks} cursor={cursor} />
                    {replayNextAfter && (
                      <div className="mt-3">
                        <Button size="sm" variant="secondary" disabled={loadingMoreReplay} onClick={() => void loadMoreReplay()}>
                          {loadingMoreReplay ? 'Loading more...' : 'Load More Replay Chunks'}
                        </Button>
                      </div>
                    )}
                  </>
                )}
              </CardBody>
            </Card>
          )}

          {!shellSession && detail.action === 'dbeaver' && (
            <Card>
              <CardHeader title="Database Query Replay">
                <p className="text-xs text-gray-400">Chronological query timeline (not video replay).</p>
              </CardHeader>
              <CardBody>
                <div className="mb-4 grid gap-x-8 gap-y-1 sm:grid-cols-2">
                  <InfoRow label="Connector Requested" value={detail.lifecycle.connector_requested ? 'Yes' : 'No'} />
                  <InfoRow label="Connector Succeeded" value={detail.lifecycle.connector_succeeded ? 'Yes' : 'No'} />
                  <InfoRow label="Connector Failed" value={detail.lifecycle.connector_failed ? 'Yes' : 'No'} />
                  <InfoRow label="Final Outcome" value={finalLifecycleEvent?.event_type ?? detail.status} />
                </div>
                <div className="mb-3 flex flex-wrap items-end gap-3">
                  <label className="block">
                    <span className="mb-1 block text-xs font-medium text-gray-500">Search Query</span>
                    <input
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                      placeholder="SELECT users..."
                      className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    />
                  </label>
                  <label className="block">
                    <span className="mb-1 block text-xs font-medium text-gray-500">Protocol</span>
                    <select
                      value={queryProtocolFilter}
                      onChange={(e) => setQueryProtocolFilter(e.target.value as 'all' | 'simple' | 'extended' | 'prepared' | 'rpc')}
                      className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    >
                      <option value="all">All</option>
                      <option value="simple">Simple</option>
                      <option value="extended">Extended</option>
                      <option value="prepared">Prepared Only</option>
                      <option value="rpc">RPC</option>
                    </select>
                  </label>
                </div>
                <Table>
                  <thead>
                    <tr>
                      <Th>Time</Th>
                      <Th>Engine</Th>
                      <Th>Protocol</Th>
                      <Th>Prepared</Th>
                      <Th>Risk</Th>
                      <Th>Query</Th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {filteredQueries.map((item) => (
                      <tr key={item.id} className="hover:bg-gray-50">
                        <Td>{new Date(item.event_time).toLocaleString()}</Td>
                        <Td><Badge>{item.engine}</Badge></Td>
                        <Td>{item.protocol_type || '-'}</Td>
                        <Td>{item.prepared ? 'yes' : 'no'}</Td>
                        <Td><Badge color={isDangerousSQL(item.query) ? 'red' : 'green'}>{isDangerousSQL(item.query) ? 'dangerous' : 'normal'}</Badge></Td>
                        <Td mono className="max-w-md truncate">{trimText(item.query)}</Td>
                      </tr>
                    ))}
                    {filteredQueries.length === 0 && <EmptyRow colSpan={6} message="No db_query events in loaded pages." />}
                  </tbody>
                </Table>
                {eventsNextAfter && (
                  <div className="mt-3">
                    <Button size="sm" variant="secondary" disabled={loadingMoreEvents} onClick={() => void loadMoreEvents()}>
                      {loadingMoreEvents ? 'Loading more...' : 'Load More Query Events'}
                    </Button>
                  </div>
                )}
              </CardBody>
            </Card>
          )}

          {!shellSession && detail.action === 'redis' && (
            <Card>
              <CardHeader title="Redis Command Replay">
                <p className="text-xs text-gray-400">Chronological command timeline (not video replay).</p>
              </CardHeader>
              <CardBody>
                <div className="mb-3">
                  <label className="block">
                    <span className="mb-1 block text-xs font-medium text-gray-500">Search Command/Args</span>
                    <input
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                      placeholder="SET user:1"
                      className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    />
                  </label>
                </div>
                <Table>
                  <thead>
                    <tr>
                      <Th>Time</Th>
                      <Th>Command</Th>
                      <Th>Danger</Th>
                      <Th>Args (redacted summary)</Th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {filteredRedisCommands.map((item) => (
                      <tr key={item.id} className="hover:bg-gray-50">
                        <Td>{new Date(item.event_time).toLocaleString()}</Td>
                        <Td><Badge color="indigo">{item.command}</Badge></Td>
                        <Td><Badge color={item.dangerous ? 'red' : 'green'}>{item.dangerous ? 'dangerous' : 'normal'}</Badge></Td>
                        <Td mono className="max-w-md truncate">{item.args_summary.join(' ') || '-'}</Td>
                      </tr>
                    ))}
                    {filteredRedisCommands.length === 0 && <EmptyRow colSpan={4} message="No redis_command events in loaded pages." />}
                  </tbody>
                </Table>
                {eventsNextAfter && (
                  <div className="mt-3">
                    <Button size="sm" variant="secondary" disabled={loadingMoreEvents} onClick={() => void loadMoreEvents()}>
                      {loadingMoreEvents ? 'Loading more...' : 'Load More Command Events'}
                    </Button>
                  </div>
                )}
              </CardBody>
            </Card>
          )}

          {!shellSession && detail.action === 'sftp' && (
            <Card>
              <CardHeader title="SFTP Operation Replay">
                <p className="text-xs text-gray-400">File operation timeline (not screen/video replay).</p>
              </CardHeader>
              <CardBody>
                <div className="mb-3">
                  <label className="block">
                    <span className="mb-1 block text-xs font-medium text-gray-500">Search Operation/Path</span>
                    <input
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                      placeholder="rename /etc/hosts"
                      className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    />
                  </label>
                </div>
                <Table>
                  <thead>
                    <tr>
                      <Th>Time</Th>
                      <Th>Operation</Th>
                      <Th>Path</Th>
                      <Th>Path To</Th>
                      <Th>Size</Th>
                      <Th>Risk</Th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {filteredSFTPOps.map((item) => (
                      <tr key={item.id} className="hover:bg-gray-50">
                        <Td>{new Date(item.event_time).toLocaleString()}</Td>
                        <Td><Badge>{item.operation}</Badge></Td>
                        <Td mono className="max-w-xs truncate">{item.path || '-'}</Td>
                        <Td mono className="max-w-xs truncate">{item.path_to || '-'}</Td>
                        <Td>{item.size !== undefined ? `${item.size} bytes` : '-'}</Td>
                        <Td><Badge color={isDestructiveSFTPOperation(item.operation) ? 'red' : 'green'}>{isDestructiveSFTPOperation(item.operation) ? 'destructive' : 'normal'}</Badge></Td>
                      </tr>
                    ))}
                    {filteredSFTPOps.length === 0 && <EmptyRow colSpan={6} message="No file_operation events in loaded pages." />}
                  </tbody>
                </Table>
                {eventsNextAfter && (
                  <div className="mt-3">
                    <Button size="sm" variant="secondary" disabled={loadingMoreEvents} onClick={() => void loadMoreEvents()}>
                      {loadingMoreEvents ? 'Loading more...' : 'Load More File Operations'}
                    </Button>
                  </div>
                )}
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
              {eventsNextAfter && (
                <CardBody>
                  <Button size="sm" variant="secondary" disabled={loadingMoreEvents} onClick={() => void loadMoreEvents()}>
                    {loadingMoreEvents ? 'Loading more...' : 'Load More Timeline Events'}
                  </Button>
                </CardBody>
              )}
            </Card>
          )}

          {!shellSession && connectorEvents.length > 0 && (
            <Card>
              <CardHeader title="Connector Lifecycle" />
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
                </tbody>
              </Table>
            </Card>
          )}
        </div>
      )}
    </>
  )
}

function defaultTab(action: string): SessionTab {
  switch (action) {
    case 'shell':
      return 'transcript'
    case 'dbeaver':
      return 'queries'
    case 'redis':
      return 'commands'
    case 'sftp':
      return 'files'
    default:
      return 'timeline'
  }
}

function appendEvents(prev: SessionEvent[], next: SessionEvent[]): SessionEvent[] {
  if (next.length === 0) {
    return prev
  }
  const seen = new Set(prev.map((item) => item.id))
  const merged = [...prev]
  for (const item of next) {
    if (seen.has(item.id)) {
      continue
    }
    merged.push(item)
  }
  return merged
}

function appendReplay(prev: SessionReplayChunk[], next: SessionReplayChunk[]): SessionReplayChunk[] {
  if (next.length === 0) {
    return prev
  }
  const seen = new Set(prev.map((item) => item.event_id))
  const merged = [...prev]
  for (const item of next) {
    if (seen.has(item.event_id)) {
      continue
    }
    merged.push(item)
  }
  return merged
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

function buildNormalizedTranscript(events: SessionEvent[]): TranscriptChunk[] {
  const rows: TranscriptChunk[] = []
  const pendingEchoes: string[] = []
  let inputBuffer = ''
  for (const event of events) {
    if (event.event_type !== 'data_in' && event.event_type !== 'data_out') {
      continue
    }
    const raw = decodeEventText(event.payload)
    if (raw === '') {
      continue
    }
    if (event.event_type === 'data_in') {
      const parsed = consumeInputChunk(raw, inputBuffer)
      inputBuffer = parsed.buffer
      parsed.commands.forEach((command, idx) => {
        const cleaned = command.trim()
        if (cleaned === '') {
          return
        }
        rows.push({
          id: `${event.id}:in:${idx}`,
          event_time: event.event_time,
          source: 'in',
          text: cleaned,
        })
        pendingEchoes.push(cleaned)
        if (pendingEchoes.length > 32) {
          pendingEchoes.shift()
        }
      })
      continue
    }

    const outputLines = normalizeOutputChunk(raw)
    outputLines.forEach((line, idx) => {
      const cleaned = line.trim()
      if (cleaned === '') {
        return
      }
      if (pendingEchoes.length > 0 && cleaned === pendingEchoes[0]) {
        pendingEchoes.shift()
        return
      }
      rows.push({
        id: `${event.id}:out:${idx}`,
        event_time: event.event_time,
        source: 'out',
        text: cleaned,
      })
    })
  }
  return rows
}

function consumeInputChunk(chunk: string, buffer: string): { buffer: string; commands: string[] } {
  const commands: string[] = []
  const clean = stripANSI(chunk)
  let current = buffer
  let lastBreak = ''
  for (const ch of clean) {
    if (ch === '\r' || ch === '\n') {
      if (lastBreak === '\r' && ch === '\n') {
        lastBreak = ch
        continue
      }
      if (current.trim() !== '') {
        commands.push(current)
      }
      current = ''
      lastBreak = ch
      continue
    }
    lastBreak = ''
    if (ch === '\b' || ch === '\u007f') {
      current = current.slice(0, -1)
      continue
    }
    if (isControl(ch)) {
      continue
    }
    current += ch
  }
  return { buffer: current, commands }
}

function normalizeOutputChunk(chunk: string): string[] {
  const clean = stripANSI(chunk)
    .replace(/\u001b\[\?2004[hl]/g, '')
    .replace(/\r\n/g, '\n')
  const lines: string[] = []
  let current = ''
  for (const ch of clean) {
    if (ch === '\r') {
      current = ''
      continue
    }
    if (ch === '\n') {
      if (current !== '') {
        lines.push(current)
      }
      current = ''
      continue
    }
    if (ch === '\b' || ch === '\u007f') {
      current = current.slice(0, -1)
      continue
    }
    if (isControl(ch)) {
      continue
    }
    current += ch
  }
  if (current !== '') {
    lines.push(current)
  }
  return lines
}

function stripANSI(value: string): string {
  return value.replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, '').replace(/\u001b\][^\u0007]*(?:\u0007|\u001b\\)/g, '')
}

function isControl(ch: string): boolean {
  if (ch === '\t') {
    return false
  }
  if (ch.length === 0) {
    return false
  }
  const code = ch.charCodeAt(0)
  return code < 0x20
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

function readString(value: unknown): string {
  if (typeof value !== 'string') {
    return ''
  }
  return value.trim()
}

function readStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return []
  }
  return value
    .map((item) => (typeof item === 'string' ? item.trim() : ''))
    .filter((item) => item !== '')
}

function readNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value
  }
  return undefined
}

function isDangerousSQL(query: string): boolean {
  const text = query.toLowerCase()
  return /(drop\s+|truncate\s+|delete\s+|alter\s+|grant\s+|revoke\s+|shutdown\s+|create\s+role)/.test(text)
}

function isDestructiveSFTPOperation(operation: string): boolean {
  const op = operation.toLowerCase().trim()
  return op === 'delete' || op === 'rename' || op === 'rmdir'
}
