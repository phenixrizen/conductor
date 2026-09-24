<script setup lang="ts">
import type { AgentInfo, SessionInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'

const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ launched: [session: SessionInfo] }>()

const api = useSessions()
const toast = useToast()
const agents = ref<AgentInfo[]>([])
const loading = ref(false)
const submitting = ref(false)
const error = ref('')

const state = reactive({ agentId: '', name: '', cwd: '', args: '' })

const items = computed(() => agents.value.map((a) => ({ label: a.name, value: a.id, icon: a.icon || 'i-lucide-terminal' })))
const selected = computed(() => agents.value.find((a) => a.id === state.agentId))

watch(open, async (v) => {
  if (!v) return
  error.value = ''
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

function splitArgs(s: string): string[] {
  // Minimal shell-like splitting: whitespace separated, quotes group.
  const out: string[] = []
  const re = /"([^"]*)"|'([^']*)'|(\S+)/g
  for (const m of s.matchAll(re)) out.push(m[1] ?? m[2] ?? m[3] ?? '')
  return out
}

async function submit() {
  if (!state.agentId) return
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
  <UModal v-model:open="open" title="Launch an agent" description="Starts the command in a PTY on the server. Share it afterwards with a link.">
    <template #body>
      <form class="flex flex-col gap-4" @submit.prevent="submit">
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
        <UFormField label="Agent" name="agentId" required>
          <USelect v-model="state.agentId" :items="items" :loading="loading" placeholder="Choose an agent" class="w-full" />
          <template v-if="selected" #hint>
            <span class="font-mono text-xs">{{ selected.command.join(' ') }}</span>
          </template>
        </UFormField>
        <p v-if="selected?.description" class="text-sm text-muted -mt-2">{{ selected.description }}</p>
        <UFormField label="Name" name="name" hint="optional">
          <UInput v-model="state.name" placeholder="e.g. refactor auth module" class="w-full" />
        </UFormField>
        <UFormField label="Working directory" name="cwd" hint="must be under an allowed root">
          <UInput v-model="state.cwd" :placeholder="selected?.cwd || 'server default'" class="w-full font-mono" />
        </UFormField>
        <UFormField v-if="selected?.allowArgs" label="Extra arguments" name="args" hint="appended to the command">
          <UInput v-model="state.args" placeholder="--model opus" class="w-full font-mono" />
        </UFormField>
        <div class="flex justify-end gap-2 pt-2">
          <UButton label="Cancel" color="neutral" variant="ghost" @click="open = false" />
          <UButton label="Launch" type="submit" icon="i-lucide-play" :loading="submitting" :disabled="!state.agentId" />
        </div>
      </form>
    </template>
  </UModal>
</template>
