<script setup lang="ts">
import type { SessionInfo, ShareLink } from '~/composables/useSessions'
import type { ActivityEntry, Attention, TransportKind, ViewerInfo, Welcome } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'
import type { FileTarget } from '~/components/FileBrowser.vue'
import type { InspectorTab } from '~/components/SessionInspector.vue'
import { shortCwd } from '~/utils/sessions'

const route = useRoute()
const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const live = useAttention()
const { httpBase } = useApiBase()
const { create } = useTerminalTransport()

const id = computed(() => String(route.params.id))
const session = ref<SessionInfo | null>(null)
const error = ref('')
const ready = ref(false)
const viewers = ref<ViewerInfo[]>([])
const viewerCount = ref(0)
const activity = ref<ActivityEntry[]>([])
const links = ref<ShareLink[]>([])
const transport = ref<{ kind: TransportKind; state: TransportState; rtt: number | null }>({ kind: 'ws', state: 'idle', rtt: null })
const share = ref(false)
const fileTarget = ref<FileTarget | null>(null)
const previewUrl = ref<string | null>(null)
const tab = ref<InspectorTab>('people')
const attention = ref<Attention>({ state: '' })

const INSPECTOR_KEY = 'conductor.inspector'
const inspector = ref(true)
try {
  inspector.value = localStorage.getItem(INSPECTOR_KEY) !== '0'
} catch {
  /* ignore */
}
watch(inspector, (v) => {
  try {
    localStorage.setItem(INSPECTOR_KEY, v ? '1' : '0')
  } catch {
    /* ignore */
  }
})

// Opening a session takes its event badge away, and one arriving while it is
// open never stays.
const events = useEvents()
watch(
  [id, () => events.marks.value[id.value]],
  ([sid, mark]) => {
    if (mark) events.clearMark(sid)
  },
  { immediate: true },
)

// The live store is the source of truth for attention (it is what the sidebar
// shows); the terminal's own attention message arrives a moment earlier.
const stored = computed(() => live.sessions.value.find((s) => s.id === id.value))
watch(
  () => stored.value?.attention,
  (a) => {
    if (a) attention.value = a
  },
  { deep: true },
)

const agentLabel = computed(() => {
  const a = session.value?.agentId ?? ''
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', agy: 'Antigravity' }
  return names[a] ?? a
})
const meta = computed(() => {
  const s = session.value
  if (!s) return ''
  const parts = [agentLabel.value]
  parts.push(s.kind === 'hosted' ? `hosted by ${s.hostUser || '?'} on ${s.hostName || 'dev machine'}` : 'server')
  parts.push(shortCwd(s.cwd))
  if (s.branch) parts.push(s.branch)
  return parts.join(' · ')
})

const menu = computed(() => [
  [
    { label: 'Mark as needs input', icon: 'i-lucide-hand', onSelect: () => signal('needs_input') },
    { label: 'Mark as working', icon: 'i-lucide-loader-circle', onSelect: () => signal('working') },
    { label: 'Clear signal', icon: 'i-lucide-x', onSelect: () => signal('clear') },
  ],
  [{ label: 'Stop session', icon: 'i-lucide-square', color: 'error' as const, disabled: !(session.value && (session.value.status === 'running' || session.value.status === 'starting')), onSelect: stop }],
])

const terminal = ref<{ connect: () => void; focus: () => void; sendInput: (t: string) => boolean; requestFile: (p: string, s?: boolean) => Promise<any> } | null>(null)

// "Priya is typing…": anyone else whose last input is under four seconds old.
const now = ref(Date.now())
let tick: number | undefined
onMounted(() => (tick = window.setInterval(() => (now.value = Date.now()), 1000)))
onBeforeUnmount(() => window.clearInterval(tick))
const selfId = ref('')
const typingNames = computed(() =>
  viewers.value.filter((v) => v.id !== selfId.value && v.lastInputAt && now.value - Date.parse(v.lastInputAt) < 4000).map((v) => v.name),
)
const typingLine = computed(() => {
  const n = typingNames.value
  if (!n.length) return ''
  if (n.length === 1) return `${n[0]} is typing…`
  if (n.length === 2) return `${n[0]} and ${n[1]} are typing…`
  return `${n[0]}, ${n[1]} and ${n.length - 2} more are typing…`
})

