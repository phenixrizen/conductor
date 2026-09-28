import type { TerminalTransport } from '~/utils/transport/types'
import { WebSocketTransport } from '~/utils/transport/ws'
import { WebRTCTransport } from '~/utils/transport/webrtc'
import type { SessionKind } from './useSessions'

export interface TransportSpec {
  sessionId: string
  token: string
  kind: SessionKind
  forceRelay?: boolean
  /** Display name sent in the hello; defaults to the stored identity. */
  name?: string
}

/** Builds the right transport for a session kind. Each call creates a fresh connection object. */
export function useTerminalTransport() {
  const { wsBase } = useApiBase()

  const identity = useIdentity()

  function create(spec: TransportSpec): TerminalTransport {
    const url = `${wsBase.value}/ws/sessions/${encodeURIComponent(spec.sessionId)}?token=${encodeURIComponent(spec.token)}`
    const name = spec.name ?? identity.name.value
    if (spec.kind === 'hosted') return new WebRTCTransport(url, { forceRelay: spec.forceRelay, name })
    return new WebSocketTransport(url, { name })
  }

  return { create }
}
