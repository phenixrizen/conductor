import { encodeText } from '~/utils/protocol'
import type { SessionInfo } from './useSessions'

/**
 * Sends input to a session without opening its terminal: a short-lived
 * control connection over the existing transports (relay for hosted sessions,
 * so no WebRTC negotiation). The attach uses the session's own size so the
 * PTY is not resized by the round trip.
 */
export function useQuickReply() {
  const { create } = useTerminalTransport()
  const admin = useAdminToken()
  const sending = useState<Set<string>>('quickReplySending', () => new Set())

  function mark(id: string, on: boolean) {
    const next = new Set(sending.value)
    if (on) next.add(id)
    else next.delete(id)
    sending.value = next
  }

  async function send(session: SessionInfo, text: string, opts: { token?: string } = {}): Promise<void> {
    if (sending.value.has(session.id)) return
    mark(session.id, true)
    const t = create({ sessionId: session.id, token: opts.token ?? admin.token.value, kind: session.kind, forceRelay: true })
    try {
      const welcome = await t.connect({ cols: session.cols || 80, rows: session.rows || 24 })
      if (welcome.role !== 'control') throw new Error('This link is view-only')
      t.sendInput(encodeText(text))
      await new Promise((r) => setTimeout(r, 150))
    } finally {
      t.close()
      mark(session.id, false)
    }
  }

  return { send, sending }
}
