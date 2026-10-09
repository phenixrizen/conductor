<script setup lang="ts">
import { emptyTabs, toggleFold } from '~/utils/editorTabs'
import type { SessionInfo } from '~/composables/useSessions'
import type { TransportKind, ActivityEntry, Welcome } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'
import { WALL_SHORTCUTS } from '~/composables/useShortcuts'
import { isActive } from '~/utils/attention'
import { bestGrid } from '~/utils/wall'

useHead({ title: 'Yard' })

const route = useRoute()
const router = useRouter()
const attention = useAttention()
const admin = useWorkbenchToken()
const api = useSessions()
const toast = useToast()
const quick = useQuickReply()
const { create } = useTerminalTransport()
const { httpBase } = useApiBase()
useShortcutsModal().registerPage(WALL_SHORTCUTS)

type Filter = 'all' | 'needs' | 'running'
const filter = ref<Filter>('all')
const active = computed(() => attention.sessions.value.filter(isActive))
const waiting = computed(() => attention.needsInput.value.filter(isActive))
const running = computed(() => active.value.filter((s) => s.attention?.state !== 'needs_input'))
const tiles = computed(() => (filter.value === 'needs' ? waiting.value : filter.value === 'running' ? running.value : active.value))
const answered = computed(() =>
  attention.sessions.value
    .filter((s) => s.lastAnswer)
    .sort((a, b) => (b.lastAnswer?.at ?? '').localeCompare(a.lastAnswer?.at ?? ''))
    .slice(0, 8),
)
const showQueue = computed(() => waiting.value.length > 0 || answered.value.length > 0)

// Queue keyboard selection (J / K, Enter to type a reply).
const queueSel = ref(0)
const queue = useTemplateRef<{ focusSelected: () => void }>('queue')
watch(waiting, (list) => {
  queueSel.value = Math.min(queueSel.value, Math.max(0, list.length - 1))
})

// Focus mode: /yard?focus=<id> expands one session in place. The grid is one
// Esc, one click or one browser Back away; the URL stays shareable.
const focusId = computed(() => (typeof route.query.focus === 'string' ? route.query.focus : ''))
const focused = computed<SessionInfo | undefined>(() => attention.sessions.value.find((s) => s.id === focusId.value))
const focusTerminal = ref<{ focus: () => void; requestFile: (p: string, s?: boolean, x?: FileGetExtra) => Promise<any> } | null>(null)
const viewers = ref(0)
const transport = ref<{ kind: TransportKind; state: TransportState; rtt: number | null }>({ kind: 'ws', state: 'idle', rtt: null })
// The editor area (design 4g): files open above the focused tile's terminal, the Files pane beside it.
const { tabs, editorOpen, openFile, openUrl, openDiff, openCommitDiff } = useEditorTabs()
watch(focusId, () => {
  viewers.value = 0
  transport.value = { kind: 'ws', state: 'idle', rtt: null }
  tabs.value = emptyTabs()
})

function focusSession(s: SessionInfo) {
  router.push({ path: '/yard', query: { focus: s.id } })
}

function backToGrid() {
  router.push({ path: '/yard' })
}

function leaveFocus() {
  // Esc first folds the editor; only a second Esc leaves focus mode.
  if (!focusId.value) return
  if (editorOpen.value) {
    tabs.value = toggleFold(tabs.value)
    return
  }
  backToGrid()
}
function queueNext() {
  if (waiting.value.length) queueSel.value = (queueSel.value + 1) % waiting.value.length
}
function queuePrev() {
  if (waiting.value.length) queueSel.value = (queueSel.value - 1 + waiting.value.length) % waiting.value.length
}
const inTerminal = { usingInput: true }
defineShortcuts({
  escape: leaveFocus,
  j: queueNext,
  k: queuePrev,
  enter: () => {
    if (!focusId.value && waiting.value.length) queue.value?.focusSelected()
  },
  alt_escape: { ...inTerminal, handler: leaveFocus },
  alt_j: { ...inTerminal, handler: queueNext },
  alt_k: { ...inTerminal, handler: queuePrev },
})

