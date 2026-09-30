<script setup lang="ts">
import type { JoinInfo, JoinRunMember, SessionKind } from '~/composables/useSessions'
import type { Attention, TransportKind } from '~/utils/protocol'
import type { TransportState } from '~/utils/transport/types'
import type { FileTarget } from '~/components/FileViewer.vue'
import { parseLocation } from '~/utils/links'
import { agentInitials } from '~/utils/sessions'

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

// A run link opens every member of a crew run: a tile each, and one member
// at a time in full with the link's role (`focus`). A session link opens its
// session in full.
const run = computed(() => info.value?.run)
const focus = ref<JoinRunMember | null>(null)
/** The session shown in full: a session link's, or the member opened from a run link's tiles. */
const current = computed<{ id: string; name: string; agentId: string; kind: SessionKind; hostName?: string; hostUser?: string } | null>(() => {
  const s = info.value?.session
  if (s) return s
  const m = focus.value
  // Crew members run on the server.
  return m?.sessionId ? { id: m.sessionId, name: m.name, agentId: m.agentId, kind: 'server' } : null
})

/** What each member's tile last heard from its session, by member name. */
const tileState = ref<Record<string, { status?: string; attention?: string }>>({})

useHead({ title: computed(() => (run.value ? `${run.value.name} (shared)` : info.value?.session ? `${info.value.session.name} (shared)` : 'Join session')) })

function agentName(agentId: string): string {
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', agy: 'Antigravity' }
  return names[agentId] ?? agentId
}
const agentLabel = computed(() => agentName(current.value?.agentId ?? ''))
const hostedBy = computed(() => {
  const s = current.value
  if (!s) return ''
  return s.kind === 'hosted' ? `hosted by ${s.hostUser || s.hostName || 'a developer'}` : 'running on the server'
})

