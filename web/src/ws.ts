import { useEffect, useRef } from 'react'

export interface LiveEvent {
  type: string
  payload?: unknown
}

// useLive subscribes to control-plane events and invokes the callback for
// every event whose type matches one of the given prefixes ('*' = all).
// Reconnects automatically.
export function useLive(prefixes: string[], onEvent: (ev: LiveEvent) => void) {
  const cb = useRef(onEvent)
  cb.current = onEvent

  useEffect(() => {
    let ws: WebSocket | null = null
    let closed = false
    let retry: ReturnType<typeof setTimeout>

    const connect = () => {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${location.host}/api/ws`)
      ws.onmessage = (msg) => {
        try {
          const ev: LiveEvent = JSON.parse(msg.data)
          if (prefixes.includes('*') || prefixes.some((p) => ev.type.startsWith(p))) {
            cb.current(ev)
          }
        } catch {
          /* ignore malformed frames */
        }
      }
      ws.onclose = () => {
        if (!closed) retry = setTimeout(connect, 2000)
      }
    }
    connect()
    return () => {
      closed = true
      clearTimeout(retry)
      ws?.close()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [prefixes.join(',')])
}
