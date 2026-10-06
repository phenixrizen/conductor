<script setup lang="ts">
import type { AgentInfo, RunInfo } from '~/composables/useSessions'
import { draftMember, memberNameError, memberNameFrom, type DraftMember } from '~/utils/crews'
import { runSubtitle, shortRunId } from '~/utils/crewWords'
import { relativeTime } from '~/utils/sessions'

/**
 * The run page's header: the run named by its crew and its start, how many
 * members need input and run, how long it has been up, and its actions: add
 * an agent mid-run, share the run (a run link), stop every member, and once
 * stopped resume it as a new run. Each action calls the server and hands the
 * run as it is after it to the page (`changed`).
 */
const props = defineProps<{ run: RunInfo | null; runId: string; counts: { needs: number; running: number }; now: number }>()
const emit = defineEmits<{ changed: [run: RunInfo] }>()

const api = useSessions()
const toast = useToast()
const live = useAttention()
const router = useRouter()

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
    toast.add({ title: `${m.name} joined the run`, icon: 'i-lucide-user-plus', color: 'success' })
  } catch (e) {
    addError.value = (e as Error).message
  } finally {
    adding.value = false
  }
}

const shareOpen = ref(false)

// Resume as new run: a new run of the crew in which every member with a conversation continues it in its kept worktree.
const resuming = ref(false)
async function resumeRun() {
  resuming.value = true
  try {
    const next = await api.resumeRun(props.runId)
    live.applyRun(next)
    toast.add({ title: 'Run resumed', description: `${next.id}: the conversations continue in their worktrees.`, icon: 'i-lucide-play', color: 'success' })
    await router.push(`/runs/${encodeURIComponent(next.id)}`)
  } catch (e) {
    toast.add({ title: 'Resume failed', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    resuming.value = false
  }
}

// Stop asks first.
const stopOpen = ref(false)
const stopping = ref(false)

async function stopAll() {
  stopping.value = true
  try {
    const run = await api.stopRun(props.runId)
    emit('changed', run)
    stopOpen.value = false
    toast.add({ title: 'Run stopped', description: 'Every member was stopped; the worktrees and branches stay.', icon: 'i-lucide-square', color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    stopping.value = false
  }
}
</script>

<template>
  <UDashboardNavbar :toggle="false" :ui="{ root: 'h-14', left: 'min-w-0 flex-1', right: 'flex-none' }" data-crew-run-header>
    <template #leading>
      <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" aria-label="Back to the list" class="lg:hidden" to="/sessions" data-back-to-list />
    </template>
    <template #title>
      <span class="flex min-w-0 items-baseline gap-2">
        <NuxtLink v-if="run" :to="`/crews/${encodeURIComponent(run.crewId)}`" class="truncate hover:underline" :title="`The saved crew ${run.name}`">{{ run.name }}</NuxtLink>
        <span v-else class="truncate">Run</span>
        <span v-if="run" class="flex-none truncate text-[13px] font-normal text-muted" :class="!run.label && 'hidden sm:inline'" data-run-subtitle>{{ runSubtitle(run) }}</span>
        <span class="hidden flex-none font-mono text-[11px] font-normal text-dimmed md:inline" :title="runId">{{ shortRunId(runId) }}</span>
      </span>
    </template>
    <template #trailing>
      <div v-if="run" class="ml-2 hidden min-w-0 items-center gap-1.5 sm:flex">
        <UBadge :label="`${counts.needs} need input`" :color="counts.needs ? 'warning' : 'neutral'" variant="subtle" size="sm" class="flex-none" />
        <UBadge :label="`${counts.running} running`" :color="counts.running ? 'success' : 'neutral'" variant="subtle" size="sm" class="flex-none" />
        <span class="ml-1 truncate font-mono text-xs text-muted">{{ uptime }}</span>
      </div>
    </template>
    <template #right>
      <!-- Icons only on a phone: the labels would push the name off the bar. -->
      <UButton icon="i-lucide-plus" color="neutral" variant="outline" aria-label="Add agent" :disabled="!run || stopped" @click="openAdd"><span class="hidden sm:inline">Add agent</span></UButton>
      <UButton icon="i-lucide-share-2" color="neutral" variant="outline" aria-label="Share" :disabled="!run" @click="shareOpen = true"><span class="hidden sm:inline">Share</span></UButton>
      <UButton v-if="stopped" icon="i-lucide-play" color="primary" variant="soft" aria-label="Resume as new run" :loading="resuming" data-run-resume @click="resumeRun"><span class="hidden sm:inline">Resume as new run</span></UButton>
      <UButton icon="i-lucide-square" color="error" variant="soft" aria-label="Stop" :disabled="!run || stopped" @click="stopOpen = true"><span class="hidden sm:inline">Stop</span></UButton>
      <FullscreenButton class="hidden sm:inline-flex" />
    </template>
  </UDashboardNavbar>
  <!-- On a phone the counts and the uptime take a line of their own: the bar keeps the name and the buttons. -->
  <div v-if="run" class="flex items-center gap-1.5 border-b border-default px-4 py-1.5 sm:hidden" data-run-header-strip>
    <UBadge :label="`${counts.needs} need input`" :color="counts.needs ? 'warning' : 'neutral'" variant="subtle" size="sm" class="flex-none" data-run-header-needs />
    <UBadge :label="`${counts.running} running`" :color="counts.running ? 'success' : 'neutral'" variant="subtle" size="sm" class="flex-none" data-run-header-running />
    <span class="min-w-0 truncate font-mono text-[11px] text-muted">{{ uptime }}</span>
  </div>

  <USlideover v-model:open="addOpen" title="Add an agent" description="It joins this run, not the saved crew. One that starts at launch starts now; its prompt is typed once it is ready." :ui="{ content: 'sm:max-w-4xl' }">
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
        <UButton label="Add to the run" icon="i-lucide-user-plus" :loading="adding" :disabled="addInvalid" @click="add" />
      </div>
    </template>
  </USlideover>

  <ShareLinksModal v-model:open="shareOpen" :run-id="runId" :session-name="run?.name" />

  <UModal v-model:open="stopOpen" :title="`Stop this run of ${run?.name || 'the crew'}?`" description="Every member's session is stopped; a member not started yet never starts. The worktrees and their branches stay, and the run can be resumed as a new run.">
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton label="Cancel" color="neutral" variant="ghost" @click="stopOpen = false" />
        <UButton label="Stop the run" icon="i-lucide-square" color="error" :loading="stopping" data-confirm-stop @click="stopAll" />
      </div>
    </template>
  </UModal>
</template>
