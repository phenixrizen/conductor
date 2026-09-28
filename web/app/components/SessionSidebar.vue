<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { filterSessions, groupSessions, relativeTime, sessionMeta } from '~/utils/sessions'

const attention = useAttention()
const route = useRoute()
const launch = useLaunchModal()

const query = ref('')
const filterInput = useTemplateRef<{ inputRef?: HTMLInputElement }>('filterInput')
const now = ref(Date.now())
let tick: number | undefined

const groups = computed(() => groupSessions(filterSessions(attention.sessions.value, query.value)))
const empty = computed(() => attention.sessions.value.length === 0)

function active(id: string) {
  return route.path === `/sessions/${id}`
}

function exitLabel(s: SessionInfo) {
  const code = s.status === 'exited' && s.exitCode !== undefined ? `exit ${s.exitCode}` : s.status
  return `${code} · ${relativeTime(s.endedAt ?? s.createdAt, now.value, { suffix: true })}`
}

function focusFilter() {
  filterInput.value?.inputRef?.focus()
}

defineExpose({ focusFilter })

onMounted(() => {
  tick = window.setInterval(() => (now.value = Date.now()), 30000)
})
onBeforeUnmount(() => window.clearInterval(tick))
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex flex-col gap-2.5 px-1 pb-2">
      <UButton label="Launch agent" icon="i-lucide-plus" block @click="launch.show()">
        <template #trailing><UKbd value="N" size="sm" class="ml-auto opacity-70" /></template>
      </UButton>
      <UInput ref="filterInput" v-model="query" placeholder="Filter sessions, paths, people" size="sm" icon="i-lucide-slash" :ui="{ base: 'font-normal' }" />
    </div>

    <div class="flex-1 min-h-0 overflow-y-auto px-1 flex flex-col gap-3.5" data-session-list>
      <p v-if="empty" class="px-2 py-4 text-xs text-muted leading-relaxed">No sessions yet. Launch an agent here or run <code>conductor host</code> from your machine.</p>

      <section v-if="groups.needs.length" class="flex flex-col gap-0.5">
        <h3 class="flex items-center gap-2 px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-warning">
          Needs you <span class="rounded-full bg-warning/20 px-1.5 text-warning tracking-normal">{{ groups.needs.length }}</span>
        </h3>
        <NuxtLink
          v-for="s in groups.needs"
          :key="s.id"
          :to="`/sessions/${s.id}`"
          class="flex gap-2.5 rounded-md px-2.5 py-2 transition-colors"
          :class="active(s.id) ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60'"
        >
          <SessionAvatar :agent-id="s.agentId" solid />
          <div class="min-w-0 flex-1 flex flex-col gap-0.5">
            <div class="flex items-center gap-1.5">
              <span class="truncate text-sm font-semibold">{{ s.name }}</span>
              <span class="ml-auto size-2 rounded-full bg-warning flex-none" aria-hidden="true" />
            </div>
            <span class="truncate text-xs">{{ s.attention?.message || 'Waiting for input' }}</span>
            <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
          </div>
        </NuxtLink>
      </section>

      <section v-if="groups.running.length" class="flex flex-col gap-0.5">
        <h3 class="px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted">Running · {{ groups.running.length }}</h3>
        <NuxtLink
          v-for="s in groups.running"
          :key="s.id"
          :to="`/sessions/${s.id}`"
          class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 transition-colors"
          :class="active(s.id) ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60'"
        >
          <SessionAvatar :agent-id="s.agentId" />
          <div class="min-w-0 flex-1 flex flex-col">
            <span class="truncate text-sm font-medium">{{ s.name }}</span>
            <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
          </div>
          <span class="size-2 rounded-full flex-none" :class="s.status === 'running' ? 'bg-success' : 'bg-neutral-400'" aria-hidden="true" />
        </NuxtLink>
      </section>

      <section v-if="groups.exited.length" class="flex flex-col gap-0.5">
        <h3 class="px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted">Exited · {{ groups.exited.length }}</h3>
        <NuxtLink
          v-for="s in groups.exited"
          :key="s.id"
          :to="`/sessions/${s.id}`"
          class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 opacity-75 transition-colors"
          :class="active(s.id) ? 'bg-default border border-default shadow-xs opacity-100' : 'hover:bg-elevated/60'"
        >
          <SessionAvatar :agent-id="s.agentId" dashed />
          <div class="min-w-0 flex-1 flex flex-col">
            <span class="truncate text-sm font-medium">{{ s.name }}</span>
            <span class="truncate font-mono text-[11px] text-muted">{{ exitLabel(s) }}</span>
          </div>
        </NuxtLink>
      </section>
    </div>
  </div>
</template>
