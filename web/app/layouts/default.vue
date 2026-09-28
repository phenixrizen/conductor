<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const { hasToken, clear } = useAdminToken()
const showToken = ref(false)
const colorMode = useColorMode()
const attention = useAttention()
const alerts = useAttentionSettings()
const sidebar = useSidebar()
const shortcuts = useShortcutsModal()
const launch = useLaunchModal()
const router = useRouter()
const list = useTemplateRef<{ focusFilter: () => void }>('list')

const nav = computed<NavigationMenuItem[]>(() => [
  { label: 'Wall', icon: 'i-lucide-layout-grid', to: '/wall', badge: attention.count.value ? { label: String(attention.count.value), color: 'warning', variant: 'solid' } : undefined },
  { label: 'Carousel', icon: 'i-lucide-gallery-horizontal', to: '/carousel' },
  { label: 'Agents', icon: 'i-lucide-bot', to: '/agents' },
])

function toggleTheme() {
  colorMode.preference = colorMode.value === 'dark' ? 'light' : 'dark'
}

// Shortcuts pause while a terminal or a text field has focus (see useShortcuts).
defineShortcuts({
  meta_b: () => sidebar.toggle(),
  '?': () => shortcuts.show(),
  n: () => launch.show(),
  '/': () => list.value?.focusFilter(),
  'g-w': () => router.push('/wall'),
  'g-c': () => router.push('/carousel'),
  'g-a': () => router.push('/agents'),
})
</script>

<template>
  <UDashboardGroup>
    <UDashboardSidebar
      :resizable="!sidebar.hidden.value"
      :min-size="14"
      :default-size="18"
      :max-size="26"
      :ui="{ root: sidebar.hidden.value ? 'lg:hidden' : undefined, body: 'gap-0 py-2', footer: 'border-t border-default flex-col items-stretch gap-1' }"
    >
      <template #header>
        <div class="flex items-center gap-2 px-1 w-full min-w-0">
          <img src="/brand/conductor-mark.svg" alt="" class="size-7 dark:hidden" />
          <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-7 hidden dark:block" />
          <span class="font-semibold truncate">Conductor</span>
          <div class="flex-1" />
          <UTooltip text="Hide sidebar" :kbds="['meta', 'B']">
            <UButton icon="i-lucide-panel-left-close" color="neutral" variant="ghost" size="sm" aria-label="Hide sidebar" class="hidden lg:inline-flex" @click="sidebar.hide()" />
          </UTooltip>
        </div>
      </template>

      <template #default>
        <SessionSidebar ref="list" />
      </template>

      <template #footer>
        <UNavigationMenu :items="nav" orientation="vertical" class="w-full" />
        <div class="flex items-center justify-between px-1 pt-1">
          <UPopover>
            <UTooltip text="Alerts">
              <UButton :icon="alerts.settings.value.notifications ? 'i-lucide-bell-ring' : 'i-lucide-bell'" color="neutral" variant="ghost" size="sm" aria-label="Alerts" />
            </UTooltip>
            <template #content>
              <div class="p-3 flex flex-col gap-3 w-64">
                <p class="text-xs text-muted">When a session needs input:</p>
                <USwitch :model-value="alerts.settings.value.notifications" label="Browser notification" :description="alerts.permission.value === 'denied' ? 'Blocked by the browser' : undefined" :disabled="alerts.permission.value === 'denied' || alerts.permission.value === 'unsupported'" @update:model-value="alerts.setNotifications" />
                <USwitch :model-value="alerts.settings.value.chime" label="Chime" @update:model-value="alerts.setChime" />
                <p class="text-xs text-muted">The tab title and favicon always show the count.</p>
              </div>
            </template>
          </UPopover>
          <UTooltip text="Keyboard shortcuts" :kbds="['?']">
            <UButton icon="i-lucide-keyboard" color="neutral" variant="ghost" size="sm" aria-label="Keyboard shortcuts" @click="shortcuts.show()" />
          </UTooltip>
          <UTooltip :text="hasToken ? 'Admin token set' : 'Set admin token'">
            <UButton :icon="hasToken ? 'i-lucide-key-round' : 'i-lucide-lock'" :color="hasToken ? 'neutral' : 'warning'" variant="ghost" size="sm" :aria-label="hasToken ? 'Admin token set' : 'Set admin token'" @click="showToken = true" />
          </UTooltip>
          <UTooltip text="Toggle theme">
            <UButton icon="i-lucide-sun-moon" color="neutral" variant="ghost" size="sm" aria-label="Toggle theme" @click="toggleTheme" />
          </UTooltip>
          <UTooltip v-if="hasToken" text="Forget token">
            <UButton icon="i-lucide-log-out" color="neutral" variant="ghost" size="sm" aria-label="Forget token" @click="clear()" />
          </UTooltip>
        </div>
      </template>
    </UDashboardSidebar>

    <slot />

    <AdminTokenGate v-model:open="showToken" />
    <ShortcutsModal />
    <LaunchSessionModal v-model:open="launch.open.value" @launched="(s) => navigateTo(`/sessions/${s.id}`)" />
  </UDashboardGroup>
</template>
