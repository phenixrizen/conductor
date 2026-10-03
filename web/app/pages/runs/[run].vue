<script setup lang="ts">
import type { RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import { broadcastSelection, crewFeed, memberStatus, runCounts, takeViewLink } from '~/utils/crews'
import { bestGrid, lastItemSpan } from '~/utils/wall'

// The crew view: a tile for every member of one run, its activity and a
// broadcast bar. Sessions and the run come from the live store (useAttention):
// the run, with its member states, branches and diff stats, is read as the page
// opens and again whenever a run event or a member session's change says it
// changed. Nothing polls.

const route = useRoute()
const router = useRouter()
const api = useSessions()
const admin = useAdminToken()
const live = useAttention()
const events = useEvents()
const toast = useToast()
const { create } = useTerminalTransport()

const runId = computed(() => String(route.params.run))
/** The run as the live store last read it. */
const run = computed<RunInfo | null>(() => live.runOf(runId.value) ?? null)
const error = ref('')
const gone = ref(false)
const now = ref(Date.now())
/** The view link of the launch that opened this page, shown once: it lives in this page alone and goes with it, or when dismissed. */
const launchLink = ref(takeViewLink(String(route.params.run)))
const copy = useCopy()

useHead({ title: computed(() => run.value?.name || 'Crew') })

/** Reads the run as the page opens: its diff stats are read with it. Later reads are the live store's. */
async function load() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  const id = runId.value
  try {
    const r = await live.refreshRun(id)
    if (id !== runId.value) return
    error.value = ''
    gone.value = !r
  } catch (e) {
    if (id === runId.value) error.value = (e as Error).message
  }
}

/** An action (add, start, stop) answered with the run as it is now. */
function changed(r: RunInfo) {
  live.applyRun(r)
}

/** The run's sessions in the live store. */
const sessions = computed(() => live.sessions.value.filter((s) => s.crew?.runId === runId.value))

interface Tile {
  name: string
  member?: RunMember
  session?: SessionInfo
}

/** One tile per member in the run's order, then any session of the run the last read did not know yet. */
const tiles = computed<Tile[]>(() => {
  const out: Tile[] = []
  const seen = new Set<string>()
  for (const m of run.value?.members ?? []) {
    const s = sessions.value.find((x) => (m.sessionId ? x.id === m.sessionId : x.crew?.member === m.name))
    if (s) seen.add(s.id)
    out.push({ name: m.name, member: m, session: s })
  }
  for (const s of sessions.value) if (!seen.has(s.id)) out.push({ name: s.crew?.member ?? s.name, session: s })
  return out
})

const counts = computed(() => (run.value ? runCounts(run.value, live.sessions.value) : { needs: 0, running: 0 }))

// Broadcast selection: every member whose session runs, unless the person
// unticked it; a member that starts later is selected as it appears. Only the
// person's own ticks are kept (choices), so no read of the run and no run
// event clears one; another run starts afresh.
const choices = ref<Record<string, boolean>>({})
const selection = computed(() =>
  broadcastSelection(
    tiles.value.map((t) => ({
      name: t.name,
      live: !!t.session && (t.session.status === 'running' || t.session.status === 'starting'),
      waiting: t.session?.attention?.state === 'needs_input',
    })),
    choices.value,
  ),
)
function isSelected(name: string) {
  return selection.value.selected.includes(name)
}
function toggle(name: string, on: boolean | 'indeterminate') {
  choices.value = { ...choices.value, [name]: on === true }
}

watch(runId, () => {
  choices.value = {}
  gone.value = false
  load()
})

// Activity: the members' events in the live feed and the run's own log.
const memberOf = computed(() => {
  const m = new Map<string, string>()
  for (const s of sessions.value) m.set(s.id, s.crew?.member ?? s.name)
  for (const x of run.value?.members ?? []) if (x.sessionId) m.set(x.sessionId, x.name)
  return m
})
const feed = computed(() => crewFeed(events.entries.value, run.value?.log ?? [], memberOf.value))

function transportFor(s: SessionInfo) {
  return () => create({ sessionId: s.id, token: admin.token.value, kind: s.kind })
}

function footer(t: Tile): { where: string; diff: string } {
  const m = t.member
  const where = m?.branch || t.session?.branch || (run.value?.isolation === 'worktree' ? '' : 'shared cwd')
  const diff = m?.diff ? `+${m.diff.added} −${m.diff.removed}` : ''
  return { where, diff }
}

function pendingLabel(t: Tile): string {
  const m = t.member
  if (!m) return ''
  const st = run.value ? memberStatus(run.value, m, live.sessions.value) : m.status
  if (st === 'ended') return m.error ? `ended: ${m.error}` : 'ended'
  // Started, and its session not in the live store yet.
  if (st !== 'pending') return 'starting…'
  if (m.start.when === 'after') return `starts once ${m.start.member} is idle`
  if (m.start.when === 'manual') return 'starts by hand'
  return 'waiting to start'
}

