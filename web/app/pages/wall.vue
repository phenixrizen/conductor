<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TransportKind } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'
import type { FileTarget } from '~/components/FileViewer.vue'
import { WALL_SHORTCUTS } from '~/composables/useShortcuts'
import { isActive } from '~/utils/attention'
import { bestGrid } from '~/utils/wall'

useHead({ title: 'Wall' })

const route = useRoute()
const router = useRouter()
const attention = useAttention()
const admin = useAdminToken()
const api = useSessions()
const toast = useToast()
const { create } = useTerminalTransport()
const { httpBase } = useApiBase()
const fs = useFullscreenToggle()
useShortcutsModal().registerPage(WALL_SHORTCUTS)

const active = computed(() => attention.sessions.value.filter(isActive))
const waiting = computed(() => attention.needsInput.value.filter(isActive))

// Focus mode: /wall?focus=<id> expands one session in place. The grid is one
// Esc, one click or one browser Back away; the URL stays shareable.
const focusId = computed(() => (typeof route.query.focus === 'string' ? route.query.focus : ''))
const focused = computed<SessionInfo | undefined>(() => attention.sessions.value.find((s) => s.id === focusId.value))
const focusTerminal = ref<{ focus: () => void; requestFile: (p: string, s?: boolean) => Promise<any> } | null>(null)
const viewers = ref(0)
const transport = ref<{ kind: TransportKind; state: TransportState }>({ kind: 'ws', state: 'idle' })
const fileOpen = ref(false)
const fileTarget = ref<FileTarget | null>(null)
const previewUrl = ref<string | null>(null)
let fileClosedAt = 0
watch(fileOpen, (open) => {
  if (!open) fileClosedAt = Date.now()
})
watch(focusId, () => {
  viewers.value = 0
  transport.value = { kind: 'ws', state: 'idle' }
  fileTarget.value = null
  previewUrl.value = null
})

function focusSession(s: SessionInfo) {
  router.push({ path: '/wall', query: { focus: s.id } })
}

function backToGrid() {
  router.push({ path: '/wall' })
}

defineShortcuts({
  escape: () => {
    // Esc first closes the file panel (handled by the panel); only a second Esc leaves focus mode.
    if (!focusId.value || fileOpen.value || Date.now() - fileClosedAt < 400) return
    backToGrid()
  },
  f: () => fs.toggle(),
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
const layout = computed(() => bestGrid(active.value.length, box.value.w, box.value.h, 8, 1.6))
const gridStyle = computed(() => ({
  gridTemplateColumns: `repeat(${layout.value.cols}, minmax(0, 1fr))`,
  gridTemplateRows: `repeat(${layout.value.rows}, minmax(0, 1fr))`,
}))

function transportFor(s: SessionInfo) {
  return () => create({ sessionId: s.id, token: admin.token.value, kind: s.kind })
}

async function stop(s: SessionInfo) {
  try {
    await api.stop(s.id)
    toast.add({ title: 'Stop requested', description: s.name, color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, color: 'error' })
  }
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

function requestFile(path: string, stat?: boolean) {
  if (!focusTerminal.value) return Promise.reject(new Error('terminal not ready'))
  return focusTerminal.value.requestFile(path, stat)
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
      <UDashboardNavbar :title="focusId ? focused?.name || 'Session' : 'Wall'">
        <template #leading>
          <SidebarReveal />
          <UTooltip v-if="focusId" text="Back to the grid" :kbds="['escape']">
            <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" aria-label="Back to the grid" @click="backToGrid" />
          </UTooltip>
        </template>
        <template #trailing>
          <div class="flex items-center gap-2 ml-2">
            <template v-if="focusId">
              <SessionStatusBadge v-if="focused" :status="focused.status" :exit-code="focused.exitCode" />
              <AttentionBadge :attention="focused?.attention" />
              <TransportBadge :kind="transport.kind" :state="transport.state" />
              <UBadge :label="`${viewers} viewer${viewers === 1 ? '' : 's'}`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
            </template>
            <template v-else>
              <UBadge :label="`${active.length} active`" color="neutral" variant="subtle" size="sm" />
              <UBadge v-if="waiting.length" :label="`${waiting.length} waiting`" icon="i-lucide-hand" color="secondary" variant="solid" size="sm" />
              <UBadge v-if="!attention.connected.value" label="polling" color="warning" variant="subtle" size="sm" />
            </template>
          </div>
        </template>
        <template #right>
          <template v-if="focusId && focused">
            <UButton label="Open page" icon="i-lucide-square-terminal" color="neutral" variant="soft" :to="`/sessions/${focused.id}`" />
            <UButton v-if="isActive(focused)" label="Stop" icon="i-lucide-square" color="error" variant="soft" @click="stop(focused)" />
          </template>
          <UTooltip text="Toggle fullscreen" :kbds="['F']">
            <UButton :icon="fs.fullscreen.value ? 'i-lucide-minimize' : 'i-lucide-maximize'" color="neutral" variant="ghost" aria-label="Toggle fullscreen" @click="fs.toggle" />
          </UTooltip>
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="attention.error.value" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="attention.error.value" class="m-3" />

      <div v-if="focusId" class="flex-1 min-h-0 p-2 sm:p-3 flex flex-col gap-2">
        <UAlert
          v-if="focused?.attention?.state === 'needs_input'"
          color="secondary"
          variant="subtle"
          icon="i-lucide-hand"
          title="Agent is waiting for input"
          :description="focused?.attention?.message || 'Type into the terminal to continue.'"
          :actions="[{ label: 'Focus terminal', icon: 'i-lucide-keyboard', onClick: () => focusTerminal?.focus() }]"
        />
        <div class="flex-1 min-h-0">
          <TerminalView
            v-if="focused"
            :key="focused.id"
            ref="focusTerminal"
            :create-transport="transportFor(focused)"
            @viewers="viewers = $event"
            @transport="transport = $event"
            @open-file="openFile"
            @open-url="openUrl"
          />
          <div v-else class="h-full flex flex-col items-center justify-center gap-3 text-muted">
            <UIcon name="i-lucide-search-x" class="size-8" />
            <p class="text-sm">That session is gone.</p>
            <UButton label="Back to the grid" icon="i-lucide-arrow-left" color="neutral" variant="soft" @click="backToGrid" />
          </div>
        </div>
      </div>

      <div v-else-if="!active.length" class="flex-1 flex flex-col items-center justify-center gap-3 text-muted p-8">
        <UIcon name="i-lucide-layout-grid" class="size-10" />
        <p class="text-sm">No active sessions. Launch an agent or start one with <code>conductor host</code>.</p>
        <UButton label="Launch agent" icon="i-lucide-play" to="/" />
      </div>

      <div v-else ref="grid" class="flex-1 min-h-0 p-2 sm:p-3">
        <div class="grid h-full w-full gap-2" :style="gridStyle">
          <SessionTile v-for="s in active" :key="s.id" :session="s" :create-transport="transportFor(s)" @select="focusSession(s)" />
        </div>
      </div>
    </template>
  </UDashboardPanel>

  <FileViewer v-if="focusId" v-model:open="fileOpen" v-model:target="fileTarget" v-model:url="previewUrl" :request="requestFile" :cwd="focused?.cwd" :raw-url="rawUrl" />
</template>
