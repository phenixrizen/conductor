<script setup lang="ts">
import { groupSessions } from '~/utils/sessions'

useHead({ title: 'Sessions' })

const attention = useAttention()
const admin = useAdminToken()
const launch = useLaunchModal()

// The sidebar is the session list; this route only picks the first session
// worth looking at (someone waiting, else the newest running one).
const target = computed(() => {
  const g = groupSessions(attention.sessions.value)
  return g.needs[0] ?? g.running[0] ?? null
})

watch(
  target,
  (t) => {
    if (t) navigateTo(`/sessions/${t.id}`, { replace: true })
  },
  { immediate: true },
)

onMounted(() => {
  if (!admin.hasToken.value) admin.needsToken.value = true
  attention.start()
})
</script>

<template>
  <UDashboardPanel id="home">
    <template #header>
      <UDashboardNavbar title="Sessions">
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="attention.error.value" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="attention.error.value" class="mb-4" :actions="[{ label: 'Set token', onClick: () => (admin.needsToken.value = true) }]" />
      <div class="flex-1 flex flex-col items-center justify-center gap-3 text-muted p-8 text-center">
        <UIcon name="i-lucide-terminal" class="size-10" />
        <p class="text-sm">No active sessions. Launch an agent here or run <code>conductor host</code> from your machine.</p>
        <UButton label="Launch agent" icon="i-lucide-play" @click="launch.show()" />
      </div>
    </template>
  </UDashboardPanel>
</template>
