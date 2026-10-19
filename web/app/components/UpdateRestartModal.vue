<script setup lang="ts">
import { restartWords } from '~/utils/desktop'
import { sessionLive } from '~/utils/sidebarActions'

/**
 * Restart to update (#290): what it ends, the server and the sessions running on this computer, counted as it opens; then the
 * app quits to install the update it downloaded. Later closes it; the header's button stays until the update installs.
 */
const { ready, restartOpen, install } = useDesktopUpdate()
const sessions = useSessions()
const running = ref<number | null>(null)
const busy = ref(false)
watch(restartOpen, async (open) => {
  if (!open) return
  running.value = null
  const list = await sessions.list().catch(() => [])
  running.value = list.filter((s) => s.kind === 'server' && sessionLive(s.status)).length
})
async function restart() {
  busy.value = true
  await install().catch(() => {})
  busy.value = false
}
</script>

<template>
  <UModal
    v-model:open="restartOpen"
    :title="`Restart to update to ${ready}?`"
    :description="running === null ? 'Counting the sessions running on this computer…' : restartWords(ready, running)"
    data-update-restart-modal
  >
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton label="Later" color="neutral" variant="outline" @click="restartOpen = false" />
        <UButton label="Restart to update" icon="i-lucide-circle-arrow-up" :loading="busy" :disabled="running === null" data-update-restart @click="restart" />
      </div>
    </template>
  </UModal>
</template>
