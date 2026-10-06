<script setup lang="ts">
import { SPONSOR_NAME, SPONSOR_URL } from '~/utils/about'
import type { JoinInfo, JoinRunMember, SessionKind } from '~/composables/useSessions'
import type { Attention, ChatHistory, ChatMessage, ChatPost, ChatSend, TransportKind, ViewerInfo, Welcome } from '~/utils/protocol'
import { CloseCode } from '~/utils/protocol'
import type { CloseInfo, TransportState } from '~/utils/transport/types'
import type { FileTarget } from '~/components/FileViewer.vue'
import { parseLocation } from '~/utils/links'
import { joinServer } from '~/utils/invite'
import { joinedFromInfo } from '~/utils/joined'
import { scopeItems } from '~/utils/chat'
import { skipWords, type MemberStatus } from '~/utils/crews'

// The workbench (the desktop app, or a browser holding the workbench token) shows the page beside its sidebar, so a shared session
// never takes the window over and its own sessions stay one click away; a guest gets the bare page. Decided once: a layout that
// changed under a live terminal would remount the page and drop it.
definePageMeta({ layout: false })
const admin = useWorkbenchToken()
const desktop = useDesktop()
const inWorkbench = admin.hasToken.value || desktop.isDesktop.value
const kept = useJoined()

const route = useRoute()
const api = useSessions()
const toast = useToast()
const identity = useIdentity()
const { create } = useTerminalTransport()

const token = computed(() => String(route.params.token))
/** The server this page talks to for the link: another one (an invite's switchyard, `?server=`) or its own (''). */
const server = computed(() => joinServer(String(route.query.server ?? '')))
const serverHost = computed(() => {
  try {
    return server.value ? new URL(server.value).host : ''
  } catch {
    return ''
  }
})
const info = ref<JoinInfo | null>(null)
const error = ref('')
const joined = ref(false)
const nameDraft = ref('')
const viewers = ref(0)
const viewerList = ref<ViewerInfo[]>([])
function onViewers(info: { count: number; list?: ViewerInfo[] }) {
  viewers.value = info.count
  viewerList.value = info.list ?? []
}
const status = ref('')
const transport = ref<{ kind: TransportKind; state: TransportState; rtt: number | null }>({ kind: 'ws', state: 'idle', rtt: null })
const fileOpen = ref(false)
const fileTarget = ref<FileTarget | null>(null)
const previewUrl = ref<string | null>(null)
const pathInput = ref('')
const terminal = ref<{ requestFile: (p: string, s?: boolean) => Promise<any>; sendInput: (t: string) => boolean; submit: (t: string) => boolean; chat: (p: ChatPost) => boolean; chatSend: (s: ChatSend) => boolean } | null>(null)
const attention = ref<Attention>({ state: '' })
function onAttention(msg: { state: string; message?: string; source?: string }) {
  attention.value = { ...(msg as Attention), since: new Date().toISOString() }
  hearFocused({ attention: msg.state })
}
function onStatus(s: string) {
  status.value = s
  hearFocused({ status: s })
}
/** The connection closed: what was being said waits; a close for the session's end says so, should the status frame not have been read first. */
function onClosed(info: CloseInfo) {
  chat.offline()
  if (info.code === CloseCode.SessionEnded && info.reason === 'session ended' && !ended.value) onStatus('exited')
}

// A run link opens every member of a crew run: a tile each, and one member
// at a time in full with the link's role (`focus`). A session link opens its
// session in full.
const run = computed(() => info.value?.run)
const focus = ref<JoinRunMember | null>(null)
/** The session shown in full: a session link's, or the member opened from a run link's tiles. */
const current = computed<{ id: string; name: string; agentId: string; kind: SessionKind; hostName?: string; hostUser?: string } | null>(() => {
  const s = info.value?.session
  if (s) return s
  const m = focus.value
  // Crew members run on the server.
  return m?.sessionId ? { id: m.sessionId, name: m.name, agentId: m.agentId, kind: m.kind ?? 'server' } : null
})

/** What each member's tile last heard from its session, by member name: kept while a member is open in full, for the tiles on the way back (backToCrew). */
const tileState = ref<Record<string, { status?: string; attention?: string }>>({})
/**
 * What the full view hears of the member open in it goes to that member's tile too, so Back shows the truth: a tile that joins again is
 * told of a prompt only while one is live, never that one was cleared.
 */
