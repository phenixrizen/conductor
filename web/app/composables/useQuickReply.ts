import { encodeText, FOLLOW_SIZE } from '~/utils/protocol'
import type { TerminalTransport } from '~/utils/transport/types'
import type { SessionInfo } from './useSessions'

/**
 * Sends to a session without opening its terminal: a short-lived control connection over the existing transports
 * (relay for hosted sessions, so no WebRTC negotiation). The hello follows the session's size (FOLLOW_SIZE), so the
 * round trip never resizes it. `reply` submits a line (the owner types it and presses Enter 250 ms later, which a TUI
 * takes as Enter); `send` writes keys as they are, for a quick-reply option such as a digit.
 */
export function useQuickReply() {
  const { create } = useTerminalTransport()
  const admin = useWorkbenchToken()
  const sending = useState<Set<string>>('quickReplySending', () => new Set())

  function mark(id: string, on: boolean) {
    const next = new Set(sending.value)
    if (on) next.add(id)
    else next.delete(id)
    sending.value = next
  }

  async function over(session: Pick<SessionInfo, 'id' | 'kind'>, token: string | undefined, act: (t: TerminalTransport) => void, server?: string): Promise<void> {
    if (sending.value.has(session.id)) return
    mark(session.id, true)
    const t = create({ sessionId: session.id, token: token ?? admin.token.value, kind: session.kind, forceRelay: true, server })
    try {
      const welcome = await t.connect(FOLLOW_SIZE)
      if (welcome.role !== 'control') throw new Error('This link is view-only')
      act(t)
      // The owner finishes a submission after the connection goes; this only lets the frame leave.
      await new Promise((r) => setTimeout(r, 150))
    } finally {
      t.close()
      mark(session.id, false)
    }
  }

  function send(session: Pick<SessionInfo, 'id' | 'kind'>, input: string, opts: { token?: string; server?: string } = {}): Promise<void> {
    return over(session, opts.token, (t) => t.sendInput(encodeText(input)), opts.server)
  }

  function reply(session: Pick<SessionInfo, 'id' | 'kind'>, text: string, opts: { token?: string; server?: string } = {}): Promise<void> {
    return over(session, opts.token, (t) => t.submit(text), opts.server)
  }

  return { send, reply, sending }
}
