<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const { hasToken, clear } = useAdminToken()
const showToken = ref(false)
const colorMode = useColorMode()

const items = computed<NavigationMenuItem[][]>(() => [
  [
    { label: 'Sessions', icon: 'i-lucide-terminal', to: '/' },
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