// The join API returns only metadata; nothing connects until the guest
// presses Join, so a fetched link never exposes terminal content.
async function fetchInfo() {
  try {
    info.value = await api.join(token.value)
    if (info.value.session) status.value = info.value.session.status
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}

onMounted(() => {
  nameDraft.value = identity.name.value
  fetchInfo()
})

function join() {
  identity.set(nameDraft.value)
  joined.value = true
}

function createTransport() {
  const s = current.value!
  return create({ sessionId: s.id, token: token.value, kind: s.kind, name: identity.name.value })
}

function tileTransport(m: JoinRunMember) {
  return () => create({ sessionId: m.sessionId!, token: token.value, kind: 'server', name: identity.name.value })
}

function setTile(name: string, patch: { status?: string; attention?: string }) {
  tileState.value = { ...tileState.value, [name]: { ...tileState.value[name], ...patch } }
}

function tileStatus(m: JoinRunMember): { label: string; cls: string; dot: string } {
  const t = tileState.value[m.name]
  const st = t?.status ?? m.status
  if (t?.attention === 'needs_input' && (st === 'running' || st === 'starting')) return { label: 'Needs input', cls: 'text-warning', dot: 'bg-warning' }
  if (st === 'running') return { label: 'Running', cls: 'text-success', dot: 'bg-success' }
  return { label: st.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
}

function openMember(m: JoinRunMember) {
  if (!m.sessionId) return
  status.value = tileState.value[m.name]?.status ?? m.status
  attention.value = { state: '' }
  viewers.value = 0
  transport.value = { kind: 'ws', state: 'idle', rtt: null }
  fileTarget.value = null
  previewUrl.value = null
  focus.value = m
}

function backToCrew() {
  focus.value = null
  // Members may have started or ended meanwhile.
  fetchInfo()
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
          <div v-if="run" class="flex flex-col gap-1">
            <h1 class="text-lg font-semibold tracking-tight">Join {{ run.name }}</h1>
            <p class="text-sm leading-relaxed text-muted">
              A crew of {{ run.members.length }} {{ run.members.length === 1 ? 'agent' : 'agents' }} running on the server.
              <template v-if="info.role === 'control'">You'll have <b class="text-default">control</b>: your keystrokes reach the agent you open.</template>
              <template v-else>You'll be <b class="text-default">view only</b>: you can watch every agent and open files.</template>
            </p>
          </div>
          <div v-else-if="current" class="flex flex-col gap-1">
            <h1 class="text-lg font-semibold tracking-tight">Join {{ current.name }}</h1>
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
            <UButton type="submit" :label="run ? 'Join crew' : 'Join session'" block :disabled="!nameDraft.trim()" />
          </form>
        </template>
        <div v-else class="text-sm text-muted flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Checking link…</div>
      </div>
    </main>
  </template>

  <template v-else-if="run && !current">
    <header class="flex items-center gap-3 border-b border-default px-4 py-2">
      <img src="/brand/conductor-mark.svg" alt="" class="size-6 dark:hidden" />
      <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-6 hidden dark:block" />
      <span class="font-semibold">Conductor</span>
      <USeparator orientation="vertical" class="h-5" />
      <span class="truncate">{{ run.name }}</span>
      <UBadge :label="info!.role === 'control' ? 'control' : 'view only'" :icon="info!.role === 'control' ? 'i-lucide-keyboard' : 'i-lucide-eye'" :color="info!.role === 'control' ? 'primary' : 'neutral'" variant="subtle" size="sm" />
      <UBadge :label="`${run.members.length} ${run.members.length === 1 ? 'agent' : 'agents'}`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
      <div class="flex-1" />
      <span class="text-xs text-muted hidden md:inline">you are <b class="text-default">{{ identity.name.value }}</b></span>
      <UButton icon="i-lucide-refresh-cw" color="neutral" variant="ghost" size="sm" aria-label="Refresh the members" @click="fetchInfo" />
    </header>

    <main class="flex-1 min-h-0 overflow-y-auto p-2 sm:p-3">
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="mb-3" />
      <div class="grid auto-rows-[18rem] gap-3 sm:grid-cols-2 xl:grid-cols-3" data-join-tiles>
        <template v-for="m in run.members" :key="m.name">
          <div
            v-if="m.sessionId"
            class="flex min-h-0 cursor-pointer flex-col overflow-hidden rounded-lg border bg-elevated/40 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary"
            :class="tileState[m.name]?.attention === 'needs_input' ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented'"
            role="button"
            tabindex="0"
            :aria-label="`Open ${m.name}`"
            :data-member="m.name"
            @click="openMember(m)"
            @keydown.enter.prevent="openMember(m)"
            @keydown.space.prevent="openMember(m)"
          >
            <div class="flex flex-none items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
              <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
              <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
              <span class="flex items-center gap-1.5 text-[11.5px]" :class="tileStatus(m).cls"><span class="size-[7px] rounded-full" :class="tileStatus(m).dot" aria-hidden="true" />{{ tileStatus(m).label }}</span>
            </div>
            <div class="pointer-events-none min-h-0 flex-1">
              <TerminalView
                :create-transport="tileTransport(m)"
                read-only
                fit="scale"
                compact
                :auto-focus="false"
                @status="(s) => setTile(m.name, { status: s })"
                @attention="(a) => setTile(m.name, { attention: a.state })"
              />
            </div>
            <div class="flex flex-none items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted">
              <span class="truncate">{{ agentName(m.agentId) }}</span>
              <span class="ml-auto flex-none">{{ info!.role === 'control' ? 'open to type' : 'open' }}</span>
            </div>
          </div>
          <div v-else class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-dashed border-accented" :data-member="m.name">
            <div class="flex items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
              <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
              <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
            </div>
            <div class="flex flex-1 items-center justify-center p-3 text-sm text-muted">{{ m.status === 'ended' ? 'ended' : m.status === 'starting' ? 'starting…' : 'not started yet' }}</div>
          </div>
        </template>
      </div>
    </main>
  </template>

  <template v-else-if="info && current">
    <header class="flex items-center gap-3 border-b border-default px-4 py-2">
      <UTooltip v-if="run" text="Back to the crew">
        <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" size="sm" aria-label="Back to the crew" @click="backToCrew" />
      </UTooltip>
      <img src="/brand/conductor-mark.svg" alt="" class="size-6 dark:hidden" />
      <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-6 hidden dark:block" />
      <span class="font-semibold">Conductor</span>
      <USeparator orientation="vertical" class="h-5" />
      <span class="truncate"><template v-if="run">{{ run.name }} · </template>{{ current.name }}</span>
      <UBadge :label="info.role === 'control' ? 'control' : 'view only'" :icon="info.role === 'control' ? 'i-lucide-keyboard' : 'i-lucide-eye'" :color="info.role === 'control' ? 'primary' : 'neutral'" variant="subtle" size="sm" />
      <SessionStatusBadge v-if="status" :status="status as any" />
      <AttentionBadge :attention="attention" />
      <TransportBadge :kind="transport.kind" :state="transport.state" :rtt="transport.rtt" />
      <UBadge :label="`${viewers} here`" icon="i-lucide-users" color="neutral" variant="subtle" size="sm" />
      <UBadge v-if="current.kind === 'hosted'" :label="`hosted on ${current.hostName || 'dev machine'}`" icon="i-lucide-laptop" color="neutral" variant="subtle" size="sm" />
      <div class="flex-1" />
      <span class="text-xs text-muted hidden md:inline">you are <b class="text-default">{{ identity.name.value }}</b></span>
      <form class="hidden md:flex items-center gap-1" @submit.prevent="openPath">
        <UInput v-model="pathInput" placeholder="open path[:line]" size="sm" class="w-56 font-mono" icon="i-lucide-file-search" />
      </form>
    </header>

    <main class="flex-1 min-h-0 p-2 sm:p-3 flex flex-col gap-2">
      <div class="flex-1 min-h-0">
        <TerminalView
          :key="current.id"
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