// Grid: every active session fits on screen; tiles shrink as sessions are added.
const grid = useTemplateRef<HTMLDivElement>('grid')
const box = ref({ w: 0, h: 0 })
watch(
  grid,
  (el, _prev, onCleanup) => {
    if (!el) return
    const observer = new ResizeObserver((entries) => {
      const r = entries[0]?.contentRect
      if (r) box.value = { w: r.width, h: r.height }
    })
    observer.observe(el)
    onCleanup(() => observer.disconnect())
  },
  { immediate: true },
)
const layout = computed(() => bestGrid(tiles.value.length, box.value.w, box.value.h, 12, 1.6))
const gridStyle = computed(() => ({
  gridTemplateColumns: `repeat(${layout.value.cols}, minmax(0, 1fr))`,
  gridTemplateRows: `repeat(${layout.value.rows}, minmax(0, 1fr))`,
}))

function transportFor(s: SessionInfo) {
  return () => create({ sessionId: s.id, token: admin.token.value, kind: s.kind })
}

function fail(title: string) {
  return (e: unknown) => toast.add({ title, description: (e as Error).message, color: 'error' })
}

function reply(s: SessionInfo, text: string) {
  quick.reply(s, text).catch(fail(`Reply to ${s.name} failed`))
}

function option(s: SessionInfo, index: number) {
  const o = s.attention?.options?.[index]
  if (!o) return
  quick.send(s, o.input).catch(fail(`Reply to ${s.name} failed`))
}

async function stop(s: SessionInfo) {
  try {
    await api.stop(s.id)
    toast.add({ title: 'Stop requested', description: s.name, color: 'neutral' })
  } catch (e) {
    fail('Stop failed')(e)
  }
}

// The editor on the focused tile (design round 12, F6 and F8): saves and Neovim as on the session page.
const editor = useEditorBridge(focusTerminal as unknown as Ref<EditorTerminal | null>, () => (focused.value?.kind === 'hosted' ? focused.value.hostName || 'the host' : 'this server'))
// The focused tile's activity, for the Files pane's Touched section (design 4e): what its connection replays and reports.
const focusActivity = ref<ActivityEntry[]>([])
function onFocusWelcome(w: Welcome) {
  editor.onWelcome(w)
  focusActivity.value = []
}
function onFocusActivity(e: ActivityEntry) {
  focusActivity.value = [...focusActivity.value.slice(-199), e]
}
watch(
  () => focused.value?.id,
  () => {
    editor.reset()
    focusActivity.value = []
  },
)

function requestFile(path: string, stat?: boolean, extra?: FileGetExtra) {
  if (!focusTerminal.value) return Promise.reject(new Error('terminal not ready'))
  return focusTerminal.value.requestFile(path, stat, extra)
}

function rawUrl(path: string) {
  if (focused.value?.kind !== 'server') return null
  return `${httpBase.value}/api/sessions/${encodeURIComponent(focused.value.id)}/files?path=${encodeURIComponent(path)}&raw=1&token=${encodeURIComponent(admin.token.value)}`
}

onMounted(() => {
  if (!admin.hasToken.value) admin.needsToken.value = true
  attention.start()
})
</script>