function hearFocused(patch: { status?: string; attention?: string }) {
  const name = focus.value?.name
  if (!name) return
  tileState.value = { ...tileState.value, [name]: { ...tileState.value[name], ...patch } }
}

// The chat for whoever holds the link (design 2d): the session's own thread, over this page's connection, so it works through a
// switchyard too. A panel beside the terminal where there is room, a sheet over it on a phone (2c). A view-only guest can talk
// and never reaches the agent; the owner's welcome says whether chat exists at all.
const chatKey = computed(() => `session:${current.value?.id ?? ''}`)
const chat = useChat(chatKey)
const unread = useChatUnread()
const chatUnread = computed(() => unread.count(chatKey.value))
const chatOffline = computed(() => transport.value.state !== 'open')
const ended = computed(() => status.value === 'exited' || status.value === 'stopped')
const chatNote = computed(() => (info.value?.role === 'control' ? '' : 'You are view only: what you write reaches the people here, not the agent.'))
const wide = useMedia('(min-width: 48rem)')
const chatPanel = ref(true)
const chatSheet = ref(false)
/** The thread is in front of the person: the panel beside the terminal where it shows, or the sheet. */
const chatShown = computed(() => joined.value && !!current.value && ((wide.value && chatPanel.value) || chatSheet.value))
function showChat() {
  if (wide.value) chatPanel.value = !chatPanel.value
  else chatSheet.value = true
}
watch(
  [chatKey, chatShown],
  ([key, shown]) => {
    if (shown) unread.openThread(key)
    else unread.closeThread(key)
  },
  { immediate: true },
)
watch(chatKey, (_, old) => old && unread.closeThread(old))
onBeforeUnmount(() => unread.closeThread(chatKey.value))
const viaChat = (post: ChatPost) => terminal.value?.chat(post) ?? false
const viaChatSend = (send: ChatSend) => terminal.value?.chatSend(send) ?? false
function onWelcome(w: Welcome) {
  unread.registerSelf(w.subscriberId ?? w.viewerId ?? '')
  chat.welcome(!!w.chat, viaChat)
}
function onChat(m: ChatMessage) {
  // The run's chat comes over its own connection (useRunChat); a member's frames of it are dropped here.
  if (m.scope !== 'run') chat.accept(m, { live: true })
}
function onChatHistory(h: ChatHistory) {
  if (h.scope !== 'run') chat.history(h)
}
function chatSend(text: string, to: string) {
  chat.send(text, to ? { to } : {}, viaChat)
}

// The run's chat for a run link (design 2f): one thread for everyone on the link, over a quiet connection of its own to a live
// member, read and posted beside the tiles and while a member is open in full (`on` names that member). A view-only guest talks
// here and reaches no member; a guest with control may also have a message typed into a running member.
const runMembers = computed(() => info.value?.run?.members ?? [])
const runChat = useRunChat(
  computed(() => info.value?.run?.id ?? ''),
  runMembers,
  { token, server },
)
const runChatOpen = ref(false)
const runChatUnread = computed(() => unread.count(runChat.key.value))
const runChatShown = computed(() => joined.value && !!run.value && runChatOpen.value)
watch(
  [() => runChat.key.value, runChatShown],
  ([key, shown]) => {
    if (!key.endsWith(':')) {
      if (shown) unread.openThread(key)
      else unread.closeThread(key)
    }
  },
  { immediate: true },
)
const runChatNote = computed(() => (info.value?.role === 'control' ? '' : 'You are view only: you can talk here; nothing you write reaches a member.'))
/** The members' states for the composer's menu: the tiles' word on a prompt, else the run's status. */
const runStates = computed(() => new Map(runMembers.value.map((m) => [m.name, (tileState.value[m.name]?.attention === 'needs_input' ? 'needs_input' : m.status) as MemberStatus])))
const runScope = computed(() => scopeItems(runMembers.value, runStates.value))
const runTargets = computed(() => runScope.value.slice(1))
const runChatDescription = computed(() => (run.value ? `${run.value.name} · kept with the run` : ''))
function runChatSend(text: string, to: string) {
  runChat.send(text, to, focus.value?.name ?? '')
}
function runChatSendTo(ref: string, to: string) {
  if (!runChat.sendTo(ref, to)) toast.add({ title: 'Not connected', color: 'warning' })
}
watch(runChat.refused, (r) => {
  if (r) toast.add({ title: 'Not sent to the member', description: skipWords(r.reason), color: 'warning' })
})
/** A member's question answered from the run's chat: the choice's keys into that member, over the link. */
const quick = useQuickReply()
function runChatAnswer(m: ChatMessage, index: number) {
  const o = m.options?.[index]
  const member = runMembers.value.find((x) => x.name === m.on)
  if (!o || !member?.sessionId) return
  quick.send({ id: member.sessionId, kind: member.kind ?? 'server' }, o.input, { token: token.value, server: server.value }).catch((e: unknown) => toast.add({ title: `Reply to ${member.name} failed`, description: (e as Error).message, color: 'error' }))
}
function chatSendToAgent(ref: string) {
  if (!chat.sendToAgent(ref, viaChatSend)) toast.add({ title: 'Not connected', color: 'warning' })
}
function chatRetry(nonce: string) {
  chat.retry(nonce, viaChat)
}
/** A question's choice from the session's chat: its keys into the session. */
function chatAnswer(m: ChatMessage, index: number) {
  const o = m.options?.[index]
  if (o && !terminal.value?.sendInput(o.input)) toast.add({ title: 'Not connected', color: 'warning' })
}
function onRequestError(err: { code: string; message: string; requestId: string }) {
  if (chat.fail(err.requestId, err.message)) return
  toast.add({ title: 'Not sent to the agent', description: err.message, color: 'warning' })
}

