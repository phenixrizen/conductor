import { MAX_ANSWER_REQ_ID, MAX_QUESTION_ID, QUESTION_ANSWERED, type AnswerMsg, type Attention } from './protocol'

/**
 * A reply to an agent's question: a choice's keys (`input`), or a line (`text`). `question` is the id of the question the person was
 * shown (the attention's `id`): the owner takes the reply only while that question still waits, so that a click that arrives late
 * never lands on the question that came next. Without one (an older owner's question), whatever shows takes it, as before.
 */
export type QuestionReply = { question?: string; input: string } | { question?: string; text: string }

/** The question a reply to the attention shown names: its id while it waits on input; none otherwise. */
export function questionOf(att: Pick<Attention, 'state' | 'id'> | null | undefined): string | undefined {
  return att?.state === 'needs_input' && att.id ? att.id : undefined
}

/**
 * The attention a page shows when a listing without the question's id (a server older than the host, or one the host has not
 * told yet) reports what its own connection to the owner reported with one: the same question, keeping that id. Anything else
 * shown is `next` as it is.
 */
export function keepQuestionId(next: Attention, shown: Attention | null | undefined): Attention {
  if (next.id || !shown?.id || next.state !== shown.state || (next.message ?? '') !== (shown.message ?? '')) return next
  const a = next.options ?? []
  const b = shown.options ?? []
  if (a.length !== b.length || a.some((o, i) => o.label !== b[i]!.label || o.input !== b[i]!.input)) return next
  return { ...next, id: shown.id }
}

/** A question's or a request's id as the owner takes it: printable ASCII, no space, within its bound. */
function wireId(id: string, max: number): boolean {
  return id.length > 0 && id.length <= max && /^[\x21-\x7e]+$/.test(id)
}

/**
 * The `answer` message a reply goes as, or null when it names no question the owner could take (none, or not an id): it goes as
 * keys or a submit then, as before.
 */
export function answerMessage(reply: QuestionReply, reqId: string): AnswerMsg | null {
  const q = reply.question
  if (!q || !wireId(q, MAX_QUESTION_ID) || !wireId(reqId, MAX_ANSWER_REQ_ID)) return null
  if ('input' in reply) return reply.input ? { t: 'answer', reqId, question: q, input: reply.input } : null
  return reply.text ? { t: 'answer', reqId, question: q, text: reply.text } : null
}

/** How many questions a page remembers having replied to. */
const REMEMBERED = 64

/**
 * The questions a page has sent a reply to: a second click or key on one (a double click, a digit pressed twice before the
 * question leaves the page) is not sent, so the person is never told their own question was answered by someone else. `take`
 * says whether a reply to `question` may go, and notes it; `release` forgets it when the reply did not go for another reason (not
 * connected, the session slow), so that it can be tried again. A reply naming no question always goes.
 */
export function repliedQuestions() {
  const sent: string[] = []
  return {
    take(question: string | undefined): boolean {
      if (!question) return true
      if (sent.includes(question)) return false
      sent.push(question)
      if (sent.length > REMEMBERED) sent.shift()
      return true
    },
    release(question: string | undefined) {
      const i = question ? sent.indexOf(question) : -1
      if (i >= 0) sent.splice(i, 1)
    },
  }
}

/**
 * The page's refusal of a reply that names no question to an owner that takes answers (a hosted session's question the server
 * listed before its host said its id, or a server older than its host): nothing is sent, for keys that name nothing could land
 * on another question.
 */
export const NO_QUESTION = 'no_question'

/** A reply that did not go: the owner's refusal (its error code), or the page's own (`not_connected`, `no_word`, `no_question`). */
export class ReplyError extends Error {
  readonly code: string
  constructor(code: string, message: string) {
    super(message)
    this.name = 'ReplyError'
    this.code = code
  }
}

/** The words a page shows for a reply that did not go (a toast's): `name` the session it was for, where the page lists several. */
export function replyFailure(e: unknown, name?: string): { title: string; description?: string; color: 'warning' | 'error' } {
  const code = (e as { code?: unknown } | null)?.code
  if (code === QUESTION_ANSWERED) {
    return {
      title: 'Already answered',
      description: `${name ? `${name}'s` : 'This'} question was answered before your reply arrived. Nothing was sent. If the agent still asks, answer it in its terminal.`,
      color: 'warning',
    }
  }
  if (code === NO_QUESTION) {
    return { title: 'Not sent', description: 'This page could not tell the session which question your reply was for, so nothing was sent. Try again in a moment, or answer in the terminal.', color: 'warning' }
  }
  if (code === 'not_connected') return { title: 'Not connected', description: 'Reconnect the terminal and try again.', color: 'warning' }
  return { title: name ? `Reply to ${name} failed` : 'Reply failed', description: (e as Error | null)?.message, color: 'error' }
}
