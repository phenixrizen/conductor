<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TransportKind } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'
import type { FileTarget } from '~/components/FileViewer.vue'
import { parseLocation } from '~/utils/links'

const route = useRoute()
const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const { httpBase } = useApiBase()
const { create } = useTerminalTransport()

const id = computed(() => String(route.params.id))
const session = ref<SessionInfo | null>(null)
const error = ref('')
const viewers = ref(0)
const transport = ref<{ kind: TransportKind; state: TransportState }>({ kind: 'ws', state: 'idle' })
const share = ref(false)
const fileOpen = ref(false)
const fileTarget = ref<FileTarget | null>(null)
const previewUrl = ref<string | null>(null)
const pathInput = ref('')
const ready = ref(false)
const terminal = ref<{ connect: () => void; requestFile: (p: string, s?: boolean) => Promise<any> } | null>(null)

useHead({ title: computed(() => session.value?.name || 'Session') })

async function load() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  try {
    const res = await api.get(id.value)
    session.value = res.session
    error.value = ''
    ready.value = true
  } catch (e) {
    error.value = (e as Error).message
  }
}

function createTransport() {
  return create({ sessionId: id.value, token: admin.token.value, kind: session.value?.kind ?? 'server' })
}

function onStatus(status: string, exitCode?: number) {
  if (session.value) {
    session.value.status = status as SessionInfo['status']
    if (exitCode !== undefined) session.value.exitCode = exitCode
  }
}

async function stop() {
  if (!session.value) return
  try {
    await api.stop(session.value.id)
    toast.add({ title: 'Stop requested', color: 'neutral' })
    await load()
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, color: 'error' })
  }
}

function openFile(loc: { path: string; line?: number }) {
  previewUrl.value = null
  fileTarget.value = { ...loc }
}

function openUrl(url: string) {
  toast.add({
    title: url,
    icon: 'i-lucide-link',
    color: 'neutral',
    actions: [
      { label: 'Open in new tab', icon: 'i-lucide-external-link', onClick: () => window.open(url, '_blank', 'noopener,noreferrer') },
      { label: 'Preview in pane', icon: 'i-lucide-panel-right', onClick: () => (previewUrl.value = url) },
    ],
  })
}

function openPath() {
  const loc = parseLocation(pathInput.value)
  if (!loc.path) return
  openFile(loc)
  pathInput.value = ''
}

function requestFile(path: string, stat?: boolean) {
  if (!terminal.value) return Promise.reject(new Error('terminal not ready'))
  return terminal.value.requestFile(path, stat)
}

function rawUrl(path: string) {
  if (session.value?.kind !== 'server') return null
  return `${httpBase.value}/api/sessions/${encodeURIComponent(id.value)}/files?path=${encodeURIComponent(path)}&raw=1&token=${encodeURIComponent(admin.token.value)}`
}

onMounted(load)
watch(() => admin.token.value, load)
watch(id, () => {
  ready.value = false
  session.value = null
  load()
})
</script>

<template>
  <UDashboardPanel :id="`session-${id}`" :ui="{ body: 'p-0 sm:p-0 flex flex-col min-h-0 gap-0' }">
    <template #header>
      <UDashboardNavbar :title="session?.name || 'Session'">
        <template #leading>
          <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" to="/" aria-label="Back to sessions" />
        </template>
        <template #trailing>
          <div class="flex items-center gap-2 ml-2">
            <SessionStatusBadge v-if="session" :status="session.status" :exit-code="session.exitCode" />
            <TransportBadge :kind="transport.kind" :state="transport.state" />
            <UBadge :label="`${viewers} viewer${viewers === 1 ? '' : 's'}`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
            <UBadge v-if="session?.kind === 'hosted'" :label="`hosted on ${session.hostName || 'dev machine'}`" icon="i-lucide-laptop" color="neutral" variant="subtle" size="sm" />
          </div>
        </template>
        <template #right>
          <form class="hidden md:flex items-center gap-1" @submit.prevent="openPath">
            <UInput v-model="pathInput" placeholder="open path[:line]" size="sm" class="w-56 font-mono" icon="i-lucide-file-search" />
          </form>
          <UButton label="Share" icon="i-lucide-share-2" color="neutral" variant="soft" @click="share = true" />
          <UButton
            v-if="session && (session.status === 'running' || session.status === 'starting')"
            label="Stop"
            icon="i-lucide-square"
            color="error"
            variant="soft"
            @click="stop"
          />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="m-4" />
      <div v-else-if="ready && session" class="flex-1 min-h-0 p-2 sm:p-3">
        <TerminalView
          ref="terminal"
          :create-transport="createTransport"
          @status="onStatus"
          @viewers="viewers = $event"
          @transport="transport = $event"
          @open-file="openFile"
          @open-url="openUrl"
        />
      </div>
      <div v-else class="p-6 text-sm text-muted flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading session…</div>
    </template>
  </UDashboardPanel>

  <ShareLinksModal v-model:open="share" :session-id="id" />
  <FileViewer v-model:open="fileOpen" v-model:target="fileTarget" v-model:url="previewUrl" :request="requestFile" :cwd="session?.cwd" :raw-url="rawUrl" />
</template>
