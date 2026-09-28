<script setup lang="ts">
import type { JoinInfo } from '~/composables/useSessions'
import type { Attention, TransportKind } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'
import type { FileTarget } from '~/components/FileViewer.vue'
import { parseLocation } from '~/utils/links'

definePageMeta({ layout: 'bare' })

const route = useRoute()
const api = useSessions()
const toast = useToast()
const identity = useIdentity()
const { create } = useTerminalTransport()

const token = computed(() => String(route.params.token))
const info = ref<JoinInfo | null>(null)
const error = ref('')
const joined = ref(false)
const nameDraft = ref('')
const viewers = ref(0)
const status = ref('')
const transport = ref<{ kind: TransportKind; state: TransportState; rtt: number | null }>({ kind: 'ws', state: 'idle', rtt: null })
const fileOpen = ref(false)
const fileTarget = ref<FileTarget | null>(null)
const previewUrl = ref<string | null>(null)
const pathInput = ref('')
const terminal = ref<{ requestFile: (p: string, s?: boolean) => Promise<any>; sendInput: (t: string) => boolean } | null>(null)
const attention = ref<Attention>({ state: '' })
function onAttention(msg: { state: string; message?: string; source?: string }) {
  attention.value = { ...(msg as Attention), since: new Date().toISOString() }
}

useHead({ title: computed(() => (info.value ? `${info.value.session.name} (shared)` : 'Join session')) })

const agentLabel = computed(() => {
  const a = info.value?.session.agentId ?? ''
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', agy: 'Antigravity' }
  return names[a] ?? a
})
const hostedBy = computed(() => {
  const s = info.value?.session
  if (!s) return ''
  return s.kind === 'hosted' ? `hosted by ${s.hostUser || s.hostName || 'a developer'}` : 'running on the server'
})

// The join API returns only metadata; nothing connects until the guest
// presses Join, so a fetched link never exposes terminal content.
onMounted(async () => {
  nameDraft.value = identity.name.value
  try {
    info.value = await api.join(token.value)
    status.value = info.value.session.status
  } catch (e) {
    error.value = (e as Error).message
  }
})

function join() {
  identity.set(nameDraft.value)
  joined.value = true
}

function createTransport() {
  return create({ sessionId: info.value!.session.id, token: token.value, kind: info.value!.session.kind, name: identity.name.value })
}

function reply(text: string) {
  if (!terminal.value?.sendInput(text + '\r')) toast.add({ title: 'Not connected', color: 'warning' })
}

function option(index: number) {
  const o = attention.value.options?.[index]
  if (o && !terminal.value?.sendInput(o.input)) toast.add({ title: 'Not connected', color: 'warning' })
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
  <template v-if="!joined">
    <main class="flex flex-1 items-center justify-center p-6">
      <div class="flex w-full max-w-sm flex-col gap-4 rounded-lg border border-default bg-default p-6 shadow-sm">
        <img src="/brand/conductor-mark.svg" alt="" class="size-8 dark:hidden" />
        <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-8 hidden dark:block" />
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-link-2-off" title="This link cannot be used" :description="error" />
        <template v-else-if="info">
          <div class="flex flex-col gap-1">
            <h1 class="text-lg font-semibold tracking-tight">Join {{ info.session.name }}</h1>
            <p class="text-sm leading-relaxed text-muted">
              A live {{ agentLabel }} session {{ hostedBy }}.
              <template v-if="info.role === 'control'">You'll have <b class="text-default">control</b>: your keystrokes reach the agent.</template>
              <template v-else>You'll be <b class="text-default">view only</b>: you can watch and open files.</template>
            </p>
          </div>
          <form class="flex flex-col gap-3" @submit.prevent="join">
            <UFormField label="Your name" name="name" hint="shown to others">
              <UInput v-model="nameDraft" placeholder="Priya Shah" class="w-full" autofocus maxlength="40" />
            </UFormField>
            <UButton type="submit" label="Join session" block :disabled="!nameDraft.trim()" />
          </form>
        </template>
        <div v-else class="text-sm text-muted flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Checking link…</div>
      </div>
    </main>
  </template>

  <template v-else-if="info">
    <header class="flex items-center gap-3 border-b border-default px-4 py-2">
      <img src="/brand/conductor-mark.svg" alt="" class="size-6 dark:hidden" />
      <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-6 hidden dark:block" />
      <span class="font-semibold">Conductor</span>
      <USeparator orientation="vertical" class="h-5" />
      <span class="truncate">{{ info.session.name }}</span>
      <UBadge :label="info.role === 'control' ? 'control' : 'view only'" :icon="info.role === 'control' ? 'i-lucide-keyboard' : 'i-lucide-eye'" :color="info.role === 'control' ? 'primary' : 'neutral'" variant="subtle" size="sm" />
      <SessionStatusBadge v-if="status" :status="status as any" />
      <AttentionBadge :attention="attention" />
      <TransportBadge :kind="transport.kind" :state="transport.state" :rtt="transport.rtt" />
      <UBadge :label="`${viewers} here`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
      <UBadge v-if="info.session.kind === 'hosted'" :label="`hosted on ${info.session.hostName || 'dev machine'}`" icon="i-lucide-laptop" color="neutral" variant="subtle" size="sm" />
      <div class="flex-1" />
      <span class="text-xs text-muted hidden md:inline">you are <b class="text-default">{{ identity.name.value }}</b></span>
      <form class="hidden md:flex items-center gap-1" @submit.prevent="openPath">
        <UInput v-model="pathInput" placeholder="open path[:line]" size="sm" class="w-56 font-mono" icon="i-lucide-file-search" />
      </form>
    </header>

    <main class="flex-1 min-h-0 p-2 sm:p-3 flex flex-col gap-2">
      <div class="flex-1 min-h-0">
        <TerminalView
          ref="terminal"
          :create-transport="createTransport"
          :read-only="info.role !== 'control'"
          @status="(s) => (status = s)"
          @attention="onAttention"
          @viewers="viewers = $event.count"
          @transport="transport = $event"
          @open-file="openFile"
          @open-url="openUrl"
        />
      </div>
      <QuickReplyBar :attention="attention" :agent-name="agentLabel" :role="info.role" @reply="reply" @option="option" />
    </main>

    <FileViewer v-model:open="fileOpen" v-model:target="fileTarget" v-model:url="previewUrl" :request="requestFile" />
  </template>
</template>
