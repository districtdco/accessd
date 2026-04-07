import { useEffect, useMemo, useRef } from 'react'
import { Terminal } from 'xterm'
import { FitAddon } from '@xterm/addon-fit'
import 'xterm/css/xterm.css'
import type { SessionReplayChunk } from '../types'

type TerminalReplayProps = {
  chunks: SessionReplayChunk[]
  cursor: number
}

export function TerminalReplay({ chunks, cursor }: TerminalReplayProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)

  const visibleChunks = useMemo(() => chunks.slice(0, Math.max(0, cursor)), [chunks, cursor])

  useEffect(() => {
    if (!containerRef.current) {
      return
    }
    const term = new Terminal({
      convertEol: false,
      disableStdin: true,
      cursorBlink: false,
      scrollback: 5000,
      fontSize: 13,
      fontFamily: 'Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
      theme: {
        background: '#111827',
        foreground: '#e5e7eb',
        cursor: '#9ca3af',
      },
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)
    fitAddon.fit()

    terminalRef.current = term
    fitAddonRef.current = fitAddon

    const observer = new ResizeObserver(() => {
      fitAddon.fit()
    })
    observer.observe(containerRef.current)

    return () => {
      observer.disconnect()
      terminalRef.current = null
      fitAddonRef.current = null
      term.dispose()
    }
  }, [])

  useEffect(() => {
    const term = terminalRef.current
    if (!term) {
      return
    }
    term.reset()
    for (const chunk of visibleChunks) {
      if (chunk.event_type === 'resize') {
        const cols = Math.max(2, Number(chunk.cols ?? 0))
        const rows = Math.max(2, Number(chunk.rows ?? 0))
        if (Number.isFinite(cols) && Number.isFinite(rows)) {
          term.resize(cols, rows)
        }
        continue
      }
      if (chunk.event_type === 'output' && typeof chunk.text === 'string' && chunk.text !== '') {
        term.write(chunk.text)
      }
    }
    term.scrollToBottom()
  }, [visibleChunks])

  return <div ref={containerRef} className="h-80 w-full overflow-hidden rounded-lg border border-gray-700 bg-gray-900" />
}
