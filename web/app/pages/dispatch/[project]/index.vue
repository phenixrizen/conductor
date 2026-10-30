<script setup lang="ts">
import type { DispatchTicket, ProjectRead, TicketSummary } from '~/composables/useDispatch'
import type { AgentInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { listWatch } from '~/composables/useDispatchLive'
import { DISPATCH_SHORTCUTS } from '~/composables/useShortcuts'
import { TRACKERS, ticketPath, validProjectKey } from '~/utils/dispatch'
import { apiQuery, filterFromURL, filterNarrows, filterToURL, type DispatchFilter } from '~/utils/dispatchQuery'

// A project's page (docs/plans/dispatch.md, PR-8): its tickets as a list, filtered and sorted on the server, the filter in the link
// (utils/dispatchQuery.ts), pages of 200; New ticket; the board and the roadmap as tabs, which come with the next change. The project and
// the list are read through the page's ordering gate, the list by its filter (a filter changed is a new list, the old one's late pages
// dropped), as the page opens and again after the window on any dispatch event of the project, and on a snapshot, which forgets what the
// gate knew (a store restored from a backup). A read that fails is asked again as a re-read is, the first and a filter's among them.
// Nothing polls.

const route = useRoute()
const router = useRouter()
const toast = useToast()
const admin = useWorkbenchToken()
const sessions = useSessions()
const { api, gate, read, readOrAbsent } = useDispatch()

/** The project's key, from the link: a key is upper case. */
const project = computed(() => String(route.params.project ?? '').toUpperCase())

const projectRead = ref<ProjectRead | null>(null)
const projectMissing = ref(false)
const tickets = ref<TicketSummary[]>([])
const total = ref(0)
/** The cursor of the page after those shown, '' when they are all. */
const next = ref('')
/** How many pages of the list are shown: a re-read reads them all again. */
const pages = ref(1)
/** The list (a thing of the gate: its project and its filter) whose tickets are shown. */
const shownThing = ref('')
const loading = ref(false)
const error = ref('')
const agents = ref<AgentInfo[]>([])
const now = ref(Date.now())
const newOpen = ref(false)

useHead({ title: computed(() => (projectRead.value ? `${projectRead.value.project.name} · Dispatch` : project.value)) })

// The view, in the link (?view=) and kept per browser as the run page keeps its own: the list now, the board and the roadmap with the
// next change.
const VIEW_KEY = 'conductor.dispatchView'
type DispatchViewKind = 'list' | 'board' | 'roadmap'
function isView(v: unknown): v is DispatchViewKind {
  return v === 'list' || v === 'board' || v === 'roadmap'
}
function readView(): DispatchViewKind {
  if (isView(route.query.view)) return route.query.view
  try {
    const v = localStorage.getItem(VIEW_KEY)
    return isView(v) ? v : 'list'
  } catch {
    return 'list'
  }
}
const view = ref<DispatchViewKind>(readView())
watch(
  () => route.query.view,
  (q) => {
    if (isView(q)) view.value = q
  },
)
watch(view, (v) => {
  try {
    localStorage.setItem(VIEW_KEY, v)
  } catch {
    /* storage refused: the choice lasts the page */
  }
  if (route.query.view !== v) void router.replace({ query: { ...route.query, view: v === 'list' ? undefined : v } })
})
const viewItems = [
  { label: 'List', value: 'list', icon: 'i-lucide-list' },
  { label: 'Board', value: 'board', icon: 'i-lucide-square-kanban' },
  { label: 'Roadmap', value: 'roadmap', icon: 'i-lucide-map' },
]

/** The filter, as the link says it; a change is written to the link, which the list is read by. */
const filter = computed<DispatchFilter>({
  get: () => filterFromURL(route.query),
  set: (f) => void router.replace({ query: { ...(isView(route.query.view) ? { view: route.query.view } : {}), ...filterToURL(f) } }),
})
const names = computed(() => ({ labels: projectRead.value?.labels ?? [], milestones: projectRead.value?.milestones ?? [] }))
/** The list's request for the filter shown, without its page: null when the filter can match nothing (a label the project lacks). */
const query = computed(() => apiQuery(project.value, filter.value, names.value))
/** The list as a thing of the gate: by its project and its filter. */
const listThing = computed(() => `list:${project.value}?${query.value ?? 'nothing'}`)
/** The tickets shown are the filter's. */
const listed = computed(() => shownThing.value === listThing.value)

/** A page's size: the most the store lists at once. */
const PAGE = 200

/** A 404, or a 400 for what is no project's key: no such project, which reading again does not change. */
function noSuch(e: unknown): boolean {
  return e instanceof ApiError && ((e.status === 404 && e.code === 'not_found') || (e.status === 400 && e.code === 'invalid_request'))
}

/** Reads the project; a key no project has says so, through the gate as any reply, so a slow 404 never hides a project read since. */
async function readProject(): Promise<void> {
  const key = project.value
  if (!validProjectKey(key)) {
    projectMissing.value = true
    return
  }
  await readOrAbsent(`project:${key}`, () => api.project(key), {
    apply: (p) => {
      if (key !== project.value) return
      projectRead.value = p
      projectMissing.value = false
    },
    rev: (p) => p.project.rev,
    absent: noSuch,
    gone: () => {
      if (key === project.value) projectMissing.value = true
    },
  })
}

/** Reads the pages of the list shown, as one reply, for the filter shown. Nothing until the project is read: its names resolve the filter. */
async function readList(): Promise<void> {
  if (!projectRead.value || projectMissing.value) return
  const thing = listThing.value
  const q = query.value
  if (q === null) {
    // The filter names labels or a milestone the project lacks: nothing matches, and nothing is asked.
    tickets.value = []
    total.value = 0
    next.value = ''
    shownThing.value = thing
    return
  }
  const want = pages.value
  loading.value = true
  try {
    await read(
      thing,
      async () => {
        const all: TicketSummary[] = []
        let cursor = ''
        let count = 0
        for (let i = 0; i < want; i++) {
          const paging = new URLSearchParams({ limit: String(PAGE), ...(cursor ? { cursor } : {}) }).toString()
          const page = await api.tickets(q ? `${q}&${paging}` : paging)
          all.push(...(page.tickets ?? []))
          count = page.total
          cursor = page.next ?? ''
          if (!cursor) break
        }
        return { all, count, cursor }
      },
      (r) => {
        tickets.value = r.all
        total.value = r.count
        next.value = r.cursor
        shownThing.value = thing
      },
    )
  } finally {
    loading.value = false
  }
}

/** The project and its list read again; rejects when a read failed (the live store asks again). */
async function reload(): Promise<void> {
  try {
    await readProject()
    await readList()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
    throw e
  }
}

/**
 * The project and its list read through the live store (DispatchLive.ask), so that a read that fails is asked again after the window,
 * doubled, as a re-read is, until one answers, with no event: as the page opens too, when the stream is connected already and no
 * snapshot comes. None without the workbench token, which is asked for; the page is read once it is given.
 */
function load(): void {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  live.ask(reload)
}

let tick: number | undefined
onMounted(() => {
  sessions
    .catalog()
    .then((a) => (agents.value = a))
    .catch(() => {})
  tick = window.setInterval(() => (now.value = Date.now()), 30_000)
})
onBeforeUnmount(() => window.clearInterval(tick))
watch(() => admin.token.value, load)

// The live events read the page again; its first read is made once it is subscribed to them (useDispatchLive's `first`).
const live = useDispatchLive({ ...listWatch(() => project.value, reload), reset: () => gate.reset() }, load)

// Another filter is another list: the old one's late pages are dropped, and the new one is read from its first page, through the live
// store (DispatchLive.ask), so that a read that fails is asked again after the window, doubled, as a re-read is, with no further event.
watch(listThing, (_, before) => {
  gate.drop(before)
  pages.value = 1
  live.ask(async () => {
    try {
      await readList()
    } catch (e) {
      error.value = (e as Error).message
      throw e
    }
  })
})
// Another project on the same page: what was shown goes.
watch(project, (_, before) => {
  gate.drop(`project:${before}`)
  projectRead.value = null
  projectMissing.value = false
  tickets.value = []
  shownThing.value = ''
  load()
})

function more() {
  pages.value++
  void readList().catch((e: Error) => (error.value = e.message))
}

/** A ticket made: said with its key and Open, and the list read at once (its event reads it again too). */
function created(t: DispatchTicket) {
  toast.add({ title: `${t.key} made`, description: t.title, icon: 'i-lucide-ticket', color: 'success', actions: [{ label: 'Open', icon: 'i-lucide-arrow-right', onClick: () => void router.push(ticketPath(t.key)) }] })
  void readList().catch(() => {})
}

const archived = computed(() => !!projectRead.value?.project.archivedAt)
const labelsById = computed(() => new Map((projectRead.value?.labels ?? []).map((l) => [l.id, l])))
const milestonesById = computed(() => new Map((projectRead.value?.milestones ?? []).map((m) => [m.id, m])))
const narrows = computed(() => filterNarrows(filter.value))

// The list's keys (Owner decisions, 10): J and K move the focus, Escape leaves the list; Enter opens a focused row, as a link does.
useShortcutsModal().registerPage(DISPATCH_SHORTCUTS)
const list = useTemplateRef<{ step: (delta: 1 | -1) => void; leave: () => void }>('list')
defineShortcuts({
  j: () => list.value?.step(1),
  k: () => list.value?.step(-1),
  escape: () => list.value?.leave(),
})
</script>

<template>
  <UDashboardPanel id="dispatch-project" :ui="{ body: 'p-0 sm:p-0 gap-0' }">
    <template #header>
      <UDashboardNavbar :toggle="false" :ui="{ left: 'min-w-0 flex-1' }" data-project-header>
        <template #leading>
          <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" aria-label="Back to the projects" to="/dispatch" class="lg:hidden" />
        </template>
        <template #title>
          <span class="flex min-w-0 items-baseline gap-2">
            <NuxtLink to="/dispatch" class="hidden text-muted hover:underline sm:inline">Dispatch</NuxtLink>
            <span class="hidden text-dimmed sm:inline">/</span>
            <span class="flex-none font-mono" data-project-key>{{ project }}</span>
            <span v-if="projectRead" class="min-w-0 truncate text-[13px] font-normal text-muted" data-project-name>{{ projectRead.project.name }}</span>
          </span>
        </template>
        <template #right>
          <UButton icon="i-lucide-plus" aria-label="New ticket" :disabled="!projectRead || archived" data-new-ticket-open @click="newOpen = true"><span class="hidden sm:inline">New ticket</span></UButton>
          <FullscreenButton class="hidden sm:inline-flex" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div v-if="error" class="flex-none px-4 pt-3 sm:px-6">
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
      </div>

      <div v-if="projectMissing" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-muted" data-project-missing>
        <UIcon name="i-lucide-search-x" class="size-8" />
        <p class="text-sm">No project has the key <code>{{ project }}</code>.</p>
        <UButton label="Dispatch" icon="i-lucide-arrow-left" color="neutral" variant="soft" to="/dispatch" />
      </div>

      <template v-else>
        <div class="flex flex-none flex-col gap-3 px-4 pt-3 pb-2 sm:px-6">
          <UAlert v-if="archived" color="neutral" variant="subtle" icon="i-lucide-archive" :title="`${project} is archived.`" description="Its tickets are read as they are; nothing of them can be changed until it is unarchived." />
          <div class="flex flex-wrap items-center gap-3">
            <UTabs v-model="view" :items="viewItems" :content="false" size="xs" color="neutral" data-project-view />
            <a
              v-if="projectRead?.project.tracker === 'external' && projectRead.project.trackerUrl"
              :href="projectRead.project.trackerUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="flex min-w-0 items-center gap-1 text-xs text-muted hover:underline"
              :title="`Its tickets are tracked at ${projectRead.project.trackerUrl}`"
              data-project-tracker
            >
              <UIcon :name="TRACKERS.external.icon" class="size-3.5 flex-none" />
              <span class="truncate">{{ projectRead.project.trackerUrl }}</span>
            </a>
            <span v-if="listed && view === 'list'" class="ml-auto text-xs text-muted tabular-nums" data-list-total :data-count="total">{{ total }} {{ total === 1 ? 'ticket' : 'tickets' }}{{ narrows ? ' match' : '' }}</span>
          </div>
          <DispatchFilters v-if="view === 'list'" v-model="filter" :labels="projectRead?.labels ?? []" :milestones="projectRead?.milestones ?? []" :agents="agents" />
        </div>

        <div v-if="view !== 'list'" class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center text-muted" data-view-later>
          <UIcon :name="view === 'board' ? 'i-lucide-square-kanban' : 'i-lucide-map'" class="size-8" />
          <p class="text-sm">The {{ view }} comes with the next change. The list has every ticket meanwhile.</p>
          <UButton label="The list" icon="i-lucide-list" color="neutral" variant="soft" @click="view = 'list'" />
        </div>

        <template v-else>
          <div v-if="!listed" class="flex flex-1 items-center justify-center p-8 text-sm text-muted">Reading the tickets…</div>
          <div v-else-if="!tickets.length" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center text-muted" data-list-empty>
            <UIcon name="i-lucide-ticket" class="size-8" />
            <p class="text-sm">{{ narrows ? 'No ticket matches the filter.' : `${project} has no tickets yet.` }}</p>
            <UButton v-if="!narrows && !archived" label="New ticket" icon="i-lucide-plus" @click="newOpen = true" />
          </div>
          <div v-else class="min-h-0 flex-1 overflow-y-auto border-t border-default">
            <DispatchTicketList ref="list" :tickets="tickets" :labels="labelsById" :milestones="milestonesById" :agents="agents" :now="now" />
            <div v-if="next" class="flex justify-center p-3">
              <UButton :label="`More (${tickets.length} of ${total})`" icon="i-lucide-chevrons-down" color="neutral" variant="soft" size="sm" :loading="loading" data-list-more @click="more" />
            </div>
          </div>
        </template>
      </template>
    </template>
  </UDashboardPanel>

  <DispatchNewTicket v-model:open="newOpen" :project="project" :gate="gate" @created="created" />
</template>