useHead({ title: computed(() => session.value?.name || 'Session') })

async function load() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  try {
    const res = await api.get(id.value)
    session.value = res.session
    attention.value = res.session.attention ?? { state: '' }
    links.value = res.links ?? []
    error.value = ''
    ready.value = true
  } catch (e) {
    error.value = (e as Error).message
  }
}

async function refreshLinks() {
  try {
    links.value = await api.links(id.value)
  } catch {
    /* keep the last list */
  }
}

async function signal(state: 'needs_input' | 'working' | 'clear') {
  try {
    await api.setAttention(id.value, state, state === 'needs_input' ? 'flagged from the workbench' : undefined)
  } catch (e) {
    toast.add({ title: 'Signal failed', description: (e as Error).message, color: 'error' })
  }
}

function onAttention(msg: { state: string; message?: string; source?: string }) {
  attention.value = { ...(msg as Attention), since: new Date().toISOString() }
}

function onViewers(info: { count: number; list?: ViewerInfo[] }) {
  viewerCount.value = info.count
  viewers.value = info.list ?? []
}

function onActivity(e: ActivityEntry) {
  activity.value = [...activity.value.slice(-199), e]
}

// Every (re)connection replays the last 50 entries, so start the log afresh.
function onWelcome(w: Welcome) {
  selfId.value = w.subscriberId ?? w.viewerId ?? ''
  activity.value = []
}

function createTransport() {
  return create({ sessionId: id.value, token: admin.token.value, kind: session.value?.kind ?? 'server' })
}

function onStatus(status: string, exitCode?: number) {
  if (session.value) {
    session.value.status = status as SessionInfo['status']
    if (exitCode !== undefined) session.value.exitCode = exitCode
  }
}

async function stop() {
  if (!session.value) return
  try {
    await api.stop(session.value.id)
    toast.add({ title: 'Stop requested', color: 'neutral' })
    await load()
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, color: 'error' })
  }
}

function reply(text: string) {
  if (!terminal.value?.sendInput(text + '\r')) toast.add({ title: 'Not connected', description: 'Reconnect the terminal and try again.', color: 'warning' })
}

function option(index: number) {
  const o = attention.value.options?.[index]
  if (!o) return
  if (!terminal.value?.sendInput(o.input)) toast.add({ title: 'Not connected', description: 'Reconnect the terminal and try again.', color: 'warning' })
}

function openFile(loc: { path: string; line?: number }) {
  previewUrl.value = null
  fileTarget.value = { ...loc }
  tab.value = 'files'
  inspector.value = true
}

function openUrl(url: string) {
  toast.add({
    title: url,
    icon: 'i-lucide-link',
    color: 'neutral',
    actions: [
      { label: 'Open in new tab', icon: 'i-lucide-external-link', onClick: () => window.open(url, '_blank', 'noopener,noreferrer') },
      {
        label: 'Preview in pane',
        icon: 'i-lucide-panel-right',
        onClick: () => {
          previewUrl.value = url
          tab.value = 'files'
          inspector.value = true
        },
      },
    ],
  })
}

function showFiles() {
  if (inspector.value && tab.value === 'files') inspector.value = false
  else {
    tab.value = 'files'
    inspector.value = true
  }
}

function requestFile(path: string, stat?: boolean) {
  if (!terminal.value) return Promise.reject(new Error('terminal not ready'))
  return terminal.value.requestFile(path, stat)
}

function rawUrl(path: string) {
  if (session.value?.kind !== 'server') return null
  return `${httpBase.value}/api/sessions/${encodeURIComponent(id.value)}/files?path=${encodeURIComponent(path)}&raw=1&token=${encodeURIComponent(admin.token.value)}`
}

