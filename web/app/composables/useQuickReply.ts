import { FOLLOW_SIZE } from '~/utils/protocol'
import { clearAnswered, repliedQuestions, type QuestionReply } from '~/utils/reply'
import type { TerminalTransport } from '~/utils/transport/types'
import type { SessionInfo } from './useSessions'

/** The questions this browser tab has replied to from a row, a card or a chat (the app renders in the browser alone: ssr is off). */
const replied = repliedQuestions()

/**
 * Sends to a session without opening its terminal: a short-lived control connection over the existing transports
 * (relay for hosted sessions, so no WebRTC negotiation). The hello follows the session's size (FOLLOW_SIZE), so the
 * round trip never resizes it. `reply` submits a line (the owner types it and presses Enter 250 ms later, which a TUI
 * takes as Enter); `send` writes keys as they are, for a quick-reply option such as a digit.
 *
 * `question` is the id of the question the person was shown (questionOf): to an owner that takes answers, the reply is
 * taken only while that question still waits, and the promise rejects with a ReplyError (`question_answered`, replyFailure
 * has the words) when another reply answered it first, nothing sent. The connection stays until the owner's word.
 */
export function useQuickReply() {
  const { create } = useTerminalTransport()
  const admin = useWorkbenchToken()
  const sending = useState<Set<string>>('quickReplySending', () => new Set())
  const store = useAttentionStore()

  function mark(id: string, on: boolean) {
    const next = new Set(sending.value)
    if (on) next.add(id)
    else next.delete(id)
    sending.value = next
  }

  async function over(session: Pick<SessionInfo, 'id' | 'kind'>, token: string | undefined, reply: QuestionReply, server?: string): Promise<void> {
    // One reply per question from here: a second click on one already answered from this tab is not sent; one refused may be
    // tried again (repliedQuestions).
    if (sending.value.has(session.id)) return
    try {
      await replied.once(reply.question, async () => {
        mark(session.id, true)
        const t: TerminalTransport = create({ sessionId: session.id, token: token ?? admin.token.value, kind: session.kind, forceRelay: true, server })
        try {
          const welcome = await t.connect(FOLLOW_SIZE)
          if (welcome.role !== 'control') throw new Error('This link is view-only')
          // An owner that takes answers says whether it took this one; to an older one, the frame is only let leave: it finishes
          // a submission after the connection goes.
          if (!(await t.answer(reply))) await new Promise((r) => setTimeout(r, 150))
        } finally {
          t.close()
          mark(session.id, false)
        }
      })
    } catch (e) {
      // The owner says the question is answered: no page offers it any longer (#258), whatever the stream has said so far.
      if ((e as { code?: unknown } | null)?.code === QUESTION_ANSWERED) clearAnswered(store.value.sessions, session.id, reply.question)
      throw e
    }
  }

  function send(session: Pick<SessionInfo, 'id' | 'kind'>, input: string, opts: { token?: string; server?: string; question?: string } = {}): Promise<void> {
    return over(session, opts.token, { question: opts.question, input }, opts.server)
  }

  function reply(session: Pick<SessionInfo, 'id' | 'kind'>, text: string, opts: { token?: string; server?: string; question?: string } = {}): Promise<void> {
    return over(session, opts.token, { question: opts.question, text }, opts.server)
  }

  /**
   * Types a line into a session that is not asking (#303, `I` and the `say` verb): the same short control connection, `submit`
   * for the line, closed once it is typed; the owner writes it and its Enter 250 ms later and goes on if the client has left by
   * then (docs/protocol.md, `submit`). The palette refuses a session waiting on a prompt before calling this, with the
   * broadcast's words.
   */
  async function say(session: Pick<SessionInfo, 'id' | 'kind'>, text: string, opts: { token?: string; server?: string } = {}): Promise<void> {
    if (sending.value.has(session.id)) return
    mark(session.id, true)
    const t: TerminalTransport = create({ sessionId: session.id, token: opts.token ?? admin.token.value, kind: session.kind, forceRelay: true, server: opts.server })
    try {
      const welcome = await t.connect(FOLLOW_SIZE)
      if (welcome.role !== 'control') throw new Error('This link is view-only')
      t.submit(text)
      await new Promise((r) => setTimeout(r, 400))
    } finally {
      t.close()
      mark(session.id, false)
    }
  }

  return { send, reply, say, sending }
}