useHead({ title: computed(() => (run.value ? `${run.value.name} (shared)` : info.value?.session ? `${info.value.session.name} (shared)` : 'Join session')) })

function agentName(agentId: string): string {
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', agy: 'Antigravity' }
  return names[agentId] ?? agentId
}
const agentLabel = computed(() => agentName(current.value?.agentId ?? ''))
/** The switchyard this link was shared through, named on the page: the invite's server, else this page's own host. */
const viaHost = computed(() => serverHost.value || (import.meta.client ? location.host : ''))
const hostedBy = computed(() => {
  const s = current.value
  if (!s) return ''
  return s.kind === 'hosted' ? `hosted by ${s.hostUser || s.hostName || 'a developer'}` : 'running on the server'
})

// The join API returns only metadata; nothing connects until the guest
// presses Join, so a fetched link never exposes terminal content.
async function fetchInfo() {
  try {
    info.value = await api.join(token.value, server.value)
    if (info.value.session) status.value = info.value.session.status
    error.value = ''
    kept.noteInfo(server.value, token.value, info.value)
  } catch (e) {
    error.value = (e as Error).message
  }
}

onMounted(async () => {
  nameDraft.value = identity.name.value
  await fetchInfo()
  // A link kept under Shared with you joins at once: the person joined it before, under this name.
  if (inWorkbench && info.value && !error.value && identity.name.value && kept.find(server.value, token.value)) join()
})

function join() {
  identity.set(nameDraft.value)
  left.value = false
  joined.value = true
  // Kept in the workbench's sidebar; a guest's browser keeps nothing.
  if (inWorkbench && info.value) kept.add({ token: token.value, server: server.value, host: viaHost.value, ...joinedFromInfo(info.value) })
}

/** What the person left, said on the card they come back to; the link still works, so Join again is one click. */
const left = ref(false)

/** Leaves the share: the terminals go (their connections close with them) and the card comes back, the name kept. */
function leave() {
  joined.value = false
  focus.value = null
  tileState.value = {}
  attention.value = { state: '' }
  fileTarget.value = null
  previewUrl.value = null
  chatSheet.value = false
  runChatOpen.value = false
  left.value = true
}

function createTransport() {
  const s = current.value!
  return create({ sessionId: s.id, token: token.value, kind: s.kind, name: identity.name.value, server: server.value })
}

function tileTransport(m: JoinRunMember) {
  return () => create({ sessionId: m.sessionId!, token: token.value, kind: m.kind ?? 'server', name: identity.name.value, server: server.value })
}

function openMember(m: JoinRunMember, heard?: { status?: string; attention?: string }) {
  if (!m.sessionId) return
  status.value = heard?.status ?? m.status
  attention.value = { state: '' }
  viewers.value = 0
  transport.value = { kind: 'ws', state: 'idle', rtt: null }
  fileTarget.value = null
  previewUrl.value = null
  focus.value = m
}

