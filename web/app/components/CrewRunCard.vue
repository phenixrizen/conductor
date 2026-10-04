<script setup lang="ts">
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import { memberTiles, shortRunId, startedClock } from '~/utils/crewWords'
import { runState } from '~/utils/runs'
import { relativeTime } from '~/utils/sessions'

/**
 * One live run on the Crews page: named by its crew and its start, its state
 * as a badge, its members as chips in the shape of the crew with each one's
 * status dot, a line for each member that asks a question (with the way to
 * answer it), how long it has been up, Stop and Open run.
 */
const props = defineProps<{ run: RunInfo; sessions: SessionInfo[]; now: number }>()

const api = useSessions()
const live = useAttention()
const toast = useToast()

const state = computed(() => runState(props.run, props.sessions))
const tiles = computed(() => memberTiles(props.run, props.sessions))
const states = computed(() => new Map(tiles.value.map((t) => [t.m.name, t.status])))
const needs = computed(() => state.value.needs)
const asks = computed(() =>
  tiles.value
    .filter((t) => t.status === 'needs_input')
    .map((t) => {
      const s = props.sessions.find((x) => x.id === t.m.sessionId)
      return { name: t.m.name, sessionId: t.m.sessionId, question: s?.attention?.message || 'waits on a prompt' }
    }),
)
const meta = computed(() => `up ${relativeTime(props.run.startedAt, props.now)} · server · ${props.run.isolation === 'worktree' ? 'worktrees' : 'shared cwd'}`)

const stopping = ref(false)
async function stop() {
  stopping.value = true
  try {
    live.applyRun(await api.stopRun(props.run.id))
    toast.add({ title: 'Run stopped', description: 'Every member was stopped; the worktrees and branches stay.', icon: 'i-lucide-square', color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    stopping.value = false
  }
}
</script>

<template>
  <article
    class="flex min-w-0 flex-col gap-3 rounded-lg border p-4"
    :class="needs ? 'border-warning ring-1 ring-warning/40' : 'border-default'"
    :data-run="run.id"
    :data-state="state.state"
    :aria-label="`${run.name}, run started ${startedClock(run.startedAt)}`"
  >
    <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      <UIcon name="i-lucide-circle-play" class="size-4.5 flex-none" :class="needs ? 'text-warning' : 'text-success'" />
      <NuxtLink :to="`/crews/${encodeURIComponent(run.crewId)}`" class="truncate text-[15px] font-semibold text-highlighted hover:underline">{{ run.name }}</NuxtLink>
      <span class="text-[13px] text-muted">run started {{ startedClock(run.startedAt) }}</span>
      <span class="font-mono text-[11px] text-dimmed" :title="run.id">{{ shortRunId(run.id) }}</span>
      <YoloBadge v-if="run.yolo" icon class="flex-none" />
      <UBadge :label="needs ? `${needs} need${needs === 1 ? 's' : ''} you` : 'running'" :color="needs ? 'warning' : 'success'" variant="subtle" size="sm" class="ml-auto flex-none" data-run-state />
    </div>

    <div class="overflow-x-auto" data-run-members>
      <!-- The chips carry each member's status for the tests and the tooltip; the dag draws them. -->
      <CrewMiniDag :members="run.members" size="chip" :states="states" :max-width="520" />
      <span v-for="t in tiles" :key="t.m.name" class="sr-only" :data-run-member="t.m.name" :data-status="t.status">{{ t.m.name }}: {{ t.words.text }}</span>
    </div>

    <div v-for="a in asks" :key="a.name" class="flex items-center gap-2 rounded-md bg-warning/10 px-2.5 py-2 text-[13px]" data-run-ask>
      <UIcon name="i-lucide-hand" class="size-3.5 flex-none text-warning" />
      <span class="min-w-0 truncate"><b class="font-semibold">{{ a.name }}</b> asks “{{ a.question }}”</span>
      <NuxtLink v-if="a.sessionId" :to="`/sessions/${encodeURIComponent(a.sessionId)}`" class="ml-auto flex-none font-medium text-highlighted hover:underline">Answer</NuxtLink>
    </div>

    <div class="flex flex-wrap items-center gap-2 font-mono text-[11.5px] text-muted">
      <span class="truncate">{{ meta }}</span>
      <div class="ml-auto flex flex-none items-center gap-1.5 font-sans">
        <UButton label="Stop" icon="i-lucide-square" size="xs" color="error" variant="soft" :loading="stopping" :aria-label="`Stop the run ${run.id}`" data-run-stop @click="stop" />
        <UButton :to="`/runs/${encodeURIComponent(run.id)}`" label="Open run" trailing-icon="i-lucide-arrow-right" size="xs" data-run-open />
      </div>
    </div>
  </article>
</template>
