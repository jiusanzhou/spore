import { useEffect, useRef, useState } from 'react'
import type { SwarmState } from './types'

/**
 * Subscribe to /api/events SSE stream.
 *
 * Reconnects with linear backoff (1s) on close. Yields the latest full state,
 * a `connected` flag, and the last update timestamp (ms). The Go side resends
 * a full snapshot on every tick — there are no incremental events — so we just
 * replace state on each frame.
 */
export function useSSE(url = '/api/events') {
  const [state, setState] = useState<SwarmState | null>(null)
  const [connected, setConnected] = useState(false)
  const [lastUpdate, setLastUpdate] = useState<number>(0)
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    let cancelled = false
    let reconnectTimer: number | undefined

    const connect = () => {
      if (cancelled) return
      const es = new EventSource(url)
      esRef.current = es
      es.onopen = () => {
        if (cancelled) return
        setConnected(true)
      }
      es.onmessage = (ev) => {
        if (cancelled) return
        try {
          const parsed = JSON.parse(ev.data) as SwarmState
          setState(parsed)
          setLastUpdate(Date.now())
        } catch {
          // ignore malformed frames
        }
      }
      es.onerror = () => {
        if (cancelled) return
        setConnected(false)
        es.close()
        esRef.current = null
        reconnectTimer = window.setTimeout(connect, 1000)
      }
    }
    connect()

    return () => {
      cancelled = true
      if (reconnectTimer) window.clearTimeout(reconnectTimer)
      esRef.current?.close()
    }
  }, [url])

  return { state, connected, lastUpdate }
}
