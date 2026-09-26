<script setup lang="ts">
import type { AgentInfo } from '~/composables/useSessions'

useHead({ title: 'Agents' })

const api = useSessions()
const admin = useAdminToken()
const agents = ref<AgentInfo[]>([])
const loading = ref(false)
const error = ref('')
const launch = ref(false)

async function refresh() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  loading.value = true
  try {
    agents.value = await api.catalog()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
watch(() => admin.token.value, refresh)
</script>

<template>
  <UDashboardPanel id="agents">
    <template #header>
      <UDashboardNavbar title="Agents">
        <template #leading>
          <SidebarReveal />
        </template>
        <template #right>
          <UButton label="Launch agent" icon="i-lucide-play" @click="launch = true" />
        </template>
      </UDashboardNavbar>
    </template>
    <template #body>
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="mb-4" />
      <p class="text-sm text-muted mb-4">
        Agents come from the server catalog (built-in entries plus the <code>catalog</code> section of the config file). Commands are argv arrays; nothing goes through a shell.
      </p>
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <UCard v-for="a in agents" :key="a.id">
          <div class="flex items-start gap-3">
            <UIcon :name="a.icon || 'i-lucide-terminal'" class="size-6 text-primary flex-none mt-0.5" />
            <div class="min-w-0 flex-1">
              <div class="font-medium">{{ a.name }} <span class="text-xs text-muted font-mono">{{ a.id }}</span></div>
              <p v-if="a.description" class="text-sm text-muted">{{ a.description }}</p>
              <code class="block text-xs mt-2 truncate">{{ a.command.join(' ') }}</code>
              <div class="mt-2 flex gap-2">
                <UBadge v-if="a.allowArgs" label="accepts args" color="neutral" variant="subtle" size="sm" />
                <UBadge v-if="a.cwd" :label="a.cwd" color="neutral" variant="subtle" size="sm" />
              </div>
            </div>
          </div>
        </UCard>
      </div>
      <p v-if="!agents.length && !loading && !error" class="text-sm text-muted">No agents in the catalog.</p>
    </template>
  </UDashboardPanel>
  <LaunchSessionModal v-model:open="launch" @launched="(s) => navigateTo(`/sessions/${s.id}`)" />
</template>