<template>
  <UDashboardPanel id="wall" :ui="{ body: 'p-0 sm:p-0 flex flex-col min-h-0 gap-0 overflow-hidden' }">
    <template #header>
      <UDashboardNavbar :toggle="false" :title="focusId ? focused?.name || 'Session' : 'Yard'" :ui="{ root: 'h-14' }">
        <template #leading>
          <UTooltip v-if="focusId" text="Back to the grid" :kbds="['escape']">
            <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" aria-label="Back to the grid" @click="backToGrid" />
          </UTooltip>
        </template>
        <template #trailing>
          <div class="flex items-center gap-2 ml-2">
            <template v-if="focusId">
              <SessionStatusBadge v-if="focused" :status="focused.status" :exit-code="focused.exitCode" />
              <AttentionBadge :attention="focused?.attention" />
              <TransportBadge :kind="transport.kind" :state="transport.state" :rtt="transport.rtt" />
              <UBadge :label="`${viewers} here`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
            </template>
            <div v-else class="flex items-center gap-1.5 text-xs">
              <UButton :label="`All ${active.length}`" size="xs" :variant="filter === 'all' ? 'solid' : 'outline'" color="neutral" @click="filter = 'all'" />
              <UButton :label="`Needs you ${waiting.length}`" size="xs" :variant="filter === 'needs' ? 'solid' : 'outline'" color="warning" @click="filter = 'needs'" />
              <UButton :label="`Running ${running.length}`" size="xs" :variant="filter === 'running' ? 'solid' : 'outline'" color="neutral" @click="filter = 'running'" />
              <UBadge v-if="!attention.connected.value" label="polling" color="warning" variant="subtle" size="sm" />
              <AttentionBar :sessions="attention.sessions.value" />
            </div>
          </div>
        </template>
        <template #right>
          <template v-if="focusId && focused">
            <UButton label="Open page" icon="i-lucide-square-terminal" color="neutral" variant="soft" :to="`/sessions/${focused.id}`" />
            <UButton v-if="isActive(focused)" label="Stop" icon="i-lucide-square" color="error" variant="soft" @click="stop(focused)" />
            <ResumeButton v-else-if="focused.kind === 'server'" :session="focused" />
          </template>
          <FullscreenButton />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="attention.error.value" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="attention.error.value" class="m-3" />

      <div v-if="focusId" class="flex flex-1 min-h-0 gap-3 p-2 sm:p-3">
        <EditorColumn v-if="focused" v-model:tabs="tabs" :request="requestFile" :nvim="editor.nvim.value" :write="editor.writeFile" :can-edit="editor.canEdit.value" :cwd="focused.cwd" :raw-url="rawUrl" :host-away="focused.status === 'host_disconnected' ? focused.hostName || 'The host' : undefined" :bar-text="focused.attention?.message || focused.agentId">
          <TerminalView
            :key="focused.id"
            ref="focusTerminal"
            :create-transport="transportFor(focused)"
            @welcome="onFocusWelcome"
            @activity="onFocusActivity"
            @nvim="editor.onNvim"
            @viewers="viewers = $event.count"
            @transport="transport = $event"
            @open-file="openFile"
            @open-url="openUrl"
          />
          <template #bar>
            <QuickReplyBar :attention="focused.attention" :agent-name="focused.agentId" role="control" :busy="quick.sending.value.has(focused.id)" @reply="reply(focused!, $event)" @option="option(focused!, $event)" />
          </template>
        </EditorColumn>
        <div v-else class="flex flex-1 flex-col items-center justify-center gap-3 text-muted">
          <UIcon name="i-lucide-search-x" class="size-8" />
          <p class="text-sm">That session is gone.</p>
          <UButton label="Back to the grid" icon="i-lucide-arrow-left" color="neutral" variant="soft" @click="backToGrid" />
        </div>
        <aside v-if="focused" class="hidden md:flex w-[332px] flex-none flex-col overflow-hidden rounded-md border border-default bg-default" data-files-aside>
          <div class="flex h-9 flex-none items-center gap-2 border-b border-default px-3 text-xs font-semibold text-highlighted"><UIcon name="i-lucide-folder-open" class="size-4 text-muted" /> Files</div>
          <FileBrowser :request="requestFile" :cwd="focused.cwd" :raw-url="rawUrl" :activity="focusActivity" external class="flex-1 min-h-0" @open="openFile" @open-diff="openDiff" @open-commit-diff="openCommitDiff" />
        </aside>
      </div>
      <div v-else-if="!active.length" class="flex-1 flex flex-col items-center justify-center gap-3 text-muted p-8">
        <UIcon name="i-lucide-layout-grid" class="size-10" />
        <p class="text-sm">No active sessions. Launch an agent or start one with <code>conductor host</code>.</p>
        <UButton label="Launch agent" icon="i-lucide-play" @click="useLaunchModal().show()" />
      </div>

      <div v-else class="flex flex-1 min-h-0">
        <WallQueue v-if="showQueue" ref="queue" :sessions="waiting" :answered="answered" :selected="queueSel" :sending="quick.sending.value" @reply="reply" @option="option" @open="focusSession" @select="queueSel = $event" />
        <div ref="grid" class="flex-1 min-h-0 p-3">
          <div v-if="!tiles.length" class="h-full flex items-center justify-center text-sm text-muted">Nothing matches this filter.</div>
          <div v-else class="grid h-full w-full gap-3" :style="gridStyle">
            <SessionTile v-for="s in tiles" :key="s.id" :session="s" :create-transport="transportFor(s)" @select="focusSession(s)" />
          </div>
        </div>
      </div>
    </template>
  </UDashboardPanel>

</template>
