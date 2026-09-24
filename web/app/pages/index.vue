<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { SessionInfo } from '~/composables/useSessions'

useHead({ title: 'Sessions' })

const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const attention = useAttention()
const sessions = attention.sessions
const loading = ref(false)
const error = computed(() => attention.error.value)
const launch = ref(false)
const shareFor = ref<string | null>(null)
const shareOpen = ref(false)

const columns: TableColumn<SessionInfo>[] = [
  { accessorKey: 'name', header: 'Session' },
  { accessorKey: 'agentId', header: 'Agent' },
  { accessorKey: 'kind', header: 'Where' },
  { accessorKey: 'status', header: 'Status' },
  { accessorKey: 'viewers', header: 'Viewers' },
  { accessorKey: 'createdAt', header: 'Started' },
  { id: 'actions', header: '' },
]

async function refresh(silent = false) {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  if (!silent) loading.value = true
  try {
    await attention.refresh()
  } finally {
    loading.value = false
  }
}

async function stop(s: SessionInfo) {
  try {
    await api.stop(s.id)
    toast.add({ title: s.status === 'running' ? 'Session stopped' : 'Session removed', description: s.name, color: 'neutral' })
    await refresh(true)
  } catch (e) {
    toast.add({ title: 'Failed', description: (e as Error).message, color: 'error' })
  }
}

function share(s: SessionInfo) {
  shareFor.value = s.id
  shareOpen.value = true
}

function since(ts: string) {
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000)
  if (s < 60) return `${Math.floor(s)}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

onMounted(() => {
  attention.start()
  refresh()
})
</script>

<template>
  <UDashboardPanel id="sessions">
    <template #header>
      <UDashboardNavbar title="Sessions">
        <template #right>
          <UButton icon="i-lucide-refresh-cw" color="neutral" variant="ghost" aria-label="Refresh" :loading="loading" @click="refresh()" />
          <UButton label="Launch agent" icon="i-lucide-play" @click="launch = true" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="mb-4" :actions="[{ label: 'Set token', onClick: () => (admin.needsToken.value = true) }]" />

      <UTable :data="sessions" :columns="columns" :loading="loading && !sessions.length" empty="No sessions yet. Launch an agent here or run `conductor host` from your machine.">
        <template #name-cell="{ row }">
          <NuxtLink :to="`/sessions/${row.original.id}`" class="font-medium hover:underline">{{ row.original.name }}</NuxtLink>
          <div class="text-xs text-muted font-mono truncate max-w-[28rem]">{{ row.original.command.join(' ') }}</div>
        </template>
        <template #kind-cell="{ row }">
          <UBadge :label="row.original.kind === 'hosted' ? `hosted · ${row.original.hostName || 'dev machine'}` : 'server'" :icon="row.original.kind === 'hosted' ? 'i-lucide-laptop' : 'i-lucide-server'" color="neutral" variant="subtle" size="sm" />
        </template>
        <template #status-cell="{ row }">
          <div class="flex items-center gap-1.5">
            <SessionStatusBadge :status="row.original.status" :exit-code="row.original.exitCode" />
            <AttentionBadge :attention="row.original.attention" />
          </div>
        </template>
        <template #viewers-cell="{ row }">
          <span class="inline-flex items-center gap-1"><UIcon name="i-lucide-users" class="size-4 text-muted" />{{ row.original.viewers }}</span>
        </template>
        <template #createdAt-cell="{ row }">
          <span :title="new Date(row.original.createdAt).toLocaleString()">{{ since(row.original.createdAt) }}</span>
        </template>
        <template #actions-cell="{ row }">
          <div class="flex justify-end gap-1">
            <UButton icon="i-lucide-square-terminal" size="xs" color="neutral" variant="ghost" aria-label="Open" :to="`/sessions/${row.original.id}`" />
            <UButton icon="i-lucide-share-2" size="xs" color="neutral" variant="ghost" aria-label="Share" @click="share(row.original)" />
            <UButton
              :icon="row.original.status === 'running' || row.original.status === 'starting' ? 'i-lucide-square' : 'i-lucide-trash-2'"
              size="xs"
              color="error"
              variant="ghost"
              :aria-label="row.original.status === 'running' ? 'Stop' : 'Remove'"
              @click="stop(row.original)"
            />
          </div>
        </template>
      </UTable>
    </template>
  </UDashboardPanel>

  <LaunchSessionModal v-model:open="launch" @launched="(s) => navigateTo(`/sessions/${s.id}`)" />
  <ShareLinksModal v-if="shareFor" v-model:open="shareOpen" :session-id="shareFor" />
</template>
