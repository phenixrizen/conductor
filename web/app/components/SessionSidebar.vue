<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { filterSessions, relativeTime, sessionMeta } from '~/utils/sessions'
import { needsDotShown, runOpen, sessionOpen, sidebarGroups, sidebarSessions } from '~/utils/sidebar'

/**
 * The full sidebar. In each section (needs you, running, exited) the sessions of no run come first, then the members of each crew run under
 * a header naming the run, which links to its run page (sidebarGroups, as the rail groups them). With `runId`, only the sessions of that
 * run, under a header naming it (`runName`) with a link back to its run page.
 */
const props = defineProps<{ runId?: string; runName?: string }>()

const attention = useAttention()
const events = useEvents()
const route = useRoute()
const launch = useLaunchModal()

const query = ref('')
const filterInput = useTemplateRef<{ inputRef?: HTMLInputElement }>('filterInput')
const now = ref(Date.now())
let tick: number | undefined

const shown = computed(() => sidebarSessions(attention.sessions.value, props.runId))
const groups = computed(() => sidebarGroups(filterSessions(shown.value, query.value), attention.runNames.value))
const count = (key: 'needs' | 'running' | 'exited') => groups.value[key].reduce((n, g) => n + g.sessions.length, 0)
const needsDot = computed(() => needsDotShown(events.routes.value))
const empty = computed(() => shown.value.length === 0)
/** The run-only variant names its run once, at the top: its groups need no header of their own. */
const runHeaders = computed(() => !props.runId)

function active(id: string) {
  return sessionOpen(route.path, id)
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
      <div v-if="runId" class="flex flex-col gap-0.5 rounded-md bg-elevated/60 px-2.5 py-2" data-sidebar-run>
        <span class="text-[11px] font-semibold uppercase tracking-wider text-muted">Crew</span>
        <NuxtLink :to="`/runs/${encodeURIComponent(runId)}`" class="flex items-center gap-1.5 text-sm font-semibold text-highlighted hover:underline" :aria-current="runOpen(route.path, runId) ? 'page' : undefined">
          <UIcon name="i-lucide-layout-grid" class="size-4 flex-none text-muted" />
          <span class="truncate">{{ runName || runId }}</span>
        </NuxtLink>
        <span v-if="!runOpen(route.path, runId)" class="text-xs text-muted">Only this crew's members are listed.</span>
      </div>
      <p v-if="empty && runId" class="px-2 py-4 text-xs text-muted leading-relaxed">No member of this crew has a session yet.</p>
      <p v-else-if="empty" class="px-2 py-4 text-xs text-muted leading-relaxed">No sessions yet. Launch an agent here or run <code>conductor host</code> from your machine.</p>

      <section v-if="count('needs')" class="flex flex-col gap-0.5">
        <h3 class="flex items-center gap-2 px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-warning">
          Needs you <span class="rounded-full bg-warning/20 px-1.5 text-warning tracking-normal">{{ count('needs') }}</span>
        </h3>
        <div v-for="g in groups.needs" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <NuxtLink
            v-for="s in g.sessions"
            :key="s.id"
            :to="`/sessions/${s.id}`"
            class="flex gap-2.5 rounded-md px-2.5 py-2 transition-colors"
            :class="[active(s.id) ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60', g.runId && runHeaders && 'ml-2']"
          >
            <SessionAvatar :agent-id="s.agentId" solid />
            <div class="min-w-0 flex-1 flex flex-col gap-0.5">
              <div class="flex items-center gap-1.5">
                <span class="truncate text-sm font-semibold">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
                <YoloBadge v-if="s.yolo" icon />
                <span v-if="needsDot" class="ml-auto size-2 rounded-full bg-warning flex-none" aria-hidden="true" />
              </div>
              <span class="truncate text-xs">{{ s.attention?.message || 'Waiting for input' }}</span>
              <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
            </div>
          </NuxtLink>
        </div>
      </section>

      <section v-if="count('running')" class="flex flex-col gap-0.5">
        <h3 class="px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted">Running · {{ count('running') }}</h3>
        <div v-for="g in groups.running" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <NuxtLink
            v-for="s in g.sessions"
            :key="s.id"
            :to="`/sessions/${s.id}`"
            class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 transition-colors"
            :class="[active(s.id) ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60', g.runId && runHeaders && 'ml-2']"
          >
            <SessionAvatar :agent-id="s.agentId" />
            <div class="min-w-0 flex-1 flex flex-col">
              <div class="flex items-center gap-1.5 min-w-0">
                <span class="truncate text-sm font-medium">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
                <YoloBadge v-if="s.yolo" icon />
              </div>
              <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
            </div>
            <span class="size-2 rounded-full flex-none" :class="s.status === 'running' ? 'bg-success' : 'bg-neutral-400'" aria-hidden="true" />
          </NuxtLink>
        </div>
      </section>

      <section v-if="count('exited')" class="flex flex-col gap-0.5">
        <h3 class="px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted">Exited · {{ count('exited') }}</h3>
        <div v-for="g in groups.exited" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <!-- The resume button sits beside the row's link, not in it: a link holds no button. -->
          <div v-for="s in g.sessions" :key="s.id" class="flex items-center gap-1" :class="g.runId && runHeaders && 'ml-2'">
            <NuxtLink
              :to="`/sessions/${s.id}`"
              class="flex min-w-0 flex-1 items-center gap-2.5 rounded-md px-2.5 py-1.5 opacity-75 transition-colors"
              :class="active(s.id) ? 'bg-default border border-default shadow-xs opacity-100' : 'hover:bg-elevated/60'"
            >
              <SessionAvatar :agent-id="s.agentId" dashed />
              <div class="min-w-0 flex-1 flex flex-col">
                <div class="flex items-center gap-1.5 min-w-0">
                  <span class="truncate text-sm font-medium">{{ s.name }}</span>
                  <EventMarkBadge :session-id="s.id" />
                  <YoloBadge v-if="s.yolo" icon />
                </div>
                <span class="truncate font-mono text-[11px] text-muted">{{ exitLabel(s) }}</span>
              </div>
            </NuxtLink>
            <ResumeButton v-if="s.kind === 'server'" :session="s" icon-only size="xs" />
          </div>
        </div>
      </section>
    </div>
  </div>
</template>
