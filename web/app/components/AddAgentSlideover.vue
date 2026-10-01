<script setup lang="ts">
import type { AgentInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { agentPayload, commandOf, formErrors, formFromAgent, type AgentForm, type Field } from '~/utils/agentForm'
import { slugId } from '~/utils/argv'

/**
 * Add an agent to the catalog, or edit one (`agent` set). Saving replaces the
 * agent with the same ID on the server, so an edit sends the whole agent:
 * fields this form has no control for (working directory, icon, adapter, the
 * tool-events flag) are carried over from the agent being edited.
 */
const props = defineProps<{
  agent?: AgentInfo
  /** IDs already in the catalog, to warn before an add replaces one. */
  takenIds?: string[]
}>()
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ saved: [agent: AgentInfo] }>()

const api = useSessions()
const toast = useToast()

let nextUid = 0
const uid = () => nextUid++
const form = reactive<AgentForm>(formFromAgent(undefined, uid))
const idTouched = ref(false)
/** The ID names the agent being replaced, so it stays fixed once the agent exists. */
const idLocked = ref(false)
const attempted = ref(false)
const error = ref('')
const saving = ref(false)
const testing = ref(false)

function reset() {
  Object.assign(form, formFromAgent(props.agent, uid))
  idTouched.value = !!props.agent
  idLocked.value = !!props.agent
  attempted.value = false
  error.value = ''
  scheduleCheck()
}

watch(open, (v) => {
  if (v) {
    // A toast from the last save sits over the footer buttons of a right-hand slideover.
    toast.clear()
    reset()
  } else {
    clearTimeout(checkTimer)
  }
})

// An error describes the form as it was submitted; editing it is the fix.
watch(
  form,
  () => {
    error.value = ''
  },
  { deep: true },
)

// The ID follows the name until someone types in the ID field, and stops for
// good once it is locked: after the first save it names the saved agent, and
// following a rename would save a second agent under the new ID.
watch(
  () => form.name,
  (n) => {
    if (!idTouched.value && !idLocked.value) form.id = slugId(n)
  },
)
function setId(v: string | number | null | undefined) {
  form.id = String(v ?? '')
  idTouched.value = form.id !== ''
}
const idHint = computed(() => (idLocked.value ? 'fixed once saved' : idTouched.value ? undefined : 'from the name'))
const replacing = computed(() => !idLocked.value && !!form.id && !!props.takenIds?.includes(form.id))

// Whether the program resolves on the server. Informational: saving never waits for it.
type Check = { state: 'idle' | 'pending' | 'missing' | 'failed' } | { state: 'found'; path: string }
const check = ref<Check>({ state: 'idle' })
let checkTimer: ReturnType<typeof setTimeout> | undefined
let checkSeq = 0

function scheduleCheck() {
  clearTimeout(checkTimer)
  const seq = ++checkSeq
  const program = commandOf(form)[0]?.trim()
  if (!program) {
    check.value = { state: 'idle' }
    return
  }
  check.value = { state: 'pending' }
  checkTimer = setTimeout(async () => {
    try {
      const r = await api.checkCommand(program)
      if (seq === checkSeq) check.value = r.found ? { state: 'found', path: r.path ?? program } : { state: 'missing' }
    } catch {
      if (seq === checkSeq) check.value = { state: 'failed' }
    }
  }, 400)
}
watch(() => commandOf(form)[0], scheduleCheck)
onBeforeUnmount(() => clearTimeout(checkTimer))

function addEnv() {
  form.env.push({ uid: uid(), key: '', value: '', masked: false })
}
function removeEnv(uid: number) {
  form.env = form.env.filter((r) => r.uid !== uid)
}

