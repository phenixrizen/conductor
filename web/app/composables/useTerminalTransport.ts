import type { TerminalTransport } from '~/utils/transport/types'
import { WebSocketTransport } from '~/utils/transport/ws'
import { WebRTCTransport } from '~/utils/transport/webrtc'
import type { SessionKind } from './useSessions'

export interface TransportSpec {
  sessionId: string
  token: string
  kind: SessionKind
  forceRelay?: boolean
}

/** Builds the right transport for a session kind. Each call creates a fresh connection object. */
export function useTerminalTransport() {
  const { wsBase } = useApiBase()

  function create(spec: TransportSpec): TerminalTransport {
    const url = `${wsBase.value}/ws/sessions/${encodeURIComponent(spec.sessionId)}?token=${encodeURIComponent(spec.token)}`
    if (spec.kind === 'hosted') return new WebRTCTransport(url, { forceRelay: spec.forceRelay })
    return new WebSocketTransport(url)
  }

  return { create }
}
