import type { Ref } from 'vue'
import type { ChatHistory, ChatMessage, ChatPost, ChatScope, ChatSend, ChatQuote } from '~/utils/protocol'
import { CHAT_TO_AGENT } from '~/utils/protocol'
import { chatNonce, cleanChatText, mergeMessage } from '~/utils/chat'

/** A message of this browser's own on its way: shown at once, settled when the owner echoes its nonce. */
export interface PendingChat {
  nonce: string
  text: string
  to?: string
  /** A run's chat: the scope of the post, and the member the sender looked at. */
  scope?: ChatScope
  on?: string
  /** Lines of a file the message is about (F7). */
  quote?: ChatQuote
  at: string
  state: 'queued' | 'sending' | 'failed'
  error?: string
}

export interface ChatThread {
  messages: ChatMessage[]
  pending: PendingChat[]
  /** The owner's welcome said chat: the panel shows; false until a welcome, or on an older owner. */
  capable: boolean
}

const empty = (): ChatThread => ({ messages: [], pending: [], capable: false })

/**
 * A chat thread by key (`session:<id>`, `run:<id>`), fed by the terminal's
 * connection: the history on each welcome, then live messages. A send is
 * optimistic: its row shows at once, settles when the owner echoes the nonce,
 * waits while the connection is down (and goes on the next welcome), and
 * fails with Retry on an error that names it.
 */
export function useChat(key: Ref<string>) {
  const threads = useState<Record<string, ChatThread>>('chatThreads', () => ({}))
  const unread = useChatUnread()
  let counter = 0

  const thread = computed<ChatThread>(() => threads.value[key.value] ?? empty())
  function put(next: ChatThread) {
    threads.value = { ...threads.value, [key.value]: next }
  }

  /** A live message: kept once (the history and the live frame can both carry it), the pending row it echoes settled, unread counted. */
  function accept(m: ChatMessage, opts: { live?: boolean } = {}) {
    const t = thread.value
    put({ ...t, messages: mergeMessage(t.messages, m), pending: m.nonce ? t.pending.filter((p) => p.nonce !== m.nonce) : t.pending })
    if (opts.live !== false) unread.accept(key.value, m)
  }

  function history(h: ChatHistory) {
    const t = thread.value
    let messages = t.messages
    for (const m of h.messages) messages = mergeMessage(messages, m)
    put({ ...t, messages })
  }

  /** A new welcome: the owner replays what it keeps; what was queued goes now. */
  function welcome(capable: boolean, via: (post: ChatPost) => boolean) {
    const t = thread.value
    put({ ...t, messages: [], capable })
    if (capable) flush(via)
  }

  /** The post a pending row goes out as: its scope, member and `on` travel with it. */
  function postOf(p: Pick<PendingChat, 'nonce' | 'text' | 'to' | 'scope' | 'on' | 'quote'>): ChatPost {
    return { t: 'chat', nonce: p.nonce, text: p.text, ...(p.scope ? { scope: p.scope } : {}), ...(p.to ? { to: p.to } : {}), ...(p.on ? { on: p.on } : {}), ...(p.quote ? { quote: p.quote } : {}) }
  }

  function send(text: string, opts: { to?: string; on?: string; scope?: ChatScope; quote?: ChatQuote } = {}, via: (post: ChatPost) => boolean): boolean {
    const clean = cleanChatText(text)
    if (!clean && !opts.quote) return false
    const nonce = chatNonce(++counter)
    const row: PendingChat = { nonce, text: clean, to: opts.to, scope: opts.scope, on: opts.on, quote: opts.quote, at: new Date().toISOString(), state: 'sending' }
    const sent = via(postOf(row))
    put({ ...thread.value, pending: [...thread.value.pending, sent ? row : { ...row, state: 'queued' }] })
    return true
  }

  /** Sends what waited for a connection. */
  function flush(via: (post: ChatPost) => boolean) {
    const t = thread.value
    if (!t.pending.some((p) => p.state === 'queued')) return
    put({ ...t, pending: t.pending.map((p) => (p.state === 'queued' && via(postOf(p)) ? { ...p, state: 'sending' } : p)) })
  }

  function retry(nonce: string, via: (post: ChatPost) => boolean) {
    const t = thread.value
    put({ ...t, pending: t.pending.map((p) => (p.nonce === nonce ? { ...p, state: via(postOf(p)) ? 'sending' : 'queued', error: undefined } : p)) })
  }

  /** An error the owner sent naming a post of ours: the row fails, with the owner's words. */
  function fail(requestId: string, message: string): boolean {
    const t = thread.value
    if (!t.pending.some((p) => p.nonce === requestId)) return false
    put({ ...t, pending: t.pending.map((p) => (p.nonce === requestId ? { ...p, state: 'failed', error: message } : p)) })
    return true
  }

  /** The connection closed: what was in flight waits for the next welcome. */
  function offline() {
    const t = thread.value
    if (!t.pending.some((p) => p.state === 'sending')) return
    put({ ...t, pending: t.pending.map((p) => (p.state === 'sending' ? { ...p, state: 'queued' } : p)) })
  }

  /** Types a kept message into the agent, or into the member `to` of a run (scope `run`). */
  function sendToAgent(ref: string, via: (send: ChatSend) => boolean, opts: { scope?: ChatScope; to?: string } = {}): boolean {
    return via({ t: 'chat_send', ref, ...(opts.scope ? { scope: opts.scope } : {}), ...(opts.to ? { to: opts.to } : {}) })
  }

  function forget(nonce: string) {
    put({ ...thread.value, pending: thread.value.pending.filter((p) => p.nonce !== nonce) })
  }

  return { thread, accept, history, welcome, send, flush, retry, fail, offline, sendToAgent, forget, toAgent: CHAT_TO_AGENT }
}
