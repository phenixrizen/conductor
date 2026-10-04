<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'
import { sidebarRunFor } from '~/utils/crews'
import { SIDEBAR_SIZE } from '~/utils/sidebar'
import { noticeWords } from '~/utils/desktop'

const { hasToken, clear } = useWorkbenchToken()
const showToken = ref(false)
const colorMode = useColorMode()
const attention = useAttention()
const desktop = useDesktop()
const alerts = useAttentionSettings()
const sidebar = useSidebar()
const shortcuts = useShortcutsModal()
const launch = useLaunchModal()
// Fullscreen: one listener and one F shortcut for every page under this layout.
const fs = useFullscreenToggle()
fs.listen()
fs.shortcuts()
const identity = useIdentity()
const nameDraft = ref('')
const nameOpen = ref(false)

function saveName() {
  identity.set(nameDraft.value)
  nameOpen.value = false
}
const router = useRouter()
const toast = useToast()
const route = useRoute()
const list = useTemplateRef<{ focusFilter: () => void }>('list')

// The sidebar's collapsed state is the rail, and useSidebar (localStorage) is
// its one source: the group's own persistence is off (persistent false below),
// so no Nuxt UI cookie can bring back another state on load.
const railModel = computed({
  get: () => sidebar.rail.value,
  set: (collapsed: boolean) => (collapsed ? sidebar.collapse() : sidebar.expand()),
})

// The sidebar's fixed id. Nuxt UI names its root `<storageKey>-sidebar-<id>`,
// the group's storageKey being its default, 'dashboard'.
const SIDEBAR_ID = 'main'
const SIDEBAR_ROOT_ID = `dashboard-sidebar-${SIDEBAR_ID}`
function sidebarRoot() {
  return document.getElementById(SIDEBAR_ROOT_ID)
}
/** Where the desktop sidebar, and so the rail, shows: Tailwind's lg (its root is `hidden lg:flex`); below it the sidebar is a slideover. */
const DESKTOP_SIDEBAR = '(min-width: 64rem)'

/**
 * The filter box. On the rail the full sidebar opens first. Below lg the rail
 * is not shown and the mode is left alone: the key reaches the filter of an
 * open slideover, as before the rail, and does nothing otherwise.
 */
async function focusFilter() {
  if (sidebar.rail.value && window.matchMedia(DESKTOP_SIDEBAR).matches) {
    sidebar.expand()
    await nextTick()
  }
  list.value?.focusFilter()
}

/** The collapse and expand buttons go away with their mode: focus moves to the other one rather than falling to the page. */
async function collapseByButton() {
  sidebar.collapse()
  await nextTick()
  sidebarRoot()?.querySelector<HTMLElement>('[data-rail-expand]')?.focus()
}
async function expandByButton() {
  sidebar.expand()
  await nextTick()
  sidebarRoot()?.querySelector<HTMLElement>('[data-sidebar-collapse]')?.focus()
}

// The full sidebar's width survives a reload, kept by useSidebar too: with
// the group's persistence off, Nuxt UI keeps it for the visit only. Nuxt UI
// takes the starting width from defaultSize when the sidebar sets up, and
// goes back to defaultSize on a double-click on the handle: the saved width
// for the first, 18 % again after mount for the second.
const sidebarDefaultSize = ref(sidebar.size.value)
onMounted(() => (sidebarDefaultSize.value = SIDEBAR_SIZE.default))

// A notice the desktop app owes once (an upgrade that changed what sharing does): one toast with the way to Settings.
onMounted(async () => {
  const words = noticeWords((await desktop.bridge.value?.notice?.().catch(() => '')) ?? '')
  if (words) toast.add({ ...words, icon: 'i-lucide-globe', color: 'info', duration: 20000, actions: [{ label: 'Settings', onClick: () => router.push('/settings') }] })
})

