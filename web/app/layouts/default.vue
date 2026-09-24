<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const { hasToken, clear } = useAdminToken()
const showToken = ref(false)
const colorMode = useColorMode()
const attention = useAttention()
const alerts = useAttentionSettings()

const items = computed<NavigationMenuItem[][]>(() => [
  [
    { label: 'Sessions', icon: 'i-lucide-terminal', to: '/' },
    { label: 'Wall', icon: 'i-lucide-layout-grid', to: '/wall', badge: attention.count.value ? { label: String(attention.count.value), color: 'secondary', variant: 'solid' } : undefined },
    { label: 'Agents', icon: 'i-lucide-bot', to: '/agents' },
  ],
])

function toggleTheme() {
  colorMode.preference = colorMode.value === 'dark' ? 'light' : 'dark'
}
</script>

<template>
  <UDashboardGroup>
    <UDashboardSidebar collapsible resizable :min-size="12" :default-size="16" :max-size="24" :ui="{ footer: 'border-t border-default' }">
      <template #header="{ collapsed }">
        <div class="flex items-center gap-2 px-1">
          <img src="/brand/conductor-mark.svg" alt="" class="size-7 dark:hidden" />
          <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-7 hidden dark:block" />
          <span v-if="!collapsed" class="font-semibold truncate">Conductor</span>
        </div>
      </template>

      <template #default="{ collapsed }">
        <UNavigationMenu :collapsed="collapsed" :items="items" orientation="vertical" />
      </template>

      <template #footer="{ collapsed }">
        <div class="flex flex-col gap-1 w-full">
          <UPopover>
            <UButton
              :label="collapsed ? undefined : 'Alerts'"
              :icon="alerts.settings.value.notifications ? 'i-lucide-bell-ring' : 'i-lucide-bell'"
              color="neutral"
              variant="ghost"
              class="w-full justify-start"
            />
            <template #content>
              <div class="p-3 flex flex-col gap-3 w-64">
                <p class="text-xs text-muted">When a session needs input:</p>
                <USwitch :model-value="alerts.settings.value.notifications" label="Browser notification" :description="alerts.permission.value === 'denied' ? 'Blocked by the browser' : undefined" :disabled="alerts.permission.value === 'denied' || alerts.permission.value === 'unsupported'" @update:model-value="alerts.setNotifications" />
                <USwitch :model-value="alerts.settings.value.chime" label="Chime" @update:model-value="alerts.setChime" />
                <p class="text-xs text-muted">The tab title and favicon always show the count.</p>
              </div>
            </template>
          </UPopover>
          <UButton
            :label="collapsed ? undefined : (hasToken ? 'Admin token set' : 'Set admin token')"
            :icon="hasToken ? 'i-lucide-key-round' : 'i-lucide-lock'"
            :color="hasToken ? 'neutral' : 'warning'"
            variant="ghost"
            class="w-full justify-start"
            @click="showToken = true"
          />
          <UButton
            :label="collapsed ? undefined : 'Theme'"
            icon="i-lucide-sun-moon"
            color="neutral"
            variant="ghost"
            class="w-full justify-start"
            @click="toggleTheme"
          />
          <UButton
            v-if="hasToken"
            :label="collapsed ? undefined : 'Forget token'"
            icon="i-lucide-log-out"
            color="neutral"
            variant="ghost"
            class="w-full justify-start"
            @click="clear()"
          />
        </div>
      </template>
    </UDashboardSidebar>

    <slot />

    <AdminTokenGate v-model:open="showToken" />
  </UDashboardGroup>
</template>