function backToCrew() {
  // The tiles keep their status. Of their attention only the open member's
  // stays, which the full view kept true: another member's prompt may have
  // been answered meanwhile, and a tile that joins again is told of a prompt
  // only while one is live, so a live one comes back as its tile reattaches.
  const open = focus.value?.name
  const next: Record<string, { status?: string; attention?: string }> = {}
  for (const [name, heard] of Object.entries(tileState.value)) next[name] = name === open ? { ...heard } : { status: heard.status }
  tileState.value = next
  focus.value = null
  // Members may have started or ended meanwhile.
  fetchInfo()
}

function reply(text: string) {
  if (!terminal.value?.submit(text)) toast.add({ title: 'Not connected', color: 'warning' })
}

function option(index: number) {
  const o = attention.value.options?.[index]
  if (o && !terminal.value?.sendInput(o.input)) toast.add({ title: 'Not connected', color: 'warning' })
}

function openFile(loc: { path: string; line?: number }) {
  previewUrl.value = null
  fileTarget.value = { ...loc }
}

function openUrl(url: string) {
  toast.add({
    title: url,
    icon: 'i-lucide-link',
    color: 'neutral',
    actions: [
      { label: 'Open in new tab', icon: 'i-lucide-external-link', onClick: () => window.open(url, '_blank', 'noopener,noreferrer') },
      { label: 'Preview in pane', icon: 'i-lucide-panel-right', onClick: () => (previewUrl.value = url) },
    ],
  })
}

function openPath() {
  const loc = parseLocation(pathInput.value)
  if (loc.path) openFile(loc)
  pathInput.value = ''
}

function requestFile(path: string, stat?: boolean) {
  if (!terminal.value) return Promise.reject(new Error('terminal not ready'))
  return terminal.value.requestFile(path, stat)
}
</script>

