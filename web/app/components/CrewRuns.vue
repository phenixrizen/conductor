<script setup lang="ts">
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import { memberStatus } from '~/utils/crews'
import { memberDot, runBadge, runLive, runState } from '~/utils/runs'
import { relativeTime } from '~/utils/sessions'

/**
 * A crew's runs on the Crews page, newest first: each with its age, its state (with how many members wait), its members as avatars with
 * their status, a member's error, a link to its crew view and, while it goes, a Stop button. The first SHOWN show; the rest one click away.
 */
const props = defineProps<{ runs: RunInfo[]; sessions: SessionInfo[]; now: number }>()

const api = useSessions()
const live = useAttention()
const toast = useToast()

/** How many runs show before "Show N more". */
const SHOWN = 5
const all = ref(false)
const shown = computed(() => (all.value ? props.runs : props.runs.slice(0, SHOWN)))

const rows = computed(() =>
  shown.value.map((r) => {
    const st = runState(r, props.sessions)
    return {
      run: r,
      state: st,
      badge: runBadge(st.state, st.needs),
      live: runLive(st.state),
      members: r.members.map((m) => {
        const status = memberStatus(r, m, props.sessions)
        return { m, status, dot: memberDot(status) }
      }),
      errors: r.members.filter((m) => m.error),
    }
  }),
)

const stopping = ref('')
async function stop(r: RunInfo) {
  stopping.value = r.id
  try {
    live.applyRun(await api.stopRun(r.id))
    toast.add({ title: 'Run stopped', description: 'Every member was stopped; the worktrees and branches stay.', icon: 'i-lucide-square', color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    stopping.value = ''
  }
}
</script>

<template>
  <ul v-if="runs.length" class="flex flex-col gap-1.5" data-crew-runs>
    <li v-for="row in rows" :key="row.run.id" class="flex flex-col gap-1 rounded-md bg-elevated/40 px-2 py-1.5" :data-run="row.run.id" :data-state="row.state.state">
      <div class="flex min-w-0 items-center gap-1.5 text-xs">
        <NuxtLink :to="`/runs/${encodeURIComponent(row.run.id)}`" class="min-w-0 truncate font-medium text-default hover:underline" :title="row.run.id">
          {{ relativeTime(row.run.startedAt, now, { suffix: true }) }}
        </NuxtLink>
        <UBadge :label="row.badge.label" :color="row.badge.color" variant="subtle" size="sm" class="flex-none" data-run-state />
        <UBadge v-if="row.run.yolo" label="yolo" icon="i-lucide-shield-off" color="warning" variant="subtle" size="sm" class="flex-none" />
        <div class="flex-1" />
        <UButton :to="`/runs/${encodeURIComponent(row.run.id)}`" label="Open" size="xs" color="neutral" variant="ghost" :title="`Open the crew view of ${row.run.id}`" data-run-open />
        <UButton v-if="row.live" icon="i-lucide-square" size="xs" color="error" variant="ghost" :aria-label="`Stop the run ${row.run.id}`" :loading="stopping === row.run.id" data-run-stop @click="stop(row.run)" />
      </div>
      <div class="flex flex-wrap gap-1" data-run-members>
        <span v-for="x in row.members" :key="x.m.name" class="relative" :title="`${x.m.name} · ${x.dot.label}`" :data-run-member="x.m.name" :data-status="x.status">
          <SessionAvatar :agent-id="x.m.agentId" :solid="x.status === 'needs_input'" :dashed="x.status === 'ended' || x.status === 'pending'" />
          <span class="absolute -right-0.5 -top-0.5 size-2 rounded-full ring-2 ring-default" :class="x.dot.dot" aria-hidden="true" />
        </span>
      </div>
      <p v-for="m in row.errors" :key="m.name" class="truncate text-[11px] text-error" :title="m.error">{{ m.name }}: {{ m.error }}</p>
    </li>
    <li v-if="runs.length > SHOWN">
      <UButton :label="all ? 'Show fewer' : `Show ${runs.length - SHOWN} more`" size="xs" color="neutral" variant="link" @click="all = !all" />
    </li>
  </ul>
</template>
