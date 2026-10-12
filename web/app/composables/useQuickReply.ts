import { FOLLOW_SIZE, QUESTION_ANSWERED } from '~/utils/protocol'
import { repliedQuestions, type QuestionReply } from '~/utils/reply'
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

  function mark(id: string, on: boolean) {
    const next = new Set(sending.value)
    if (on) next.add(id)
    else next.delete(id)
    sending.value = next
  }

  async function over(session: Pick<SessionInfo, 'id' | 'kind'>, token: string | undefined, reply: QuestionReply, server?: string): Promise<void> {
    // One reply per question from here: a second click on one already answered from this tab is not sent.
    if (sending.value.has(session.id) || !replied.take(reply.question)) return
    mark(session.id, true)
    const t: TerminalTransport = create({ sessionId: session.id, token: token ?? admin.token.value, kind: session.kind, forceRelay: true, server })
    try {
      const welcome = await t.connect(FOLLOW_SIZE)
      if (welcome.role !== 'control') throw new Error('This link is view-only')
      // An owner that takes answers says whether it took this one; to an older one, the frame is only let leave: it finishes
      // a submission after the connection goes.
      if (!(await t.answer(reply))) await new Promise((r) => setTimeout(r, 150))
    } catch (e) {
      // Not sent for another reason than its question's being answered: it may be tried again.
      if ((e as { code?: string }).code !== QUESTION_ANSWERED) replied.release(reply.question)
      throw e
    } finally {
      t.close()
      mark(session.id, false)
    }
  }

  function send(session: Pick<SessionInfo, 'id' | 'kind'>, input: string, opts: { token?: string; server?: string; question?: string } = {}): Promise<void> {
    return over(session, opts.token, { question: opts.question, input }, opts.server)
  }

  function reply(session: Pick<SessionInfo, 'id' | 'kind'>, text: string, opts: { token?: string; server?: string; question?: string } = {}): Promise<void> {
    return over(session, opts.token, { question: opts.question, text }, opts.server)
  }

  return { send, reply, sending }
}