/** Saves the full sidebar's width (Nuxt UI's `--width` on its root, in percent) once a drag or a double-click on the handle has set it. */
async function keepWidth() {
  await nextTick()
  if (sidebar.rail.value) return
  sidebar.resize(Number.parseFloat(sidebarRoot()?.style.getPropertyValue('--width') ?? ''))
}
/** A drag ends where the pointer is let go, anywhere on the page: Nuxt UI listens on the document, so does this. */
function keepWidthAfter(end: 'mouseup' | 'touchend') {
  document.addEventListener(end, keepWidth, { once: true })
}

// The sidebar's group variant: on a run page (/runs/<id>), and on the page
// of any member session of a run however it was reached, the sidebar lists
// only that run's members. The run's name comes from the live store's runs;
// the crew's id stands in for a run the server no longer keeps.
const sidebarRun = computed(() => sidebarRunFor(route.path, attention.sessions.value))
const runRoute = computed(() => route.path.startsWith('/runs/'))
const sidebarRunName = computed(() => {
  const id = sidebarRun.value
  if (!id) return undefined
  return attention.runNames.value[id] || attention.sessions.value.find((s) => s.crew?.runId === id)?.crew?.crewId
})

const nav = computed<NavigationMenuItem[]>(() => [
  { label: 'Yard', icon: 'i-lucide-layout-grid', to: '/yard', badge: attention.count.value ? { label: String(attention.count.value), color: 'warning', variant: 'solid' } : undefined },
  { label: 'Roundhouse', icon: 'i-lucide-gallery-horizontal', to: '/roundhouse' },
  { label: 'Agents', icon: 'i-lucide-bot', to: '/agents' },
  {
    label: 'Crews',
    icon: 'i-lucide-users',
    to: '/crews',
    active: route.path.startsWith('/crews') || runRoute.value,
  },
  { label: 'Events', icon: 'i-lucide-radio-tower', to: '/events' },
  ...(desktop.isDesktop.value ? [{ label: 'Settings', icon: 'i-lucide-settings', to: '/settings' }] : []),
])

// On the rail Nuxt UI's collapsed menu hides the links' labels and the Yard's
// count: each link is named by an aria-label instead, and the Yard's icon
// carries an amber chip while any session needs you.
const railNav = computed<NavigationMenuItem[]>(() =>
  nav.value.map((item) => {
    const n = item.to === '/yard' ? attention.count.value : 0
    return { ...item, 'aria-label': n ? `${item.label}, ${n} ${n === 1 ? 'needs' : 'need'} you` : item.label, chip: n ? { color: 'warning' } : undefined }
  }),
)

function toggleTheme() {
  colorMode.preference = colorMode.value === 'dark' ? 'light' : 'dark'
}

// Shortcuts pause while a terminal or a text field has focus (see useShortcuts).
// Plain keys work outside terminals and text fields; the Alt chords also
// work inside a terminal (see ALT_PASSTHROUGH_CODES in useShortcuts).
const inTerminal = { usingInput: true }
defineShortcuts({
  meta_b: () => sidebar.toggle(),
  '?': () => shortcuts.show(),
  n: () => launch.show(),
  '/': focusFilter,
  'g-w': () => router.push('/yard'),
  'g-c': () => router.push('/roundhouse'),
  'g-a': () => router.push('/agents'),
  'g-e': () => router.push('/events'),
  'g-r': () => router.push('/crews'),
  alt_b: { ...inTerminal, handler: () => sidebar.toggle() },
  alt_h: { ...inTerminal, handler: () => shortcuts.show() },
  alt_n: { ...inTerminal, handler: () => launch.show() },
  alt_s: { ...inTerminal, handler: focusFilter },
  alt_w: { ...inTerminal, handler: () => router.push('/yard') },
  alt_c: { ...inTerminal, handler: () => router.push('/roundhouse') },
  alt_a: { ...inTerminal, handler: () => router.push('/agents') },
  alt_e: { ...inTerminal, handler: () => router.push('/events') },
  alt_r: { ...inTerminal, handler: () => router.push('/crews') },
})
</script>