const starting = ref('')
async function startMember(name: string) {
  starting.value = name
  try {
    changed(await api.startRunMember(runId.value, name))
  } catch (e) {
    toast.add({ title: `${name} did not start`, description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    starting.value = ''
  }
}

// Grid: every tile and the activity card fit on screen, as on the wall; on
// a narrow screen they stack and the page scrolls.
const grid = useTemplateRef<HTMLDivElement>('grid')
const box = ref({ w: 0, h: 0 })
watch(
  grid,
  (el, _prev, onCleanup) => {
    if (!el) return
    const observer = new ResizeObserver((entries) => {
      const r = entries[0]?.contentRect
      if (r) box.value = { w: r.width, h: r.height }
    })
    observer.observe(el)
    onCleanup(() => observer.disconnect())
  },
  { immediate: true },
)
const narrow = computed(() => box.value.w > 0 && box.value.w < 640)
const layout = computed(() => bestGrid(tiles.value.length + 1, box.value.w, box.value.h, 12, 1.4))
const feedSpan = computed(() => lastItemSpan(tiles.value.length + 1, layout.value))
const gridStyle = computed(() =>
  narrow.value
    ? { gridTemplateColumns: 'minmax(0, 1fr)', gridAutoRows: '16rem' }
    : { gridTemplateColumns: `repeat(${layout.value.cols}, minmax(0, 1fr))`, gridTemplateRows: `repeat(${layout.value.rows}, minmax(0, 1fr))` },
)

let tick: number | undefined
onMounted(() => {
  live.start()
  load()
  // The clock of the header's "up 5 min": no read of the server.
  tick = window.setInterval(() => (now.value = Date.now()), 30000)
})
onBeforeUnmount(() => {
  window.clearInterval(tick)
})
watch(() => admin.token.value, load)
</script>

<template>
  <UDashboardPanel id="run" :ui="{ body: 'p-0 sm:p-0 flex flex-col min-h-0 gap-0 overflow-hidden' }">
    <template #header>
      <CrewRunHeader :run="run" :run-id="runId" :counts="counts" :now="now" @changed="changed" />
    </template>

    <template #body>
      <!-- Padding around, not margins on, the full-width alerts: margins would overflow the body. -->
      <div v-if="error" class="flex-none px-3 pt-3">
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
      </div>
      <div v-if="launchLink" class="flex-none px-3 pt-3">
        <UAlert
          color="neutral"
          variant="subtle"
          icon="i-lucide-link"
          :title="`View link, valid ${Math.round((launchLink!.ttlSeconds / 3600) * 10) / 10} h. Shown once: copy it now.`"
          :actions="[{ label: 'Copy link', icon: 'i-lucide-clipboard', onClick: () => copy(launchLink!.url, 'Link copied', 'Anyone with it can watch every member.') }]"
          close
          :ui="{ description: 'font-mono text-xs break-all select-all' }"
          :description="launchLink!.url"
          data-launch-link
          @update:open="launchLink = null"
        />
      </div>

      <div v-if="gone" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-muted">
        <UIcon name="i-lucide-search-x" class="size-8" />
        <p class="text-sm">No run has the id <code>{{ runId }}</code>. The server forgets runs when it restarts.</p>
        <UButton label="Crews" icon="i-lucide-arrow-left" color="neutral" variant="soft" to="/crews" />
      </div>

      <template v-else>
        <div ref="grid" class="min-h-0 flex-1 p-3" :class="narrow ? 'overflow-y-auto' : 'overflow-hidden'">
          <div class="grid h-full w-full gap-3" :class="narrow && 'h-auto'" :style="gridStyle" data-run-grid>
            <template v-for="t in tiles" :key="t.session?.id ?? `m-${t.name}`">
              <SessionTile v-if="t.session" :session="t.session" :create-transport="transportFor(t.session)" :data-member="t.name" @select="router.push(`/sessions/${t.session.id}`)">
                <template #leading>
                  <UCheckbox :model-value="isSelected(t.name)" :aria-label="`Select ${t.name} for the broadcast`" data-broadcast-pick @update:model-value="toggle(t.name, $event)" />
                </template>
                <template #footer>
                  <span class="truncate" data-branch>{{ footer(t).where }}</span>
                  <span class="ml-auto flex-none" data-diff>{{ footer(t).diff }}</span>
                </template>
              </SessionTile>
              <div v-else class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-dashed border-accented" :data-member="t.name">
                <div class="flex items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
                  <SessionAvatar :agent-id="t.member?.agentId ?? ''" dashed />
                  <span class="flex-1 truncate text-[13px] font-semibold">{{ t.name }}</span>
                </div>
                <div class="flex flex-1 flex-col items-center justify-center gap-2 p-3 text-center text-sm text-muted">
                  <span>{{ pendingLabel(t) }}</span>
                  <UButton
                    v-if="t.member?.status === 'pending' && !run?.stoppedAt"
                    label="Start now"
                    icon="i-lucide-play"
                    size="xs"
                    color="neutral"
                    variant="outline"
                    :loading="starting === t.name"
                    @click="startMember(t.name)"
                  />
                  <ResumeButton v-else-if="t.member?.status === 'ended'" :run-id="runId" :member="t.name" stay size="xs" />
                </div>
              </div>
            </template>
            <CrewFeed :items="feed" :style="narrow ? undefined : { gridColumn: `span ${feedSpan} / span ${feedSpan}` }" />
          </div>
        </div>
        <div class="flex-none px-3 pb-3">
          <BroadcastBar :run-id="runId" :members="selection.selected" :will-type="selection.sending.length" :waiting="selection.waiting.length" :disabled="!run || !!run.stoppedAt" />
        </div>
      </template>
    </template>
  </UDashboardPanel>
</template>
