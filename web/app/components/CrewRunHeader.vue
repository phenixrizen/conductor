<script setup lang="ts">
import type { AgentInfo, RunInfo } from '~/composables/useSessions'
import { draftMember, memberNameError, memberNameFrom, type DraftMember } from '~/utils/crews'
import { relativeTime } from '~/utils/sessions'

/**
 * The crew view's header: the run's name, how many members need input and
 * run, how long it has been up, and its actions: add an agent mid-run, share
 * the crew (a run link) and stop every member. Each action calls the server
 * and hands the run as it is after it to the page (`changed`).
 */
const props = defineProps<{ run: RunInfo | null; runId: string; counts: { needs: number; running: number }; now: number }>()
const emit = defineEmits<{ changed: [run: RunInfo] }>()

const api = useSessions()
const toast = useToast()

const stopped = computed(() => !!props.run?.stoppedAt)
const uptime = computed(() => {
  const r = props.run
  if (!r) return ''
  const where = `server · ${r.isolation === 'worktree' ? 'worktrees' : 'shared'}`
  if (r.stoppedAt) return `stopped · ran ${relativeTime(r.startedAt, Date.parse(r.stoppedAt))} · ${where}`
  return `up ${relativeTime(r.startedAt, props.now)} · ${where}`
})

// Add agent: one row of the crew editor's members table.
const addOpen = ref(false)
const agents = ref<AgentInfo[]>([])
const member = ref<DraftMember[]>([])
const adding = ref(false)
const addError = ref('')
const runNames = computed(() => props.run?.members.map((m) => m.name) ?? [])
const addInvalid = computed(() => {
  const m = member.value[0]
  return !m || !m.agentId || !!memberNameError(m.name, runNames.value)
})

async function openAdd() {
  addError.value = ''
  addOpen.value = true
  try {
    if (!agents.value.length) agents.value = await api.catalog()
  } catch (e) {
    addError.value = (e as Error).message
  }
  const first = agents.value[0]
  member.value = first ? [draftMember({ name: memberNameFrom(first.id, runNames.value), agentId: first.id, prompt: '', start: { when: 'immediately' } })] : []
}

async function add() {
  const m = member.value[0]
  if (!m || addInvalid.value) return
  adding.value = true
  addError.value = ''
  try {
    const run = await api.addRunMember(props.runId, m)
    emit('changed', run)
    addOpen.value = false
    toast.add({ title: `${m.name} joined the crew`, icon: 'i-lucide-user-plus', color: 'success' })
  } catch (e) {
    addError.value = (e as Error).message
  } finally {
    adding.value = false
  }
}

const shareOpen = ref(false)

// Stop all asks first.
const stopOpen = ref(false)
const stopping = ref(false)

async function stopAll() {
  stopping.value = true
  try {
    const run = await api.stopRun(props.runId)
    emit('changed', run)
    stopOpen.value = false
    toast.add({ title: 'Crew stopped', description: 'Every member was stopped; the worktrees and branches stay.', icon: 'i-lucide-square', color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    stopping.value = false
  }
}
</script>

<template>
  <UDashboardNavbar :ui="{ root: 'h-14' }" data-crew-run-header>
    <template #leading>
      <SidebarReveal />
    </template>
    <template #title>
      <span class="truncate">{{ run?.name || 'Crew' }}</span>
    </template>
    <template #trailing>
      <!-- On a phone only the members needing input stay: they are what to act on. -->
      <div v-if="run" class="ml-2 flex min-w-0 items-center gap-1.5">
        <UBadge :label="`${counts.needs} need input`" :color="counts.needs ? 'warning' : 'neutral'" variant="subtle" size="sm" class="flex-none" />
        <UBadge :label="`${counts.running} running`" :color="counts.running ? 'success' : 'neutral'" variant="subtle" size="sm" class="hidden flex-none sm:inline-flex" />
        <span class="ml-1 hidden truncate font-mono text-xs text-muted sm:inline">{{ uptime }}</span>
      </div>
    </template>
    <template #right>
      <!-- Icons only on a phone: the labels would push the name off the bar. -->
      <UButton icon="i-lucide-plus" color="neutral" variant="outline" aria-label="Add agent" :disabled="!run || stopped" @click="openAdd"><span class="hidden sm:inline">Add agent</span></UButton>
      <UButton icon="i-lucide-share-2" color="neutral" variant="outline" aria-label="Share crew" :disabled="!run" @click="shareOpen = true"><span class="hidden sm:inline">Share crew</span></UButton>
      <UButton icon="i-lucide-square" color="error" variant="soft" aria-label="Stop all" :disabled="!run || stopped" @click="stopOpen = true"><span class="hidden sm:inline">Stop all</span></UButton>
    </template>
  </UDashboardNavbar>

  <USlideover v-model:open="addOpen" title="Add an agent" description="It joins this run. One that starts immediately starts now; its prompt is typed once it is ready." :ui="{ content: 'sm:max-w-4xl' }">
    <template #body>
      <div class="flex flex-col gap-4">
        <UAlert v-if="addError" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="addError" />
        <CrewMembersTable v-if="member.length" v-model="member" :agents="agents" :others="runNames" single />
        <p v-else-if="!addError" class="text-sm text-muted">No agents in the catalog.</p>
      </div>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton label="Cancel" color="neutral" variant="ghost" @click="addOpen = false" />
        <UButton label="Add to crew" icon="i-lucide-user-plus" :loading="adding" :disabled="addInvalid" @click="add" />
      </div>
    </template>
  </USlideover>

  <ShareLinksModal v-model:open="shareOpen" :run-id="runId" :session-name="run?.name" />

  <UModal v-model:open="stopOpen" :title="`Stop ${run?.name || 'the crew'}?`" description="Every member's session is stopped. The worktrees and their branches stay.">
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton label="Cancel" color="neutral" variant="ghost" @click="stopOpen = false" />
        <UButton label="Stop all" icon="i-lucide-square" color="error" :loading="stopping" data-confirm-stop @click="stopAll" />
      </div>
    </template>
  </UModal>
</template>
