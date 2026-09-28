<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { CAROUSEL_SHORTCUTS } from '~/composables/useShortcuts'
import { isActive } from '~/utils/attention'
import { relativeTime, shortCwd } from '~/utils/sessions'

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
  { label: '5s', value: 5000 },
  { label: '10s', value: 10000 },
  { label: '20s', value: 20000 },
  { label: '30s', value: 30000 },
  { label: '60s', value: 60000 },
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
const holdingId = computed(() => (holding.value && current.value?.attention?.state === 'needs_input' ? current.value.id : undefined))
const autoplayOptions = computed(() =>
  settings.value.autoplay && active.value.length > 1
    ? { delay: settings.value.intervalMs, stopOnMouseEnter: true, stopOnInteraction: false, stopOnFocusIn: false }
    : false,
)
const rotating = computed(() => !!autoplayOptions.value && !paused.value && !holding.value && !typing.value)
const stateLabel = computed(() => {
  if (typing.value) return 'Typing'
  if (paused.value) return 'Paused'
  if (holding.value) return 'Holding'
  if (!autoplayOptions.value) return 'Manual'
  return 'Rotating'
})

// Progress of the current interval, for the film strip.
const progress = ref(0)
let lastTick = Date.now()
let progressTimer: number | undefined
watch(rotating, (on) => {
  lastTick = Date.now()
  if (!on) progress.value = 0
})

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
  lastTick = Date.now()
  progress.value = 0
}

/** Puts the keyboard in the current session's terminal. Nothing steals focus on its own. */
function focusCurrent() {
  const s = current.value
  if (s) terminals.get(s.id)?.focus()
}

function leaveTerminal() {
  const el = document.activeElement as HTMLElement | null
  if (el?.closest?.('.terminal-host')) el.blur()
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
const jumpedAt = ref<string | null>(null)
let cycleTimer: number | undefined
watch(
  [waiting, () => settings.value.follow],
  ([list, follow]) => {
    window.clearInterval(cycleTimer)
    if (!follow || !list.length) {
      jumpedAt.value = null
      return
    }
    const target = active.value.findIndex((s) => s.id === list[0]!.id)
    if (target >= 0 && target !== selected.value && !typing.value) {
      scrollTo(target)
      jumpedAt.value = new Date().toISOString()
    } else if (!jumpedAt.value) jumpedAt.value = new Date().toISOString()
    if (list.length > 1) {
      let i = 0
      cycleTimer = window.setInterval(() => {
        if (typing.value) return
        i = (i + 1) % list.length
        const idx = active.value.findIndex((s) => s.id === list[i]!.id)
        if (idx >= 0) {
          scrollTo(idx)
          jumpedAt.value = new Date().toISOString()
        }
      }, Math.max(settings.value.intervalMs, 5000))
    }
  },
  { immediate: true },
)

// "Next up" follows the same order follow mode uses: waiting sessions first.
const nextUp = computed<{ session: SessionInfo; waiting: boolean } | null>(() => {
  const n = active.value.length
  if (n < 2) return null
  if (settings.value.follow && waiting.value.length) {
    const others = waiting.value.filter((s) => s.id !== current.value?.id)
    if (others.length) return { session: others[0]!, waiting: true }
  }
  const s = active.value[(selected.value + 1) % n]
  return s ? { session: s, waiting: s.attention?.state === 'needs_input' } : null
})

const now = ref(Date.now())
let tick: number | undefined

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

function agentLabel(s: SessionInfo) {
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', agy: 'Antigravity' }
  return names[s.agentId] ?? s.agentId
}

function meta(s: SessionInfo) {
  const where = s.kind === 'hosted' ? `hosted by ${s.hostUser || '?'} on ${s.hostName || 'dev machine'}` : 'server'
  return `${agentLabel(s)} · ${where} · ${shortCwd(s.cwd)}`
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
  tick = window.setInterval(() => (now.value = Date.now()), 1000)
  progressTimer = window.setInterval(() => {
    progress.value = rotating.value ? Math.min(1, (Date.now() - lastTick) / settings.value.intervalMs) : 0
  }, 250)
  window.addEventListener('keydown', onEscape)
})
function onEscape(e: KeyboardEvent) {
  if (e.key === 'Escape') leaveTerminal()
}
onBeforeUnmount(() => {
  window.clearInterval(cycleTimer)
  window.clearInterval(tick)
  window.clearInterval(progressTimer)
  window.removeEventListener('keydown', onEscape)
})
</script>

