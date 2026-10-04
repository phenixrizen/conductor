<script setup lang="ts">
import type { AgentInfo, SessionInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { serverAgents } from '~/utils/agents'
import { splitArgs } from '~/utils/argv'
import { hostAdapter, hostCommand } from '~/utils/hostCommand'
import { effectiveYolo, yoloSummary } from '~/utils/yolo'

const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ launched: [session: SessionInfo] }>()

const api = useSessions()
const admin = useWorkbenchToken()
const toast = useToast()
const live = useAttention()
const { httpBase } = useApiBase()
const agents = ref<AgentInfo[]>([])
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const knownHosted = ref<Set<string>>(new Set())
/** The server's yolo default (GET /api/catalog). */
const yoloDefault = ref(false)

/** `yolo` is this launch's own choice; undefined follows the server's default. */
const state = reactive<{ agentId: string; runsOn: 'server' | 'local'; name: string; cwd: string; args: string; yolo?: boolean }>({ agentId: '', runsOn: 'server', name: '', cwd: '', args: '' })

const selected = computed(() => agents.value.find((a) => a.id === state.agentId))
/** The server tab offers the agents installed on the server; My machine offers every agent: what is installed there is the host's. */
const offered = computed(() => (state.runsOn === 'server' ? serverAgents(agents.value) : agents.value))
// The pick stays one the tab offers.
watch(offered, (list) => {
  if (!list.some((a) => a.id === state.agentId)) state.agentId = list[0]?.id ?? ''
})

watch(open, async (v) => {
  if (!v) return
  error.value = ''
  knownHosted.value = new Set(live.sessions.value.filter((s) => s.kind === 'hosted').map((s) => s.id))
  loading.value = true
  state.yolo = undefined
  try {
    const info = await api.catalogInfo()
    agents.value = info.agents
    yoloDefault.value = info.yoloDefault
    if (!state.agentId && offered.value[0]) state.agentId = offered.value[0].id
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
})

// Yolo: the launch's switch shows the server's default until it is moved;
// the preview shows what the recipe adds, and an agent without one says so.
const yolo = computed({
  get: () => effectiveYolo(state.yolo, yoloDefault.value),
  set: (on: boolean) => (state.yolo = on === yoloDefault.value ? undefined : on),
})
const extraArgs = computed(() => (selected.value?.allowArgs && state.args.trim() ? splitArgs(state.args) : []))
const yoloView = computed(() => yoloSummary(selected.value, yolo.value, extraArgs.value))

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
    // Its hooks too, for the signal a launch from the server wires them for.
    adapter: hostAdapter(selected.value),
  })
})

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
      yolo: state.yolo,
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
            v-for="a in offered"
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
          <p v-if="!offered.length && !loading" class="col-span-full text-sm text-muted" data-none-available>
            <template v-if="state.runsOn === 'server' && agents.length">No agent in the catalog is installed on this server. <NuxtLink to="/agents" class="underline" @click="open = false">See the Agents page</NuxtLink> for what is missing, or run one on your machine.</template>
            <template v-else>No agents in the catalog.</template>
          </p>
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
            <DirInput v-model="state.cwd" :placeholder="selected?.cwd || 'server default'" name="cwd" />
          </UFormField>
          <UFormField v-if="selected?.allowArgs" label="Extra arguments" name="args" hint="appended to the command">
            <UInput v-model="state.args" placeholder="--model opus" class="w-full font-mono" />
          </UFormField>
          <div class="flex flex-col gap-1.5" data-launch-yolo>
            <USwitch
              v-model="yolo"
              label="Yolo"
              :description="state.yolo === undefined ? `The server's default (${yoloDefault ? 'on' : 'off'})` : 'For this launch'"
              data-yolo-switch
            />
            <p v-if="yoloView.notice" class="flex items-center gap-1.5 text-xs text-warning" data-yolo-missing><UIcon name="i-lucide-triangle-alert" class="size-3.5 flex-none" />{{ yoloView.notice }}</p>
            <p v-else-if="yoloView.applies" class="text-xs text-muted">
              Skips its permission prompts: <code class="font-mono" data-yolo-argv>{{ yoloView.argv.join(' ') }}</code><template v-if="yoloView.env.length"> with {{ yoloView.env.join(', ') }}</template>
            </p>
          </div>
        </template>

        <template v-else>
          <UFormField v-if="selected?.allowArgs" label="Extra arguments" name="args" hint="appended to the command">
            <UInput v-model="state.args" placeholder="--model opus" class="w-full font-mono" />
          </UFormField>
          <UFormField label="Run this in your terminal" name="command">
            <CodeBlock :commands="[command]" wrap :disabled="!localReady" copy-title="Command copied" copy-description="It carries your workbench token; keep it private." />
            <template #hint><span>uses your workbench token; keep it private</span></template>
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
