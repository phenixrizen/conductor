<script setup lang="ts">
import { restartWords, updateBusyWords, updateGoneWords } from '~/utils/desktop'
import { sessionLive } from '~/utils/sidebarActions'

/**
 * Restart to update (#290): what it ends, the server and the sessions running on this computer, counted as it opens and again as
 * it is confirmed; then the app quits to install the version asked for. Later closes it; the header's button stays until the update installs. A version
 * the app no longer holds (a check found a newer one, or a download failed) restarts nothing: the dialog closes and says so,
 * when the app refuses it and as soon as the app says it is gone. While the app checks for updates, which may change the one it
 * holds, the restart is put off: the dialog stays, and says so.
 */
const { ready, restartFor, install } = useDesktopUpdate()
const sessions = useSessions()
const toast = useToast()
const running = ref<number | null>(null)
const busy = ref(false)
// The version shown, kept while the dialog closes.
const shown = ref('')
const open = computed({
  get: () => restartFor.value !== '',
  set: (v: boolean) => {
    if (!v) restartFor.value = ''
  },
})
function gone(version: string) {
  restartFor.value = ''
  toast.add({ ...updateGoneWords(version), icon: 'i-lucide-circle-arrow-up', color: 'warning', duration: 20000 })
}
// Bumped as the dialog opens and as it closes: a count stands only for the dialog it was taken for, open still.
let asked = 0
/** count is how many sessions run on this computer, -1 when they could not be counted, which never reads as none (restartWords). */
async function count(): Promise<number> {
  const list = await sessions.list().catch(() => null)
  return list ? list.filter((s) => s.kind === 'server' && sessionLive(s.status)).length : -1
}
// At once too: a restart asked outlives the dialog (the layout goes with a page that has none, an invite's join page), which
// finds it as it comes back.
watch(
  restartFor,
  async (version) => {
    const mine = ++asked
    if (!version) return
    if (ready.value !== version) return gone(version)
    shown.value = version
    running.value = null
    const n = await count()
    if (mine === asked) running.value = n
  },
  { immediate: true },
)
// The app says the update is gone, or that another is ready, while this asks for one.
watch(ready, (now) => {
  const asked = restartFor.value
  if (asked && now !== asked && !busy.value) gone(asked)
})
async function restart() {
  const mine = asked
  const version = restartFor.value
  const said = running.value ?? -1
  busy.value = true
  // Counted again as it is confirmed: sessions started since the dialog opened end too. More than it said (or none it can count,
  // where it said how many) is said, and asked again; "if any" said already covers any.
  const now = await count()
  if (mine !== asked) {
    busy.value = false
    return
  }
  if (said >= 0 && (now < 0 || now > said)) {
    running.value = now
    busy.value = false
    return
  }
  if (ready.value !== version) {
    busy.value = false
    return gone(version)
  }
  const outcome = await install(version).catch(() => 'gone' as const)
  // Started, the app quits to install. Otherwise nothing stopped.
  if (outcome === 'started') return
  busy.value = false
  if (outcome === 'busy') toast.add({ ...updateBusyWords(), icon: 'i-lucide-circle-arrow-up', color: 'info', duration: 10000 })
  else gone(version)
}
</script>

<template>
  <UModal
    v-model:open="open"
    :title="`Restart to update to ${shown}?`"
    :description="running === null ? 'Counting the sessions running on this computer…' : restartWords(shown, running)"
    data-update-restart-modal
  >
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton label="Later" color="neutral" variant="outline" @click="open = false" />
        <UButton label="Restart to update" icon="i-lucide-circle-arrow-up" :loading="busy" :disabled="running === null" data-update-restart @click="restart" />
      </div>
    </template>
  </UModal>
</template>
