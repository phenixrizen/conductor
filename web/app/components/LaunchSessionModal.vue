<script setup lang="ts">
import type { AgentInfo, SessionInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { splitArgs } from '~/utils/argv'
import { hostCommand } from '~/utils/hostCommand'

const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ launched: [session: SessionInfo] }>()

const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const live = useAttention()
const { httpBase } = useApiBase()
const agents = ref<AgentInfo[]>([])
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const knownHosted = ref<Set<string>>(new Set())

const state = reactive<{ agentId: string; runsOn: 'server' | 'local'; name: string; cwd: string; args: string }>({ agentId: '', runsOn: 'server', name: '', cwd: '', args: '' })

const selected = computed(() => agents.value.find((a) => a.id === state.agentId))

watch(open, async (v) => {
  if (!v) return
  error.value = ''
  knownHosted.value = new Set(live.sessions.value.filter((s) => s.kind === 'hosted').map((s) => s.id))
  loading.value = true
  try {
    agents.value = await api.catalog()
    if (!state.agentId && agents.value[0]) state.agentId = agents.value[0].id
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
})

const server = computed(() => httpBase.value || (import.meta.client ? location.origin : ''))
const command = computed(() => {
  if (!selected.value) return ''
  const argv = [...selected.value.command, ...(selected.value.allowArgs && state.args.trim() ? splitArgs(state.args) : [])]
  const signal = selected.value.signal
  return hostCommand({
    server: server.value,
    token: admin.token.value,
    name: state.name.trim(),
    argv,
    cwd: state.cwd.trim() || undefined,
    // An agent that shows its prompt on screen is noticed by the host the way the server notices it.
    pattern: signal?.kind === 'pattern' ? signal.pattern : undefined,
    // Its hooks too: the server wires an adapter's hooks into a launch only for the hook signal.
    adapter: signal?.kind === 'hook' ? selected.value.adapter || undefined : undefined,
  })
})

async function copyCommand() {
  try {
    await navigator.clipboard.writeText(command.value)
    toast.add({ title: 'Command copied', description: 'It carries your admin token; keep it private.', icon: 'i-lucide-clipboard-check', color: 'success' })
  } catch {
    toast.add({ title: 'Copy failed', description: 'Select the command and copy it manually.', color: 'warning' })
  }
}

// "My machine": the dialog waits for a hosted session with this name that did
// not exist when it opened. Ids, not timestamps, so clock skew between the
// browser and the server cannot make it wait forever.
const localReady = computed(() => state.runsOn === 'local' && !!selected.value && !!state.name.trim())
const arrived = computed(() => {
  if (!open.value || !localReady.value) return null
  return live.sessions.value.find((s) => s.kind === 'hosted' && s.name === state.name.trim() && !knownHosted.value.has(s.id)) ?? null
})
watch(arrived, (s) => {
  if (!s) return
  toast.add({ title: 'Your machine connected', description: s.name, color: 'success', icon: 'i-lucide-laptop' })
  open.value = false
  emit('launched', s)
})

async function submit() {
  if (!state.agentId || state.runsOn !== 'server') return
  submitting.value = true
  error.value = ''
  try {
    const session = await api.create({
      agentId: state.agentId,
      name: state.name || undefined,
      cwd: state.cwd || undefined,
      args: selected.value?.allowArgs && state.args.trim() ? splitArgs(state.args) : undefined,
    })
    toast.add({ title: 'Session started', description: session.name, color: 'success', icon: 'i-lucide-play' })
    emit('launched', session)
    open.value = false
    state.name = ''
    state.args = ''
  } catch (e) {
    error.value = e instanceof ApiError ? `${e.message} (${e.code})` : (e as Error).message
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" title="Launch agent" :ui="{ content: 'max-w-xl', body: 'p-0' }">
    <template #body>
      <form class="flex flex-col gap-5 p-5" @submit.prevent="submit">
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />

        <div class="grid grid-cols-2 gap-2 sm:grid-cols-4" role="radiogroup" aria-label="Agent">
          <button
            v-for="a in agents"
            :key="a.id"
            type="button"
            role="radio"
            :aria-checked="state.agentId === a.id"
            class="flex flex-col gap-2 rounded-md border p-3 text-left transition-colors"
            :class="state.agentId === a.id ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'border-default hover:border-accented'"
            @click="state.agentId = a.id"
          >
            <SessionAvatar :agent-id="a.id" size="md" :solid="state.agentId === a.id" />
            <span class="text-sm" :class="state.agentId === a.id ? 'font-semibold' : 'font-medium'">{{ a.name }}</span>
          </button>
          <p v-if="!agents.length && !loading" class="col-span-full text-sm text-muted">No agents in the catalog.</p>
        </div>
        <p v-if="selected?.description" class="-mt-2 text-xs text-muted">{{ selected.description }} <code class="font-mono">{{ selected.command.join(' ') }}</code></p>

        <UFormField label="Runs on" name="runsOn">
          <div class="grid grid-cols-2 rounded-md bg-elevated p-0.5 text-sm">
            <button type="button" class="rounded py-1.5 transition-colors" :class="state.runsOn === 'server' ? 'bg-default font-semibold shadow-xs ring-1 ring-default' : 'text-muted'" @click="state.runsOn = 'server'">Server</button>
            <button type="button" class="rounded py-1.5 transition-colors" :class="state.runsOn === 'local' ? 'bg-default font-semibold shadow-xs ring-1 ring-default' : 'text-muted'" @click="state.runsOn = 'local'">My machine</button>
          </div>
        </UFormField>

        <UFormField label="Name" name="name" :hint="state.runsOn === 'local' ? 'required to spot it when it connects' : 'optional'" :error="state.runsOn === 'local' && !state.name.trim() ? 'Give the session a name first' : undefined">
          <UInput v-model="state.name" placeholder="e.g. auth-refactor" class="w-full" />
        </UFormField>

        <template v-if="state.runsOn === 'server'">
          <UFormField label="Working directory" name="cwd" hint="must be under an allowed root">
            <UInput v-model="state.cwd" :placeholder="selected?.cwd || 'server default'" class="w-full font-mono" />
          </UFormField>
          <UFormField v-if="selected?.allowArgs" label="Extra arguments" name="args" hint="appended to the command">
            <UInput v-model="state.args" placeholder="--model opus" class="w-full font-mono" />
          </UFormField>
        </template>

        <template v-else>
          <UFormField v-if="selected?.allowArgs" label="Extra arguments" name="args" hint="appended to the command">
            <UInput v-model="state.args" placeholder="--model opus" class="w-full font-mono" />
          </UFormField>
          <UFormField label="Run this in your terminal" name="command">
            <div class="flex items-start gap-3 rounded-md bg-forest-950 px-3.5 py-3 font-mono text-xs leading-relaxed text-forest-100" :class="!localReady && 'opacity-60'">
              <code class="flex-1 break-all select-all"><span class="text-forest-400">$</span> {{ command }}</code>
              <UButton label="Copy" size="xs" variant="link" color="success" class="flex-none" :disabled="!localReady" @click="copyCommand" />
            </div>
            <template #hint><span>uses your admin token; keep it private</span></template>
          </UFormField>
          <p class="text-xs leading-relaxed text-muted">Your terminal stays attached. The session appears here as <b class="text-default">hosted</b> once it connects, peer-to-peer when UDP allows.</p>
        </template>
      </form>
    </template>
    <template #footer>
      <div class="flex w-full items-center gap-2">
        <span v-if="localReady" class="flex items-center gap-2 text-xs text-muted"><span class="size-1.5 rounded-full bg-warning animate-pulse" aria-hidden="true" />Waiting for your machine…</span>
        <div class="flex-1" />
        <UButton label="Cancel" color="neutral" variant="ghost" @click="open = false" />
        <UButton v-if="state.runsOn === 'server'" label="Launch" icon="i-lucide-play" :loading="submitting" :disabled="!state.agentId" @click="submit" />
      </div>
    </template>
  </UModal>
</template>