// The hook card needs an adapter to report through; until adapters can be
// picked here, only an agent that already has one (or already reports by hook)
// can use it, so editing a built-in never loses its wiring.
const hookAvailable = computed(() => !!props.agent?.adapter || props.agent?.signal?.kind === 'hook')
const signalItems = computed(() => [
  {
    value: 'hook',
    label: props.agent?.adapter ? `Hook command · adapter ${props.agent.adapter}` : 'Hook command',
    description: hookAvailable.value ? 'The agent reports through its own hooks.' : 'pick an adapter (coming with Events)',
    disabled: !hookAvailable.value,
  },
  { value: 'bell', label: 'Bell / OSC 9·777', description: 'A terminal bell or notification escape. The default.' },
  { value: 'pattern', label: 'Screen pattern', description: 'A regex matched against the last screen line.' },
  { value: 'none', label: 'None', description: 'Never flagged; you watch it yourself.' },
])

const errors = computed(() => formErrors(form))
const shown = computed<Partial<Record<Field, string>>>(() => (attempted.value ? errors.value : {}))

/** Validates, then saves. Returns the saved agent, or null with `error` set. */
async function persist(): Promise<AgentInfo | null> {
  error.value = ''
  attempted.value = true
  const first = Object.values(errors.value)[0]
  if (first) {
    error.value = first
    return null
  }
  try {
    const saved = await api.saveAgent(agentPayload(form, props.agent))
    // A rename typed while the request was in flight may have moved the ID (it
    // is not locked yet): lock the ID that was actually saved.
    form.id = saved.id
    idLocked.value = true
    emit('saved', saved)
    return saved
  } catch (e) {
    error.value = (e as Error).message
    return null
  }
}

async function save() {
  saving.value = true
  try {
    const saved = await persist()
    if (!saved) return
    toast.add({ title: 'Saved to catalog', description: saved.name, icon: 'i-lucide-check', color: 'success' })
    open.value = false
  } finally {
    saving.value = false
  }
}

