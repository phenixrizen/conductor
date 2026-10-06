<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { newlyNeedingInput } from '~/utils/attention'
import { sidebarRunFor } from '~/utils/crews'
import { filterSessions } from '~/utils/sessions'
import { needsDotShown, reopenNeeds, sectionPreview, sidebarModel, type ListSection, type RunBlock, type SessionRow, type SidebarItem } from '~/utils/sidebar'

/**
 * The full sidebar (design 3a, 3b): sessions and runs, ordered by what needs you. Two sections, Needs you then Running, each holding
 * loose sessions and run blocks side by side; a run is one block, placed where its most urgent member is. Then the links shared with
 * you, below your own sessions, and Exited, folded to one line. Every section folds from its header (useSidebarFolds). The run of
 * the page open now is marked and scrolled to; nothing is filtered out for it.
 */
const attention = useAttention()
const events = useEvents()
const route = useRoute()
const launch = useLaunchModal()
const joined = useJoined()
const foldState = useSidebarFolds()

const query = ref('')
const filterInput = useTemplateRef<{ inputRef?: HTMLInputElement }>('filterInput')
const now = ref(Date.now())
let tick: number | undefined

const model = computed(() => sidebarModel(filterSessions(attention.sessions.value, query.value), attention.runOf))
const needsDot = computed(() => needsDotShown(events.routes.value))
const empty = computed(() => attention.sessions.value.length === 0 && joined.list.value.length === 0)
const folds = foldState.folds
/** The run of the page open now: the run page, or a member's page. */
const openRun = computed(() => sidebarRunFor(route.path, attention.sessions.value))

function preview(key: ListSection) {
  return sectionPreview(model.value[key])
}
function isRun(it: SidebarItem) {
  return it.kind === 'run'
}

// Needs you opens again by itself when a new prompt arrives, so folding it never hides one. The first list a page sees is
// what was already there (the store filling after a load), not news: it primes the comparison and reopens nothing.
let seen = new Map<string, SessionInfo>(attention.sessions.value.map((s) => [s.id, s]))
let primed = seen.size > 0
watch(attention.sessions, (list) => {
  const next = new Map(list.map((s) => [s.id, s]))
  const fresh = primed ? newlyNeedingInput(seen, next) : []
  seen = next
  primed ||= next.size > 0
  const after = reopenNeeds(folds.value, fresh)
  if (after !== folds.value) foldState.set(after)
})

// The open run's section unfolds and its block comes into view, once per page.
function sectionOfRun(runId: string): ListSection | undefined {
  for (const key of ['needs', 'running', 'exited'] as const) if (model.value[key].some((it) => it.kind === 'run' && it.runId === runId)) return key
  return undefined
}
watch(
  openRun,
  async (runId) => {
    if (!runId) return
    const key = sectionOfRun(runId)
    if (key) foldState.unfold(key)
    await nextTick()
    document.querySelector('[data-sidebar-run-open]')?.scrollIntoView({ block: 'nearest' })
  },
  { immediate: true },
)

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
      <UInput ref="filterInput" v-model="query" placeholder="Filter sessions, runs, people" size="sm" icon="i-lucide-slash" :ui="{ base: 'font-normal' }" />
    </div>

    <div class="flex-1 min-h-0 overflow-y-auto px-1 flex flex-col gap-3" data-session-list="sidebar">
      <p v-if="empty" class="px-2 py-4 text-xs text-muted leading-relaxed">No sessions yet. Launch an agent here or run <code>conductor host</code> from your machine.</p>

      <SidebarSection v-if="model.needs.length" id="needs" title="Needs you" :count="model.counts.needs" tone="warning" :preview="preview('needs')" :folded="folds.needs" @update:folded="foldState.fold('needs', $event)">
        <ol class="flex flex-col gap-0.5">
          <template v-for="it in model.needs" :key="it.id">
            <SidebarRunBlock v-if="isRun(it)" :block="it as RunBlock" :now="now" :needs-dot="needsDot" :open="(it as RunBlock).runId === openRun" />
            <SidebarSessionRow v-else :row="it as SessionRow" :now="now" :needs-dot="needsDot" />
          </template>
        </ol>
      </SidebarSection>

      <SidebarSection v-if="model.running.length" id="running" title="Running" :count="model.counts.running" :preview="preview('running')" :folded="folds.running" @update:folded="foldState.fold('running', $event)">
        <ol class="flex flex-col gap-0.5">
          <template v-for="it in model.running" :key="it.id">
            <SidebarRunBlock v-if="isRun(it)" :block="it as RunBlock" :now="now" :needs-dot="needsDot" :open="(it as RunBlock).runId === openRun" />
            <SidebarSessionRow v-else :row="it as SessionRow" :now="now" :needs-dot="needsDot" />
          </template>
        </ol>
      </SidebarSection>

      <!-- Yours first, then the rest: the links joined from here sit below your own sessions. -->
      <SidebarSection v-if="joined.list.value.length" id="shared" title="Shared with you" :count="joined.list.value.length" :folded="folds.shared" @update:folded="foldState.fold('shared', $event)">
        <SidebarSharedList />
      </SidebarSection>

      <SidebarSection v-if="model.exited.length" id="exited" title="Exited" :count="model.counts.exited" :preview="preview('exited')" :folded="folds.exited" @update:folded="foldState.fold('exited', $event)">
        <ol class="flex flex-col gap-0.5">
          <template v-for="it in model.exited" :key="it.id">
            <SidebarRunBlock v-if="isRun(it)" :block="it as RunBlock" :now="now" :needs-dot="needsDot" :open="(it as RunBlock).runId === openRun" />
            <SidebarSessionRow v-else :row="it as SessionRow" :now="now" :needs-dot="needsDot" />
          </template>
        </ol>
      </SidebarSection>
    </div>
  </div>
</template>
