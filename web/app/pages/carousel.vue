<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { CAROUSEL_SHORTCUTS } from '~/composables/useShortcuts'
import { isActive } from '~/utils/attention'

useHead({ title: 'Carousel' })

const attention = useAttention()
const admin = useAdminToken()
const { create } = useTerminalTransport()
const fs = useFullscreenToggle()
useShortcutsModal().registerPage(CAROUSEL_SHORTCUTS)

const SETTINGS_KEY = 'conductor.carousel.settings'
const LEGACY_KEY = 'conductor.wall.settings'
interface CarouselSettings {
  intervalMs: number
  autoplay: boolean
  follow: boolean
}
const settings = ref<CarouselSettings>({ intervalMs: 10000, autoplay: true, follow: true })
try {
  const raw = localStorage.getItem(SETTINGS_KEY) ?? localStorage.getItem(LEGACY_KEY)
  if (raw) settings.value = { ...settings.value, ...JSON.parse(raw) }
} catch {
  /* ignore */
}
watch(
  settings,
  (v) => {
    try {
      localStorage.setItem(SETTINGS_KEY, JSON.stringify(v))
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
interface AutoplayPlugin {
  play: () => void
  stop: () => void
  isPlaying: () => boolean
}
interface EmblaLike {
  scrollTo: (i: number, jump?: boolean) => void
  scrollPrev: () => void
  scrollNext: () => void
  plugins: () => { autoplay?: AutoplayPlugin }
  on: (event: string, cb: () => void) => void
  off: (event: string, cb: () => void) => void
}
const carousel = useTemplateRef<{ emblaApi?: EmblaLike }>('carousel')
const selected = ref(0)
const paused = ref(false)
const typing = ref(false)
const current = computed<SessionInfo | undefined>(() => active.value[selected.value])

interface TerminalHandle {
  focus: () => void
}
const terminals = new Map<string, TerminalHandle>()
function terminalRef(id: string) {
  return (el: unknown) => {
    if (el) terminals.set(id, el as TerminalHandle)
    else terminals.delete(id)
  }
}

// The autoplay plugin stays loaded (re-creating it mid-scroll would snap the
// carousel back); rotation is started and stopped explicitly. It stops while
// a session needs input (follow mode), while a terminal has keyboard focus,
// while paused, and while the mouse hovers over the pane.
const holding = computed(() => settings.value.follow && waiting.value.length > 0)
const autoplayOptions = computed(() =>
  settings.value.autoplay && active.value.length > 1
    ? { delay: settings.value.intervalMs, stopOnMouseEnter: true, stopOnInteraction: false, stopOnFocusIn: false }
    : false,
)
const rotating = computed(() => !!autoplayOptions.value && !paused.value && !holding.value && !typing.value)

function autoplay() {
  return carousel.value?.emblaApi?.plugins().autoplay
}

function syncRotation() {
  const plugin = autoplay()
  if (!plugin) return
  if (rotating.value) plugin.play()
  else plugin.stop()
}

watch(rotating, syncRotation)
watch(
  () => carousel.value?.emblaApi,
  (api, _prev, onCleanup) => {
    if (!api) return
    // The plugin restarts itself on mouse-leave; re-apply our state when it does.
    const guard = () => {
      if (!rotating.value) autoplay()?.stop()
    }
    api.on('autoplay:play', guard)
    api.on('reInit', syncRotation)
    onCleanup(() => {
      api.off('autoplay:play', guard)
      api.off('reInit', syncRotation)
    })
    nextTick(syncRotation)
  },
  { immediate: true },
)

function scrollTo(index: number) {
  carousel.value?.emblaApi?.scrollTo(index)
  selected.value = index
}

function onSelect(index: number) {
  selected.value = index
}

/** Puts the keyboard in the current session's terminal. Nothing steals focus on its own. */
function focusCurrent() {
  const s = current.value
  if (s) terminals.get(s.id)?.focus()
}

function prev() {
  carousel.value?.emblaApi?.scrollPrev()
}

function next() {
  carousel.value?.emblaApi?.scrollNext()
}

function togglePlay() {
  paused.value = !paused.value
}

function onFocusIn(e: FocusEvent) {
  typing.value = !!(e.target as HTMLElement | null)?.closest?.('.terminal-host')
}

function onFocusOut(e: FocusEvent) {
  const to = e.relatedTarget as HTMLElement | null
  typing.value = !!to?.closest?.('.terminal-host')
}

// Follow mode: jump to the session that needs input and hold there. With
// several waiting, cycle among them only. Never yank the keyboard away
// from someone who is typing.
let cycleTimer: number | undefined
watch(
  [waiting, () => settings.value.follow],
  ([list, follow]) => {
    window.clearInterval(cycleTimer)
    if (!follow || !list.length) return
    const target = active.value.findIndex((s) => s.id === list[0]!.id)
    if (target >= 0 && target !== selected.value && !typing.value) scrollTo(target)
    if (list.length > 1) {
      let i = 0
      cycleTimer = window.setInterval(() => {
        if (typing.value) return
        i = (i + 1) % list.length
        const idx = active.value.findIndex((s) => s.id === list[i]!.id)
        if (idx >= 0) scrollTo(idx)
      }, Math.max(settings.value.intervalMs, 5000))
    }
  },
  { immediate: true },
)

defineShortcuts({
  arrowleft: prev,
  arrowright: next,
  enter: focusCurrent,
  ' ': togglePlay,
  f: () => fs.toggle(),
})

function transportFor(s: SessionInfo) {
  return () => create({ sessionId: s.id, token: admin.token.value, kind: s.kind })
}

// Keep the current slide and its neighbours connected; the rest stay idle.
function near(index: number) {
  const n = active.value.length
  if (n <= 3) return true
  const d = Math.abs(index - selected.value)
  return d <= 1 || d === n - 1
}

onMounted(() => {
  if (!admin.hasToken.value) admin.needsToken.value = true
  attention.start()
})
onBeforeUnmount(() => window.clearInterval(cycleTimer))
</script>

<template>
  <UDashboardPanel id="carousel" :ui="{ body: 'p-0 sm:p-0 flex flex-col min-h-0 gap-0 overflow-hidden' }">
    <template #header>
      <UDashboardNavbar title="Carousel">
        <template #leading>
          <SidebarReveal />
        </template>
        <template #trailing>
          <div class="flex items-center gap-2 ml-2">
            <UBadge :label="`${active.length} active`" color="neutral" variant="subtle" size="sm" />
            <UBadge v-if="waiting.length" :label="`${waiting.length} waiting`" icon="i-lucide-hand" color="secondary" variant="solid" size="sm" />
            <UBadge v-if="!attention.connected.value" label="polling" color="warning" variant="subtle" size="sm" />
          </div>
        </template>
        <template #right>
          <USwitch v-model="settings.follow" label="Follow input requests" size="sm" />
          <USwitch v-model="settings.autoplay" label="Auto-rotate" size="sm" />
          <USelect v-model="settings.intervalMs" :items="intervalItems" size="sm" class="w-32" :disabled="!settings.autoplay" />
          <UTooltip :text="paused ? 'Resume rotation' : 'Pause rotation'" :kbds="['space']">
            <UButton :icon="paused ? 'i-lucide-play' : 'i-lucide-pause'" color="neutral" variant="ghost" :aria-label="paused ? 'Resume rotation' : 'Pause rotation'" @click="togglePlay" />
          </UTooltip>
          <UTooltip text="Toggle fullscreen" :kbds="['F']">
            <UButton :icon="fs.fullscreen.value ? 'i-lucide-minimize' : 'i-lucide-maximize'" color="neutral" variant="ghost" aria-label="Toggle fullscreen" @click="fs.toggle" />
          </UTooltip>
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="attention.error.value" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="attention.error.value" class="m-3" />

      <div v-if="!active.length" class="flex-1 flex flex-col items-center justify-center gap-3 text-muted p-8">
        <UIcon name="i-lucide-gallery-horizontal" class="size-10" />
        <p class="text-sm">No active sessions. Launch an agent or start one with <code>conductor host</code>.</p>
        <UButton label="Launch agent" icon="i-lucide-play" to="/" />
      </div>

      <div v-else class="flex-1 min-h-0 p-3 pb-5" @focusin="onFocusIn" @focusout="onFocusOut">
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
          :ui="{ viewport: 'h-full', container: 'h-full', item: 'h-full basis-full', dots: '-bottom-4' }"
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
              <UButton label="Open page" icon="i-lucide-square-terminal" size="sm" color="neutral" variant="soft" :to="`/sessions/${item.id}`" />
            </div>
            <div class="flex-1 min-h-0">
              <TerminalView v-if="near(index)" :key="item.id" :ref="terminalRef(item.id)" :create-transport="transportFor(item)" :auto-focus="false" />
              <div v-else class="terminal-host rounded-lg border border-default flex items-center justify-center text-xs text-muted">idle</div>
            </div>
          </div>
        </UCarousel>
      </div>
    </template>
  </UDashboardPanel>
</template>
