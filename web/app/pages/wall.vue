<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { isActive } from '~/utils/attention'

useHead({ title: 'Wall' })

const attention = useAttention()
const admin = useAdminToken()
const { create } = useTerminalTransport()

const WALL_KEY = 'conductor.wall.settings'
interface WallSettings {
  intervalMs: number
  autoplay: boolean
  follow: boolean
}
const wall = ref<WallSettings>({ intervalMs: 10000, autoplay: true, follow: true })
try {
  const raw = localStorage.getItem(WALL_KEY)
  if (raw) wall.value = { ...wall.value, ...JSON.parse(raw) }
} catch {
  /* ignore */
}
watch(
  wall,
  (v) => {
    try {
      localStorage.setItem(WALL_KEY, JSON.stringify(v))
    } catch {
      /* ignore */
    }
  },
  { deep: true },
)

const intervalItems = [
  { label: 'Every 5 s', value: 5000 },
  { label: 'Every 10 s', value: 10000 },
  { label: 'Every 20 s', value: 20000 },
  { label: 'Every 30 s', value: 30000 },
  { label: 'Every 60 s', value: 60000 },
]

const active = computed(() => attention.sessions.value.filter(isActive))
const waiting = computed(() => attention.needsInput.value.filter(isActive))
const carousel = useTemplateRef<{ emblaApi?: { scrollTo: (i: number, jump?: boolean) => void; selectedScrollSnap: () => number; plugins: () => { autoplay?: { play: () => void; stop: () => void; isPlaying: () => boolean } } } }>('carousel')
const selected = ref(0)
const paused = ref(false)
const holdUntil = ref(0)
const fullscreen = ref(false)

const current = computed<SessionInfo | undefined>(() => active.value[selected.value])

const autoplayOptions = computed(() =>
  wall.value.autoplay && active.value.length > 1
    ? { delay: wall.value.intervalMs, stopOnMouseEnter: true, stopOnInteraction: false, stopOnFocusIn: false }
    : false,
)

function autoplay() {
  return carousel.value?.emblaApi?.plugins().autoplay
}

function scrollTo(index: number, jump = false) {
  carousel.value?.emblaApi?.scrollTo(index, jump)
  selected.value = index
}

function onSelect(index: number) {
  selected.value = index
}

function pickTile(index: number) {
  scrollTo(index)
  holdUntil.value = Date.now() + 30000
  autoplay()?.stop()
  window.setTimeout(() => {
    if (Date.now() >= holdUntil.value && !paused.value && !waiting.value.length) autoplay()?.play()
  }, 30000)
}

function togglePlay() {
  paused.value = !paused.value
  if (paused.value) autoplay()?.stop()
  else autoplay()?.play()
}

// Follow mode: jump to sessions that need input and hold there.
let cycleTimer: number | undefined
watch(
  [waiting, () => wall.value.follow],
  ([list, follow]) => {
    window.clearInterval(cycleTimer)
    if (!follow) return
    if (list.length) {
      const target = active.value.findIndex((s) => s.id === list[0]!.id)
      if (target >= 0 && target !== selected.value) scrollTo(target)
      autoplay()?.stop()
      if (list.length > 1) {
        // Several waiting: cycle among them only.
        let i = 0
        cycleTimer = window.setInterval(() => {
          i = (i + 1) % list.length
          const idx = active.value.findIndex((s) => s.id === list[i]!.id)
          if (idx >= 0) scrollTo(idx)
        }, Math.max(wall.value.intervalMs, 5000))
      }
    } else if (!paused.value && Date.now() >= holdUntil.value) {
      autoplay()?.play()
    }
  },
  { immediate: true },
)

function toggleFullscreen() {
  if (document.fullscreenElement) document.exitFullscreen()
  else document.documentElement.requestFullscreen?.()
}
function onFullscreenChange() {
  fullscreen.value = !!document.fullscreenElement
}

function transportFor(s: SessionInfo) {
  return () => create({ sessionId: s.id, token: admin.token.value, kind: s.kind })
}

function near(index: number) {
  const n = active.value.length
  if (n <= 3) return true
  const d = Math.abs(index - selected.value)
  return d <= 1 || d === n - 1
}