async function testLaunch() {
  testing.value = true
  try {
    const saved = await persist()
    if (!saved) return
    try {
      const session = await api.create({ agentId: saved.id })
      toast.add({ title: 'Session started', description: session.name, color: 'success', icon: 'i-lucide-play' })
      open.value = false
      await navigateTo(`/sessions/${session.id}`)
    } catch (e) {
      const why = e instanceof ApiError ? `${e.message} (${e.code})` : (e as Error).message
      error.value = `Saved to the catalog, but it did not start: ${why}`
    }
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <USlideover
    v-model:open="open"
    :title="agent ? `Edit ${agent.name}` : 'Add agent'"
    description="Commands are argv arrays run on the server; nothing goes through a shell."
    :ui="{ content: 'sm:max-w-xl' }"
  >
    <template #body>
      <div class="flex flex-col gap-5">
        <div class="grid gap-3 sm:grid-cols-2">
          <UFormField label="Name" name="name" required :error="shown.name">
            <UInput v-model="form.name" placeholder="Aider" maxlength="60" autofocus class="w-full" />
          </UFormField>
          <UFormField label="ID" name="id" :hint="idHint" :error="shown.id">
            <UInput :model-value="form.id" placeholder="aider" maxlength="32" :disabled="idLocked" :ui="{ base: 'font-mono' }" class="w-full" @update:model-value="setId" />
          </UFormField>
          <p v-if="replacing" class="-mt-1 flex items-center gap-1.5 text-sm text-warning sm:col-span-2"><UIcon name="i-lucide-triangle-alert" class="size-4 flex-none" />An agent with this ID exists; saving replaces it.</p>
        </div>

        <UFormField label="Command" name="command" required :error="shown.command">
          <ArgvInput v-model="form.command" v-model:pending="form.pendingCommand" placeholder="aider --model sonnet" :invalid="!!shown.command" />
          <template #help>
            <span v-if="check.state === 'pending'" class="flex items-center gap-1.5"><UIcon name="i-lucide-loader-circle" class="size-3.5 flex-none animate-spin" />Checking the server…</span>
            <span v-else-if="check.state === 'found'" class="flex items-center gap-1.5 text-success"><UIcon name="i-lucide-check" class="size-3.5 flex-none" />Found on the server: <code class="font-mono">{{ check.path }}</code></span>
            <span v-else-if="check.state === 'missing'" class="flex items-center gap-1.5 text-warning"><UIcon name="i-lucide-triangle-alert" class="size-3.5 flex-none" />Not found on the server; hosts may still have it</span>
            <span v-else-if="check.state === 'failed'">Could not check the server.</span>
            <span v-else>Press Enter or Space after each argument. Quote to keep spaces.</span>
          </template>
        </UFormField>

        <UFormField label="Description" name="description" hint="optional">
          <UInput v-model="form.description" placeholder="What it is for" maxlength="200" class="w-full" />
        </UFormField>

        <div class="text-sm">
          <div class="font-medium text-default">Environment</div>
          <p class="mt-1 text-xs text-muted">
            Values stay on the server. Leave one empty to pass the variable through from the server's own environment.
            <template v-if="form.env.some((r) => r.masked)">A stored value is never shown again; remove its row and add it again to change it.</template>
          </p>
          <div class="mt-2 flex flex-col gap-2">
            <div v-for="row in form.env" :key="row.uid" class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-center gap-2">
              <template v-if="row.masked">
                <code class="truncate font-mono text-sm" :title="row.key">{{ row.key }}</code>
                <span class="flex items-center gap-1.5 text-sm text-muted"><UIcon name="i-lucide-lock" class="size-4 flex-none" />set on the server</span>
              </template>
              <template v-else>
                <UInput v-model="row.key" placeholder="NAME" size="sm" aria-label="Variable name" autocapitalize="off" spellcheck="false" :ui="{ base: 'font-mono' }" class="w-full" />
                <UInput v-model="row.value" type="password" autocomplete="new-password" placeholder="pass through" size="sm" aria-label="Value; empty passes the server's value through" class="w-full" />
              </template>
              <UButton icon="i-lucide-trash-2" size="xs" color="neutral" variant="ghost" :aria-label="`Remove ${row.key || 'variable'}`" @click="removeEnv(row.uid)" />
            </div>
            <p v-if="shown.env" class="text-sm text-error">{{ shown.env }}</p>
            <UButton label="Add variable" icon="i-lucide-plus" size="xs" color="neutral" variant="ghost" class="self-start" @click="addEnv" />
          </div>
        </div>

        <USwitch v-model="form.allowArgs" label="Accept extra arguments" description="The Launch dialog can append arguments to this command." />

        <div class="text-sm">
          <URadioGroup
            v-model="form.signal"
            :items="signalItems"
            variant="card"
            legend="How does it tell Conductor it needs you?"
            :ui="{ fieldset: 'mt-2 grid gap-2 sm:grid-cols-2', legend: 'font-medium text-default' }"
          />
          <UFormField v-if="form.signal === 'pattern'" label="Pattern" name="pattern" hint="RE2 regular expression" :error="shown.pattern" class="mt-3">
            <UInput v-model="form.pattern" placeholder="^> $" autocapitalize="off" spellcheck="false" :ui="{ base: 'font-mono' }" class="w-full" />
            <template #help>Matched against the last line on screen once output goes quiet.</template>
          </UFormField>
        </div>
      </div>
    </template>

    <template #footer>
      <div class="flex w-full flex-col gap-3">
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
        <div class="flex items-center gap-2">
          <!-- The header's close button does the same; on a phone the three would not fit. -->
          <UButton label="Cancel" color="neutral" variant="ghost" class="hidden sm:inline-flex" @click="open = false" />
          <div class="flex-1" />
          <UButton label="Test launch" icon="i-lucide-play" color="neutral" variant="outline" :loading="testing" :disabled="saving" @click="testLaunch" />
          <UButton label="Save to catalog" icon="i-lucide-check" :loading="saving" :disabled="testing" @click="save" />
        </div>
      </div>
    </template>
  </USlideover>
</template>
