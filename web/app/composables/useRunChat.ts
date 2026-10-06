import type { Ref } from 'vue'
import type { JoinRunMember, RunMember } from './useSessions'
import type { ChatMessage, ChatPost, ChatRoster, ChatSend, ViewerInfo } from '~/utils/protocol'
import { FOLLOW_SIZE } from '~/utils/protocol'
import type { TerminalTransport } from '~/utils/transport/types'

/** A member as the run page or a run link knows it: enough to pick one to carry the chat. */
export type RunChatMember = Pick<RunMember, 'name' | 'sessionId' | 'status'> & Pick<JoinRunMember, 'kind'>

/** A send the owner refused (`not_sent`): the message it named and why, in the server's word. */
export interface RunChatRefusal {
  requestId: string
  reason: string
}

/**
 * A run's chat on a page that shows tiles and no terminal of its own (design
 * 2e, 2f): one quiet connection (`hello.chatOnly`) to a live member carries the
 * run's thread, posts and sends with scope `run`, and moves to another member
 * when that one ends; with none live the thread is what the run's record
 * kept, read-only. It works wherever the members do: a guest on a run link
 * and a run shared through a switchyard have it over their own connection.
 */
export function useRunChat(runId: Ref<string>, members: Ref<readonly RunChatMember[]>, opts: { token: Ref<string>; server?: Ref<string>; kept?: Ref<ChatMessage[] | undefined> }) {
  const { create } = useTerminalTransport()
  const unread = useChatUnread()
  const key = computed(() => `run:${runId.value}`)
  const chat = useChat(key)
  const roster = ref<ChatRoster | null>(null)
  const state = ref<'idle' | 'connecting' | 'open' | 'closed'>('idle')
  const offline = computed(() => state.value !== 'open')
  const refused = ref<RunChatRefusal | null>(null)
  let transport: TerminalTransport | null = null
  let timer: number | undefined
  let gone = false

  /** The member whose session carries the chat: the first with a live session. */
  const carrier = computed(() => members.value.find((m) => m.sessionId && (m.status === 'running' || m.status === 'starting')))
  /** Who is on the run's chat, as the roster lists them, in the shape the avatars take. */
  const people = computed<ViewerInfo[]>(() => (roster.value?.list ?? []).map((p) => ({ id: p.id, name: p.name, role: p.role, since: '' })))

  const via = (post: ChatPost) => {
    if (!transport || transport.state.value !== 'open') return false
    transport.chat(post)
    return true
  }
  const viaSend = (send: ChatSend) => {
    if (!transport || transport.state.value !== 'open') return false
    transport.chatSend(send)
    return true
  }

  function close() {
    const t = transport
    transport = null
    t?.close()
  }

  async function open(m: RunChatMember) {
    close()
    const t = create({ sessionId: m.sessionId!, token: opts.token.value, kind: m.kind ?? 'server', forceRelay: true, server: opts.server?.value, chatOnly: true })
    transport = t
    state.value = 'connecting'
    t.onControl((msg) => {
      if (t !== transport) return
      switch (msg.t) {
        case 'welcome':
          unread.registerSelf(msg.subscriberId ?? msg.viewerId ?? '')
          chat.welcome(!!msg.runChat, via)
          state.value = 'open'
          break
        case 'chat':
          if (msg.scope === 'run') chat.accept(msg, { live: true })
          break
        case 'chat_history':
          if (msg.scope === 'run') chat.history(msg)
          break
        case 'chat_roster':
          roster.value = msg
          break
        case 'error':
          // A send the owner refused names the message; a post of ours that failed names its nonce.
          if (msg.requestId && !chat.fail(msg.requestId, msg.message)) refused.value = { requestId: msg.requestId, reason: msg.message }
          break
      }
    })
    t.onClose(() => {
      if (t !== transport) return
      transport = null
      state.value = 'closed'
      chat.offline()
      later()
    })
    try {
      await t.connect(FOLLOW_SIZE)
    } catch {
      /* onClose takes it from here */
    }
  }

  /** Another go in a moment: the carrier may have ended, another member may be live. */
  function later() {
    if (gone) return
    window.clearTimeout(timer)
    timer = window.setTimeout(() => {
      if (!transport && carrier.value) open(carrier.value)
    }, 2000)
  }

  watch(
    carrier,
    (m) => {
      if (m) {
        if (!transport) open(m)
      } else {
        close()
        state.value = 'closed'
      }
    },
    { immediate: true },
  )
  // With no member live, the record's chat is the thread.
  watch(
    () => opts.kept?.value,
    (kept) => {
      if (kept?.length && !carrier.value) chat.history({ t: 'chat_history', scope: 'run', messages: kept })
    },
    { immediate: true },
  )
  onBeforeUnmount(() => {
    gone = true
    window.clearTimeout(timer)
    close()
  })

  function send(text: string, to = '', on = '') {
    chat.send(text, { scope: 'run', ...(to ? { to } : {}), ...(on ? { on } : {}) }, via)
  }
  function sendTo(ref: string, to: string): boolean {
    return chat.sendToAgent(ref, viaSend, { scope: 'run', to })
  }
  function retry(nonce: string) {
    chat.retry(nonce, via)
  }

  return { key, thread: chat.thread, roster, people, offline, refused, send, sendTo, retry }
}