onMounted(() => {
  if (!admin.hasToken.value) admin.needsToken.value = true
  attention.start()
  document.addEventListener('fullscreenchange', onFullscreenChange)
})
onBeforeUnmount(() => {
  window.clearInterval(cycleTimer)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
})
</script>

<template>
  <UDashboardPanel id="wall" :ui="{ body: 'p-0 sm:p-0 flex flex-col min-h-0 gap-0' }">
    <template #header>
      <UDashboardNavbar title="Wall">
        <template #trailing>
          <div class="flex items-center gap-2 ml-2">
            <UBadge :label="`${active.length} active`" color="neutral" variant="subtle" size="sm" />
            <UBadge v-if="waiting.length" :label="`${waiting.length} waiting`" icon="i-lucide-hand" color="secondary" variant="solid" size="sm" />
            <UBadge v-if="!attention.connected.value" label="polling" color="warning" variant="subtle" size="sm" />
          </div>
        </template>
        <template #right>
          <USwitch v-model="wall.follow" label="Follow input requests" size="sm" />
          <USwitch v-model="wall.autoplay" label="Auto-rotate" size="sm" />
          <USelect v-model="wall.intervalMs" :items="intervalItems" size="sm" class="w-32" :disabled="!wall.autoplay" />
          <UButton :icon="paused ? 'i-lucide-play' : 'i-lucide-pause'" color="neutral" variant="ghost" :aria-label="paused ? 'Resume rotation' : 'Pause rotation'" @click="togglePlay" />
          <UButton :icon="fullscreen ? 'i-lucide-minimize' : 'i-lucide-maximize'" color="neutral" variant="ghost" aria-label="Toggle fullscreen" @click="toggleFullscreen" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="attention.error.value" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="attention.error.value" class="m-3" />

      <div v-if="!active.length" class="flex-1 flex flex-col items-center justify-center gap-3 text-muted p-8">
        <UIcon name="i-lucide-layout-grid" class="size-10" />
        <p class="text-sm">No active sessions. Launch an agent or start one with <code>conductor host</code>.</p>
        <UButton label="Launch agent" icon="i-lucide-play" to="/" />
      </div>

      <div v-else class="flex-1 min-h-0 flex flex-col gap-3 p-3">
        <div class="flex-1 min-h-0">
          <UCarousel
            ref="carousel"
            v-slot="{ item, index }"
            :items="active"
            :autoplay="autoplayOptions"
            loop
            arrows
            dots
            fade
            class="h-full"
            :ui="{ viewport: 'h-full', container: 'h-full', item: 'h-full basis-full', dots: '-bottom-1' }"
            @select="onSelect"
          >
            <div class="flex h-full flex-col gap-2">
              <div class="flex items-center gap-2 px-1">
                <UIcon :name="item.kind === 'hosted' ? 'i-lucide-laptop' : 'i-lucide-server'" class="size-4 text-muted" />
                <span class="font-semibold truncate">{{ item.name }}</span>
                <span class="text-xs text-muted font-mono truncate hidden md:inline">{{ item.command.join(' ') }}</span>
                <AttentionBadge :attention="item.attention" />
                <SessionStatusBadge :status="item.status" :exit-code="item.exitCode" />
                <UBadge :label="`${item.viewers} viewer${item.viewers === 1 ? '' : 's'}`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
                <div class="flex-1" />
                <span class="text-xs text-muted">{{ index + 1 }} / {{ active.length }}</span>
                <UButton label="Open" icon="i-lucide-square-terminal" size="sm" color="neutral" variant="soft" :to="`/sessions/${item.id}`" />
              </div>
              <div class="flex-1 min-h-0">
                <TerminalView v-if="near(index)" :key="item.id" :create-transport="transportFor(item)" read-only :font-size="13" />
                <div v-else class="terminal-host rounded-lg border border-default flex items-center justify-center text-xs text-muted">idle</div>
              </div>
            </div>
          </UCarousel>
        </div>

        <div class="grid gap-2 grid-cols-2 md:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-6 max-h-[38%] overflow-y-auto pt-3">
          <SessionTile
            v-for="(s, i) in active"
            :key="s.id"
            :session="s"
            :active="i === selected"
            :create-transport="transportFor(s)"
            @select="pickTile(i)"
            @open="navigateTo(`/sessions/${s.id}`)"
          />
        </div>
      </div>
    </template>
  </UDashboardPanel>
</template>