async function revoke(link: ShareLink) {
  try {
    await api.revokeLink(id.value, link.id)
    toast.add({ title: 'Link revoked', description: 'Viewers using it were disconnected.', icon: 'i-lucide-ban', color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Revoke failed', description: (e as Error).message, color: 'error' })
  }
  await refreshLinks()
}

watch(share, (open) => {
  if (!open) refreshLinks()
})

onMounted(load)
watch(() => admin.token.value, load)
watch(id, () => {
  ready.value = false
  session.value = null
  viewers.value = []
  activity.value = []
  fileTarget.value = null
  previewUrl.value = null
  load()
})
</script>

<template>
  <UDashboardPanel :id="`session-${id}`" :ui="{ body: 'p-0 sm:p-0 flex flex-col min-h-0 gap-0' }">
    <template #header>
      <UDashboardNavbar :ui="{ root: 'h-14 bg-default', title: 'min-w-0' }">
        <template #title>
          <div class="flex min-w-0 flex-col">
            <div class="flex items-center gap-2 min-w-0">
              <span class="truncate text-[15px] font-semibold">{{ session?.name || 'Session' }}</span>
              <AttentionBadge :attention="attention" />
              <SessionStatusBadge v-if="session && session.status !== 'running'" :status="session.status" :exit-code="session.exitCode" />
            </div>
            <span class="truncate font-mono text-[11.5px] text-muted">{{ meta }}</span>
          </div>
        </template>
        <template #right>
          <TransportBadge :kind="transport.kind" :state="transport.state" :rtt="transport.rtt" class="hidden md:inline-flex" />
          <ViewerAvatars :viewers="viewers" class="hidden md:flex" />
          <UButton label="Files" icon="i-lucide-folder-open" color="neutral" variant="outline" :class="inspector && tab === 'files' && 'ring-2 ring-primary/40'" @click="showFiles" />
          <UButton label="Share" icon="i-lucide-share-2" @click="share = true" />
          <UButton icon="i-lucide-panel-right" color="neutral" variant="outline" :aria-label="inspector ? 'Hide inspector' : 'Show inspector'" class="hidden xl:inline-flex" @click="inspector = !inspector" />
          <UDropdownMenu :items="menu">
            <UButton icon="i-lucide-ellipsis" color="neutral" variant="outline" aria-label="More" />
          </UDropdownMenu>
          <FullscreenButton />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="m-4" />
      <div v-else-if="ready && session" class="flex flex-1 min-h-0">
        <div class="flex flex-1 min-w-0 flex-col gap-3 p-3">
          <div class="relative flex-1 min-h-0">
            <TerminalView
              ref="terminal"
              :create-transport="createTransport"
              @welcome="onWelcome"
              @status="onStatus"
              @attention="onAttention"
              @viewers="onViewers"
              @activity="onActivity"
              @transport="transport = $event"
              @open-file="openFile"
              @open-url="openUrl"
            />
            <div v-if="typingLine" class="pointer-events-none absolute bottom-2 left-3 flex items-center gap-2 rounded bg-default/80 px-2 py-0.5 text-xs text-muted backdrop-blur-sm"><span class="inline-block h-3.5 w-1.5 bg-muted/70" />{{ typingLine }}</div>
          </div>
          <QuickReplyBar :attention="attention" :agent-name="agentLabel" role="control" @reply="reply" @option="option" />
        </div>
        <div v-if="inspector" class="hidden xl:flex w-[332px] flex-none">
          <SessionInspector
            v-model:tab="tab"
            v-model:target="fileTarget"
            v-model:url="previewUrl"
            :session="session"
            role="control"
            :viewers="viewers"
            :activity="activity"
            :links="links"
            :request="requestFile"
            :raw-url="rawUrl"
            @new-link="share = true"
            @revoke="revoke"
          />
        </div>
      </div>
      <div v-else class="p-6 text-sm text-muted flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading session…</div>
    </template>
  </UDashboardPanel>

  <ShareLinksModal v-model:open="share" :session-id="id" :session-name="session?.name" />
</template>
