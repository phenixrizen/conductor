<script setup lang="ts">
import type { JoinInfo } from '~/composables/useSessions'
import type { TransportKind } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'
import type { FileTarget } from '~/components/FileViewer.vue'
import { parseLocation } from '~/utils/links'

definePageMeta({ layout: 'bare' })

const route = useRoute()
const api = useSessions()
const toast = useToast()
const { create } = useTerminalTransport()

const token = computed(() => String(route.params.token))
const info = ref<JoinInfo | null>(null)
const error = ref('')
const viewers = ref(0)
const status = ref('')
const transport = ref<{ kind: TransportKind; state: TransportState }>({ kind: 'ws', state: 'idle' })
const fileOpen = ref(false)
const fileTarget = ref<FileTarget | null>(null)
const previewUrl = ref<string | null>(null)
const pathInput = ref('')
const terminal = ref<{ requestFile: (p: string, s?: boolean) => Promise<any> } | null>(null)
const attention = ref<{ state: string; message?: string; source?: string; since?: string }>({ state: '' })
function onAttention(msg: { state: string; message?: string; source?: string }) {
  attention.value = { ...msg, since: new Date().toISOString() }
}

useHead({ title: computed(() => (info.value ? `${info.value.session.name} (shared)` : 'Join session')) })

onMounted(async () => {
  try {
    info.value = await api.join(token.value)
    status.value = info.value.session.status
  } catch (e) {
    error.value = (e as Error).message
  }
})

function createTransport() {
  return create({ sessionId: info.value!.session.id, token: token.value, kind: info.value!.session.kind })
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
  if (loc.path) openFile(loc)
  pathInput.value = ''
}

function requestFile(path: string, stat?: boolean) {
  if (!terminal.value) return Promise.reject(new Error('terminal not ready'))
  return terminal.value.requestFile(path, stat)
}
</script>

<template>
  <header class="flex items-center gap-3 border-b border-default px-4 py-2">
    <img src="/brand/conductor-mark.svg" alt="" class="size-6 dark:hidden" />
    <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-6 hidden dark:block" />
    <span class="font-semibold">Conductor</span>
    <template v-if="info">
      <USeparator orientation="vertical" class="h-5" />
      <span class="truncate">{{ info.session.name }}</span>
      <UBadge :label="info.role === 'control' ? 'control' : 'view only'" :icon="info.role === 'control' ? 'i-lucide-keyboard' : 'i-lucide-eye'" :color="info.role === 'control' ? 'warning' : 'neutral'" variant="subtle" size="sm" />
      <SessionStatusBadge v-if="status" :status="status as any" />
      <AttentionBadge :attention="attention as any" />
      <TransportBadge :kind="transport.kind" :state="transport.state" />
      <UBadge :label="`${viewers} viewer${viewers === 1 ? '' : 's'}`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
      <UBadge v-if="info.session.kind === 'hosted'" :label="`hosted on ${info.session.hostName || 'dev machine'}`" icon="i-lucide-laptop" color="neutral" variant="subtle" size="sm" />
    </template>
    <div class="flex-1" />
    <form v-if="info" class="hidden md:flex items-center gap-1" @submit.prevent="openPath">
      <UInput v-model="pathInput" placeholder="open path[:line]" size="sm" class="w-56 font-mono" icon="i-lucide-file-search" />
    </form>
  </header>

  <main class="flex-1 min-h-0 p-2 sm:p-3 flex flex-col gap-2">
    <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-link-2-off" title="This link cannot be used" :description="error" />
    <UAlert v-else-if="attention.state === 'needs_input'" color="secondary" variant="subtle" icon="i-lucide-hand" title="Agent is waiting for input" :description="attention.message || (info?.role === 'control' ? 'Type into the terminal to continue.' : 'Someone with control needs to answer.')" />
    <TerminalView
      v-if="info && !error"
      ref="terminal"
      :create-transport="createTransport"
      :read-only="info.role !== 'control'"
      @status="(s) => (status = s)"
      @attention="onAttention"
      @viewers="viewers = $event"
      @transport="transport = $event"
      @open-file="openFile"
      @open-url="openUrl"
    />
    <div v-else-if="!error" class="p-6 text-sm text-muted flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Checking link…</div>
  </main>

  <FileViewer v-model:open="fileOpen" v-model:target="fileTarget" v-model:url="previewUrl" :request="requestFile" />
</template>