<template>
  <UDashboardGroup :persistent="false">
    <!-- The rail's classes are lg: only: below lg the slideover gets the same header, body and footer classes, and keeps the full sidebar's. -->
    <UDashboardSidebar
      :id="SIDEBAR_ID"
      v-model:collapsed="railModel"
      collapsible
      :resizable="!sidebar.rail.value"
      :min-size="SIDEBAR_SIZE.min"
      :default-size="sidebarDefaultSize"
      :max-size="SIDEBAR_SIZE.max"
      :ui="{
        header: sidebar.rail.value ? 'lg:px-0 lg:justify-center' : undefined,
        body: sidebar.rail.value ? 'gap-0 py-2 lg:px-1' : 'gap-0 py-2',
        footer: sidebar.rail.value ? 'border-t border-default flex-col items-stretch lg:items-center gap-1 lg:px-1' : 'border-t border-default flex-col items-stretch gap-1',
      }"
    >
      <template #header="{ collapsed }">
        <div v-if="collapsed" class="grid w-full place-items-center">
          <img src="/brand/conductor-mark.svg" alt="" class="size-7 dark:hidden" />
          <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-7 hidden dark:block" />
        </div>
        <div v-else class="flex items-center gap-2 px-1 w-full min-w-0">
          <img src="/brand/conductor-mark.svg" alt="" class="size-7 dark:hidden" />
          <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-7 hidden dark:block" />
          <span class="font-semibold truncate">Conductor</span>
          <div class="flex-1" />
          <UTooltip text="Collapse to the rail" :kbds="['meta', 'B']" ignore-non-keyboard-focus>
            <UButton icon="i-lucide-panel-left-close" color="neutral" variant="ghost" size="sm" aria-label="Collapse sidebar" class="hidden lg:inline-flex" data-sidebar-collapse @click="collapseByButton" />
          </UTooltip>
        </div>
      </template>

      <template #default="{ collapsed }">
        <SidebarRail v-if="collapsed" :run-id="sidebarRun" :run-name="sidebarRunName" @search="focusFilter" />
        <SessionSidebar v-else ref="list" :run-id="sidebarRun" :run-name="sidebarRunName" />
      </template>

      <!-- Nuxt UI's handle, with the width saved once a drag or a double-click has set it. -->
      <template #resize-handle="{ onMouseDown, onTouchStart, onDoubleClick, ui }">
        <UDashboardResizeHandle
          v-if="!sidebar.rail.value"
          :aria-controls="SIDEBAR_ROOT_ID"
          data-slot="handle"
          :class="ui.handle()"
          @mousedown="(e: MouseEvent) => { onMouseDown(e); keepWidthAfter('mouseup') }"
          @touchstart="(e: TouchEvent) => { onTouchStart(e); keepWidthAfter('touchend') }"
          @dblclick="(e: MouseEvent) => { onDoubleClick(e); keepWidth() }"
        />
      </template>

      <template #footer="{ collapsed }">
        <UNavigationMenu :items="collapsed ? railNav : nav" orientation="vertical" :collapsed="collapsed" tooltip :ui="{ link: collapsed ? 'justify-center' : undefined }" class="w-full" />
        <!-- The same utility buttons in both modes: in a row on the full sidebar, stacked with their tooltips to the right on the rail. -->
        <div class="flex items-center px-1 pt-1" :class="collapsed ? 'flex-col gap-1' : 'justify-between'" data-sidebar-tools>
          <UPopover :content="collapsed ? { side: 'right' } : undefined">
            <UTooltip text="Alerts" :content="collapsed ? { side: 'right' } : undefined">
              <UButton :icon="alerts.settings.value.notifications ? 'i-lucide-bell-ring' : 'i-lucide-bell'" color="neutral" variant="ghost" size="sm" aria-label="Alerts" />
            </UTooltip>
            <template #content>
              <div class="p-3 flex flex-col gap-3 w-64">
                <p class="text-xs text-muted">When a session needs input, and for events routed to Browser on the Events page:</p>
                <USwitch :model-value="alerts.settings.value.notifications" label="Browser notification" :description="alerts.permission.value === 'denied' ? 'Blocked by the browser' : undefined" :disabled="alerts.permission.value === 'denied' || alerts.permission.value === 'unsupported'" @update:model-value="alerts.setNotifications" />
                <USwitch :model-value="alerts.settings.value.chime" label="Chime" @update:model-value="alerts.setChime" />
                <p class="text-xs text-muted">The tab title and favicon always show the count.</p>
              </div>
            </template>
          </UPopover>
          <UPopover v-model:open="nameOpen" :content="collapsed ? { side: 'right' } : undefined" @update:open="(o: boolean) => o && (nameDraft = identity.name.value)">
            <UTooltip :text="identity.name.value ? `You are ${identity.name.value}` : 'Set your name'" :content="collapsed ? { side: 'right' } : undefined">
              <UButton :icon="identity.name.value ? 'i-lucide-user-round-check' : 'i-lucide-user-round'" color="neutral" variant="ghost" size="sm" aria-label="Your name" />
            </UTooltip>
            <template #content>
              <form class="p-3 flex flex-col gap-2 w-64" @submit.prevent="saveName">
                <p class="text-xs text-muted">Shown to others on a session. Defaults to the server's user; a label, not a login.</p>
                <UInput v-model="nameDraft" placeholder="Your name" size="sm" maxlength="40" />
                <UButton type="submit" label="Save" size="sm" class="self-end" />
              </form>
            </template>
          </UPopover>
          <UTooltip text="Keyboard shortcuts" :kbds="['?']" :content="collapsed ? { side: 'right' } : undefined">
            <UButton icon="i-lucide-keyboard" color="neutral" variant="ghost" size="sm" aria-label="Keyboard shortcuts" @click="shortcuts.show()" />
          </UTooltip>
          <UTooltip :text="hasToken ? 'Workbench token set' : 'Set workbench token'" :content="collapsed ? { side: 'right' } : undefined">
            <UButton :icon="hasToken ? 'i-lucide-key-round' : 'i-lucide-lock'" :color="hasToken ? 'neutral' : 'warning'" variant="ghost" size="sm" :aria-label="hasToken ? 'Workbench token set' : 'Set workbench token'" @click="showToken = true" />
          </UTooltip>
          <UTooltip text="Toggle theme" :content="collapsed ? { side: 'right' } : undefined">
            <UButton icon="i-lucide-sun-moon" color="neutral" variant="ghost" size="sm" aria-label="Toggle theme" @click="toggleTheme" />
          </UTooltip>
          <UTooltip v-if="hasToken" text="Forget token" :content="collapsed ? { side: 'right' } : undefined">
            <UButton icon="i-lucide-log-out" color="neutral" variant="ghost" size="sm" aria-label="Forget token" @click="clear()" />
          </UTooltip>
          <UTooltip v-if="collapsed" text="Expand sidebar" :kbds="['meta', 'B']" :content="{ side: 'right' }" ignore-non-keyboard-focus>
            <UButton icon="i-lucide-panel-left-open" color="neutral" variant="ghost" size="sm" aria-label="Expand sidebar" class="hidden lg:inline-flex" data-rail-expand @click="expandByButton" />
          </UTooltip>
        </div>
      </template>
    </UDashboardSidebar>

    <slot />

    <WorkbenchTokenGate v-model:open="showToken" />
    <ShortcutsModal />
    <LaunchSessionModal v-model:open="launch.open.value" @launched="(s) => navigateTo(`/sessions/${s.id}`)" />
  </UDashboardGroup>
</template>