<template>
  <UDashboardPanel id="carousel" :ui="{ body: 'p-0 sm:p-0 flex flex-col min-h-0 gap-0 overflow-hidden' }">
    <template #header>
      <UDashboardNavbar title="Carousel" :ui="{ root: 'h-14' }">
        <template #leading>
          <SidebarReveal />
        </template>
        <template #trailing>
          <div class="flex items-center gap-2 ml-2">
            <span v-if="active.length" class="font-mono text-xs text-muted">{{ selected + 1 }} / {{ active.length }}</span>
            <UBadge v-if="!attention.connected.value" label="polling" color="warning" variant="subtle" size="sm" />
          </div>
        </template>
        <template #right>
          <div class="flex items-center gap-0.5 rounded-md border border-default p-0.5">
            <UButton icon="i-lucide-chevron-left" size="xs" color="neutral" variant="ghost" aria-label="Previous session" @click="prev" />
            <UButton :label="stateLabel" :icon="rotating ? 'i-lucide-play' : 'i-lucide-pause'" size="xs" color="neutral" variant="soft" class="font-semibold" @click="togglePlay" />
            <UButton icon="i-lucide-chevron-right" size="xs" color="neutral" variant="ghost" aria-label="Next session" @click="next" />
          </div>
          <span class="hidden md:flex items-center gap-1.5 text-xs text-muted">Every <USelect v-model="settings.intervalMs" :items="intervalItems" size="xs" class="w-20 font-mono" :disabled="!settings.autoplay" /></span>
          <USwitch v-model="settings.autoplay" label="Auto-rotate" size="sm" class="hidden md:flex" />
          <USwitch v-model="settings.follow" label="Follow input requests" size="sm" class="hidden lg:flex" />
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
        <UButton label="Launch agent" icon="i-lucide-play" @click="useLaunchModal().show()" />
      </div>

      <div v-else class="flex-1 min-h-0 flex flex-col" @focusin="onFocusIn" @focusout="onFocusOut">
        <div class="flex-1 min-h-0 px-4 pt-3">
          <UCarousel
            ref="carousel"
            v-slot="{ item, index }"
            :items="active"
            :autoplay="autoplayOptions"
            loop
            fade
            class="h-full"
            :ui="{ viewport: 'h-full', container: 'h-full', item: 'h-full basis-full' }"
            @select="onSelect"
          >
            <div class="flex h-full flex-col gap-3">
              <div class="flex items-center gap-3">
                <SessionAvatar :agent-id="item.agentId" size="md" solid />
                <div class="flex min-w-0 flex-col">
                  <div class="flex items-center gap-2"><span class="truncate text-base font-semibold">{{ item.name }}</span><AttentionBadge :attention="item.attention" /></div>
                  <span class="truncate font-mono text-[11.5px] text-muted">{{ meta(item) }}</span>
                </div>
                <div class="ml-auto flex items-center gap-2">
                  <UBadge v-if="holdingId === item.id && jumpedAt" color="warning" variant="subtle" size="sm" icon="i-lucide-hand" :label="`Jumped here ${relativeTime(jumpedAt, now)} ago. Stays until someone answers`" />
                  <UButton label="Open page" icon="i-lucide-square-terminal" size="sm" color="neutral" variant="outline" :to="`/sessions/${item.id}`" />
                </div>
              </div>
              <div class="relative flex-1 min-h-0 rounded-lg" :class="typing && index === selected ? 'ring-[3px] ring-primary ring-offset-2 ring-offset-default' : ''">
                <TerminalView v-if="near(index)" :key="item.id" :ref="terminalRef(item.id)" :create-transport="transportFor(item)" :auto-focus="false" />
                <div v-else class="terminal-host rounded-lg border border-default flex items-center justify-center text-xs text-muted">idle</div>
                <UBadge v-if="typing && index === selected" label="Paused: terminal has focus" icon="i-lucide-pause" color="neutral" variant="solid" size="sm" class="absolute top-3 right-4" />
              </div>
            </div>
          </UCarousel>
        </div>

        <div class="flex flex-col gap-2.5 px-4 pb-4 pt-3">
          <CarouselStrip :sessions="active" :selected="selected" :progress="progress" :holding-id="holdingId" :rotating="rotating" @select="scrollTo" />
          <div class="flex items-center gap-3 text-xs text-muted">
            <span v-if="nextUp">
              Next up: <b class="text-default">{{ nextUp.session.name }}</b><template v-if="nextUp.waiting">. It also needs input, so it goes before running sessions</template>
            </span>
            <span class="ml-auto hidden md:flex items-center gap-3 font-mono text-[11px]">
              <span>← → step</span><span>Space pause</span><span>Enter type</span><span>Esc leave terminal</span>
            </span>
          </div>
        </div>
      </div>
    </template>
  </UDashboardPanel>
</template>