<template>
  <NuxtLayout :name="inWorkbench ? 'default' : 'bare'">
    <JoinFrame :workbench="inWorkbench">
  <template v-if="!joined">
    <main class="flex flex-1 flex-col items-center justify-center gap-4 p-6">
      <div class="flex w-full max-w-sm flex-col gap-4 rounded-lg border border-default bg-default p-6 shadow-sm">
        <img src="/brand/conductor-mark.svg" alt="" class="size-8 dark:hidden" />
        <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-8 hidden dark:block" />
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-link-2-off" title="This link cannot be used" :description="error" />
        <UAlert v-else-if="left" color="neutral" variant="subtle" icon="i-lucide-log-out" title="You left" description="Nothing more reaches this page. The link still works: join again below, or close the tab." data-join-left />
        <template v-if="info && !error">
          <div v-if="run" class="flex flex-col gap-1">
            <h1 class="text-lg font-semibold tracking-tight">Join {{ run.name }}</h1>
            <p class="text-sm leading-relaxed text-muted">
              A crew of {{ run.members.length }} {{ run.members.length === 1 ? 'agent' : 'agents' }} running on the server.
              <template v-if="info.role === 'control'">You'll have <b class="text-default">control</b>: your keystrokes reach the agent you open.</template>
              <template v-else>You'll be <b class="text-default">view only</b>: you can watch every agent and open files.</template>
            </p>
          </div>
          <div v-else-if="current" class="flex flex-col gap-1">
            <h1 class="text-lg font-semibold tracking-tight">Join {{ current.name }}</h1>
            <p class="text-sm leading-relaxed text-muted">
              A live {{ agentLabel }} session {{ hostedBy }}.
              <template v-if="info.role === 'control'">You'll have <b class="text-default">control</b>: your keystrokes reach the agent.</template>
              <template v-else>You'll be <b class="text-default">view only</b>: you can watch and open files.</template>
            </p>
          </div>
          <form class="flex flex-col gap-3" @submit.prevent="join">
            <UFormField label="Your name" name="name" hint="shown to others">
              <UInput v-model="nameDraft" class="w-full" autofocus maxlength="40" />
            </UFormField>
            <UButton type="submit" :label="run ? 'Join crew' : 'Join session'" block :disabled="!nameDraft.trim()" />
          </form>
          <p v-if="info.switchyard && viaHost" class="text-xs text-muted" data-join-switchyard>Shared through <span class="font-mono">{{ viaHost }}</span></p>
        </template>
        <div v-else class="text-sm text-muted flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Checking link…</div>
      </div>
      <!-- The Sponsor Kit's line under the card, on a switchyard's own join page (a guest's), never inside the workbench. -->
      <p v-if="!inWorkbench && info?.switchyard" class="text-[13px] text-muted" data-join-credit>
        Sponsored and maintained by <a :href="SPONSOR_URL" target="_blank" rel="noopener" class="font-semibold text-[#A44727] no-underline hover:text-default dark:text-[#F3AC89]">{{ SPONSOR_NAME }}</a>
      </p>
    </main>
  </template>

  <template v-else-if="run && !current">
    <header class="flex items-center gap-3 border-b border-default px-4 py-2">
      <UButton v-if="inWorkbench" icon="i-lucide-arrow-left" color="neutral" variant="ghost" size="sm" aria-label="Back to the list" class="lg:hidden" to="/sessions" data-back-to-list />
      <template v-if="!inWorkbench">
        <img src="/brand/conductor-mark.svg" alt="" class="size-6 dark:hidden" />
        <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-6 hidden dark:block" />
        <span class="font-semibold">Conductor</span>
        <USeparator orientation="vertical" class="h-5" />
      </template>
      <span class="truncate">{{ run.name }}</span>
      <UBadge :label="info!.role === 'control' ? 'control' : 'view only'" :icon="info!.role === 'control' ? 'i-lucide-keyboard' : 'i-lucide-eye'" :color="info!.role === 'control' ? 'primary' : 'neutral'" variant="subtle" size="sm" />
      <UBadge :label="`${run.members.length} ${run.members.length === 1 ? 'agent' : 'agents'}`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
      <div class="flex-1" />
      <span v-if="!inWorkbench" class="text-xs text-muted hidden md:inline">you are <b class="text-default">{{ identity.name.value }}</b></span>
      <UButton icon="i-lucide-refresh-cw" color="neutral" variant="ghost" size="sm" aria-label="Refresh the members" @click="fetchInfo" />
      <UButton icon="i-lucide-message-circle" color="neutral" variant="outline" size="sm" aria-label="Run chat" :class="runChatOpen && 'ring-2 ring-primary/40'" data-run-chat-button :data-run-chat-offline="runChat.offline.value ? '' : undefined" @click="runChatOpen = true">
        <span class="hidden sm:inline">Chat</span>
        <ChatUnreadPill :count="runChatUnread" />
      </UButton>
      <UButton icon="i-lucide-log-out" color="neutral" variant="outline" size="sm" aria-label="Leave" data-join-leave @click="leave"><span class="hidden sm:inline">Leave</span></UButton>
      <FullscreenButton size="sm" />
    </header>

    <main class="flex-1 min-h-0 overflow-y-auto p-2 sm:p-3">
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="mb-3" />
      <JoinCrewGrid v-model:heard="tileState" :members="run.members" :role="info!.role" :transport-for="tileTransport" :agent-name="agentName" @open="openMember" />
    </main>
    <ChatSheet
      v-model:open="runChatOpen"
      scope="run"
      title="Run chat"
      :description="runChatDescription"
      :thread="runChat.thread.value"
      :role="info!.role"
      :offline="runChat.offline.value"
      :viewers="runChat.people.value"
      :note="runChatNote"
      :scope-items="runScope"
      :send-targets="runTargets"
      @send="runChatSend"
      @send-to="runChatSendTo"
      @retry="runChat.retry"
      @answer="runChatAnswer"
    />
  </template>

  <template v-else-if="info && current">
    <header class="flex items-center gap-3 border-b border-default px-4 py-2">
      <UButton v-if="inWorkbench && !run" icon="i-lucide-arrow-left" color="neutral" variant="ghost" size="sm" aria-label="Back to the list" class="lg:hidden" to="/sessions" data-back-to-list />
      <UTooltip v-if="run" text="Back to the crew">
        <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" size="sm" aria-label="Back to the crew" @click="backToCrew" />
      </UTooltip>
      <template v-if="!inWorkbench">
        <img src="/brand/conductor-mark.svg" alt="" class="size-6 dark:hidden" />
        <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-6 hidden dark:block" />
        <span class="font-semibold">Conductor</span>
        <USeparator orientation="vertical" class="h-5" />
      </template>
      <span class="truncate"><template v-if="run">{{ run.name }} · </template>{{ current.name }}</span>
      <UBadge :label="info.role === 'control' ? 'control' : 'view only'" :icon="info.role === 'control' ? 'i-lucide-keyboard' : 'i-lucide-eye'" :color="info.role === 'control' ? 'primary' : 'neutral'" variant="subtle" size="sm" />
      <SessionStatusBadge v-if="status" :status="status as any" />
      <AttentionBadge :attention="attention" />
      <TransportBadge :kind="transport.kind" :state="transport.state" :rtt="transport.rtt" />
      <UBadge :label="`${viewers} here`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
      <UBadge v-if="current.kind === 'hosted'" :label="`hosted on ${current.hostName || 'dev machine'}`" icon="i-lucide-laptop" color="neutral" variant="subtle" size="sm" />
      <div class="flex-1" />
      <span v-if="!inWorkbench" class="text-xs text-muted hidden md:inline">you are <b class="text-default">{{ identity.name.value }}</b></span>
      <ViewerAvatars :viewers="viewerList" class="hidden lg:flex" />
      <form class="hidden md:flex items-center gap-1" @submit.prevent="openPath">
        <UInput v-model="pathInput" placeholder="open path[:line]" size="sm" class="w-56 font-mono" icon="i-lucide-file-search" />
      </form>
      <!-- The chat (design 2c, 2d): the count while the thread is closed; the panel beside the terminal where there is room, a sheet below. -->
      <UButton v-if="chat.thread.value.capable" icon="i-lucide-message-circle" color="neutral" variant="outline" size="sm" aria-label="Chat" :class="chatShown && 'ring-2 ring-primary/40'" data-chat-button @click="showChat">
        <span class="hidden sm:inline">Chat</span>
        <ChatUnreadPill :count="chatUnread" />
      </UButton>
      <UButton v-if="run" icon="i-lucide-messages-square" color="neutral" variant="outline" size="sm" aria-label="Run chat" :class="runChatOpen && 'ring-2 ring-primary/40'" data-run-chat-button :data-run-chat-offline="runChat.offline.value ? '' : undefined" @click="runChatOpen = true">
        <span class="hidden sm:inline">Run chat</span>
        <ChatUnreadPill :count="runChatUnread" />
      </UButton>
      <UButton icon="i-lucide-log-out" color="neutral" variant="outline" size="sm" aria-label="Leave" data-join-leave @click="leave"><span class="hidden sm:inline">Leave</span></UButton>
      <FullscreenButton size="sm" />
    </header>

    <main class="flex-1 min-h-0 p-2 sm:p-3 flex gap-3">
      <div class="flex flex-1 min-w-0 flex-col gap-2">
        <div class="flex-1 min-h-0">
          <TerminalView
            :key="current.id"
            ref="terminal"
            :create-transport="createTransport"
            :read-only="info.role !== 'control'"
            @welcome="onWelcome"
            @status="onStatus"
            @attention="onAttention"
            @viewers="onViewers"
            @transport="transport = $event"
            @closed="onClosed"
            @chat="onChat"
            @chat-history="onChatHistory"
            @request-error="onRequestError"
            @open-file="openFile"
            @open-url="openUrl"
          />
        </div>
        <QuickReplyBar :attention="attention" :agent-name="agentLabel" :role="info.role" @reply="reply" @option="option" />
      </div>
      <aside v-if="chatPanel && chat.thread.value.capable" class="hidden md:flex w-[332px] flex-none" data-chat-aside>
        <ChatPanel class="w-full overflow-hidden rounded-lg border border-default" :thread="chat.thread.value" :role="info.role" :ended="ended" :offline="chatOffline" :viewers="viewerList" :note="chatNote" @send="chatSend" @send-to-agent="chatSendToAgent" @retry="chatRetry" @answer="chatAnswer" />
      </aside>
    </main>

    <FileViewer v-model:open="fileOpen" v-model:target="fileTarget" v-model:url="previewUrl" :request="requestFile" />
    <ChatSheet v-model:open="chatSheet" :thread="chat.thread.value" :role="info.role" :ended="ended" :offline="chatOffline" :viewers="viewerList" :note="chatNote" @send="chatSend" @send-to-agent="chatSendToAgent" @retry="chatRetry" @answer="chatAnswer" />
    <ChatSheet
      v-if="run"
      v-model:open="runChatOpen"
      scope="run"
      title="Run chat"
      :description="runChatDescription"
      :thread="runChat.thread.value"
      :role="info.role"
      :offline="runChat.offline.value"
      :viewers="runChat.people.value"
      :note="runChatNote"
      :scope-items="runScope"
      :send-targets="runTargets"
      @send="runChatSend"
      @send-to="runChatSendTo"
      @retry="runChat.retry"
      @answer="runChatAnswer"
    />
  </template>
    </JoinFrame>
  </NuxtLayout>
</template>
