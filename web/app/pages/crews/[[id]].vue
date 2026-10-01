<script setup lang="ts">
import type { AgentInfo, CrewInfo, CrewSummary, RunInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { agentIcon } from '~/utils/agentIcons'
import { crewKey, defaultCrew, holdViewLink, pageAfterDelete, runActive, summaryOf, toCrewInput, toDraft, type DraftCrew } from '~/utils/crews'
import { relativeTime, shortCwd } from '~/utils/sessions'

// The crews list and the editor of the selected crew. /crews/<id> selects a
// crew; /crews shows a new draft, or moves to the first crew. One page for
// both, under one key, so moving between them never remounts it. Drafts live
// in app state: an edit survives moving to another page until it is saved or
// discarded.

definePageMeta({ key: 'crews' })
useHead({ title: 'Crews' })

const route = useRoute()
const router = useRouter()
const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const live = useAttention()

/** The key of a crew never saved in `drafts`. */
const NEW = ''

/** Crews the list shows at a time. */
const PAGE_SIZE = 100

/** The page of the list on screen, as GET /api/crews lists it. */
const crews = useState<CrewSummary[]>('crews', () => [])
const total = useState<number>('crewsTotal', () => 0)
const page = useState<number>('crewsPage', () => 1)
/** Crews read in full, by id: the one in the editor, and any whose draft is open. */
const full = useState<Record<string, CrewInfo>>('crewsFull', () => ({}))
/** Why the selected crew's file cannot be used, when the server says so. */
const unreadable = ref('')
/** The id of the crew being read in full: the editor shows a loading state meanwhile, never "No crew has the id". */
const crewLoading = ref<string>()
const agents = useState<AgentInfo[]>('crewAgents', () => [])
const drafts = useState<Record<string, DraftCrew>>('crewDrafts', () => ({}))
const runs = ref<RunInfo[]>([])
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
const saving = ref(false)
const launching = ref(false)
const now = ref(Date.now())

const routeId = computed(() => (typeof route.params.id === 'string' && route.params.id ? route.params.id : undefined))
const selectedKey = computed<string | undefined>(() => routeId.value ?? (drafts.value[NEW] ? NEW : undefined))
const saved = computed(() => (selectedKey.value ? full.value[selectedKey.value] : undefined))
const draft = computed<DraftCrew | undefined>({
  get: () => (selectedKey.value === undefined ? undefined : drafts.value[selectedKey.value]),
  set: (d) => {
    if (d && selectedKey.value !== undefined) drafts.value = { ...drafts.value, [selectedKey.value]: d }
  },
})

async function refresh() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  loading.value = true
  try {
    const [list, a, r] = await Promise.all([api.listCrews((page.value - 1) * PAGE_SIZE, PAGE_SIZE), api.catalog(), api.listRuns()])
    // A page past the end (crews deleted elsewhere) moves back to the last one, and its watcher reads it.
    const last = Math.max(1, Math.ceil(list.total / PAGE_SIZE))
    if (page.value > last) {
      page.value = last
      return
    }
    crews.value = list.crews
    total.value = list.total
    agents.value = a
    runs.value = r
    error.value = ''
    loaded.value = true
    await loadSelected()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

/** Reads the crew on screen in full. A draft nobody edited follows it as saved now: another admin, or conductor crews, may have changed it. */
async function loadSelected() {
  const id = routeId.value
  unreadable.value = ''
  if (!id) return
  crewLoading.value = id
  try {
    const fresh = await api.getCrew(id)
    const before = full.value[id]
    const d = drafts.value[id]
    if (d && before && crewKey(d) === crewKey(before)) drafts.value = { ...drafts.value, [id]: toDraft(fresh) }
    full.value = { ...full.value, [id]: fresh }
  } catch (e) {
    const next = { ...full.value }
    delete next[id]
    full.value = next
    // An answer for a crew no longer on screen says nothing about the one that is.
    if (routeId.value !== id) return
    if (e instanceof ApiError && e.code === 'crew_unreadable') unreadable.value = e.message
    else if (!(e instanceof ApiError && e.status === 404)) error.value = (e as Error).message
  } finally {
    if (crewLoading.value === id) crewLoading.value = undefined
  }
}
watch(page, refresh)

async function refreshRuns() {
  try {
    runs.value = await api.listRuns()
  } catch {
    /* the badges keep what they showed */
  }
}

// A draft for the crew on screen, seeded from the crew read in full once it is read; /crews moves to the first crew of the page.
watch(
  [selectedKey, crews, full, loaded],
  () => {
    if (!loaded.value) return
    if (selectedKey.value === undefined) {
      const first = crews.value[0]
      if (first) router.replace(`/crews/${encodeURIComponent(first.id)}`)
      return
    }
    if (selectedKey.value !== NEW && !drafts.value[selectedKey.value] && saved.value) {
      drafts.value = { ...drafts.value, [selectedKey.value]: toDraft(saved.value) }
    }
  },
  { immediate: true },
)

function isDirty(key: string): boolean {
  const d = drafts.value[key]
  if (!d) return false
  if (key === NEW) return true
  // A draft is seeded from `full`, so the crew is there; without it (a new token cleared it) the draft counts as changed.
  const c = full.value[key]
  return !c || crewKey(d) !== crewKey(c)
}

/** Runs of the crew in the server's memory, newest first. */
function runsOf(id: string): RunInfo[] {
  return runs.value.filter((r) => r.crewId === id)
}

function status(key: string): { label: string; color: 'warning' | 'success' | 'neutral' } {
  if (isDirty(key)) return { label: 'Draft changes', color: 'warning' }
  if (runsOf(key).some(runActive)) return { label: 'Running', color: 'success' }
  return { label: 'Ready', color: 'neutral' }
}

/** The list's meta in two parts: the working directory, which may be cut short, and the rest, which may not. */
function meta(c: DraftCrew | CrewSummary, id?: string): { cwd: string; rest: string } {
  const last = id ? runsOf(id)[0] : undefined
  return {
    cwd: shortCwd(c.cwd) || 'server default',
    rest: `${c.isolation === 'worktree' ? 'worktrees' : 'shared cwd'} · last run ${last ? relativeTime(last.startedAt, now.value) : 'never'}`,
  }
}

function agentOf(id: string) {
  return agents.value.find((a) => a.id === id)
}

/** The list: a new draft first, then the page's crews, each as its draft shows it when one is open. */
const list = computed(() => {
  const out: Array<{ key: string; crew: DraftCrew | CrewSummary; to: string }> = []
  const fresh = drafts.value[NEW]
  if (fresh) out.push({ key: NEW, crew: fresh, to: '/crews' })
  for (const c of crews.value) out.push({ key: c.id, crew: drafts.value[c.id] ?? c, to: `/crews/${encodeURIComponent(c.id)}` })
  return out
})

async function newCrew() {
  if (!agents.value.length) await refresh()
  if (!drafts.value[NEW]) drafts.value = { ...drafts.value, [NEW]: toDraft(defaultCrew(agents.value)) }
  router.push('/crews')
}

function fail(title: string, e: unknown) {
  toast.add({ title, description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
}

/** Saves the draft on screen and returns the crew as saved; undefined when the server refused it (a toast says why). */
async function save(): Promise<CrewInfo | undefined> {
  const key = selectedKey.value
  const d = draft.value
  if (key === undefined || !d) return undefined
  saving.value = true
  try {
    const c = await api.saveCrew(toCrewInput(d), d.id)
    full.value = { ...full.value, [c.id]: c }
    if (crews.value.some((x) => x.id === c.id)) crews.value = crews.value.map((x) => (x.id === c.id ? summaryOf(c) : x))
    else crews.value = [...crews.value, summaryOf(c)].sort((a, b) => a.name.localeCompare(b.name))
    // Only a new crew adds to the count: one opened by its link from another page was counted already.
    if (key === NEW) total.value += 1
    // A new crew moves to its page before its draft goes, so /crews never shows without it in between.
    if (key === NEW) await router.replace(`/crews/${encodeURIComponent(c.id)}`)
    const next = { ...drafts.value, [c.id]: toDraft(c) }
    if (key === NEW) delete next[NEW]
    drafts.value = next
    return c
  } catch (e) {
    fail(e instanceof ApiError && e.code === 'invalid_crew' ? 'The server refused the crew' : 'Save failed', e)
    return undefined
  } finally {
    saving.value = false
  }
}

async function onSave() {
  if (await save()) toast.add({ title: 'Crew saved', icon: 'i-lucide-check', color: 'success' })
}

function discard() {
  const key = selectedKey.value
  if (key === undefined) return
  const next = { ...drafts.value }
  delete next[key]
  if (key !== NEW && saved.value) next[key] = toDraft(saved.value)
  drafts.value = next
  if (key === NEW) router.replace('/crews')
}

async function duplicate() {
  const c = saved.value
  if (!c) return
  try {
    const copy = await api.duplicateCrew(c.id)
    full.value = { ...full.value, [copy.id]: copy }
    crews.value = [...crews.value, summaryOf(copy)].sort((a, b) => a.name.localeCompare(b.name))
    total.value += 1
    toast.add({ title: 'Crew duplicated', description: isDirty(c.id) ? `${copy.name}, from what ${c.name} has saved` : copy.name, icon: 'i-lucide-copy', color: 'success' })
    router.push(`/crews/${encodeURIComponent(copy.id)}`)
  } catch (e) {
    fail('Duplicate failed', e)
  }
}

const deleteOpen = ref(false)
const deleting = ref(false)

async function confirmDelete() {
  // A crew whose file cannot be used has no `saved`: it is deleted by the id on screen.
  const id = saved.value?.id ?? routeId.value
  if (!id) return
  const name = saved.value?.name ?? id
  // Only a crew the server could read counts in `total`.
  const counted = !!saved.value
  deleting.value = true
  try {
    await api.deleteCrew(id)
    crews.value = crews.value.filter((x) => x.id !== id)
    if (counted) total.value = Math.max(0, total.value - 1)
    const nextFull = { ...full.value }
    delete nextFull[id]
    full.value = nextFull
    const next = { ...drafts.value }
    delete next[id]
    drafts.value = next
    unreadable.value = ''
    deleteOpen.value = false
    toast.add({ title: 'Crew deleted', description: name, icon: 'i-lucide-trash-2', color: 'neutral' })
    router.replace('/crews')
    // A page the delete emptied steps back, and its watcher reads it: the list never shows "No crews yet" while there are crews.
    page.value = pageAfterDelete(page.value, crews.value.length)
  } catch (e) {
    fail('Delete failed', e)
  } finally {
    deleting.value = false
  }
}

/**
 * The view link a launch returned, until this page has shown it: in app state, so a launch that resolves after the page was left keeps it
 * for the next visit, and a toast offers it meanwhile. Its token is in no other state.
 */
const shownLink = useState<{ url: string; runId: string; name: string; hours: number } | null>('crewShownLink', () => null)
let mounted = false
const shownOpen = computed({
  get: () => !!shownLink.value,
  set: (open: boolean) => {
    if (!open) shownLink.value = null
  },
})
const copy = useCopy()

function hours(ttlSeconds: number): number {
  return Math.round((ttlSeconds / 3600) * 10) / 10
}

/**
 * Saves what changed and launches the crew. The server makes the crew's view
 * link, if it asks for one, and returns it this once: the crew view shows it
 * when the launch opens that, else a dialog here does.
 */
async function launch() {
  const key = selectedKey.value
  if (key === undefined) return
  launching.value = true
  try {
    const c = isDirty(key) ? await save() : saved.value
    if (!c) return
    let launched: Awaited<ReturnType<typeof api.launchCrew>>
    try {
      launched = await api.launchCrew(c.id)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_a_repo') fail('Not a git repository', e)
      else fail('Launch failed', e)
      return
    }
    const { run, viewLink } = launched
    runs.value = [run, ...runs.value.filter((r) => r.id !== run.id)]
    const ttl = c.viewLinkTtlSeconds ?? 0
    if (c.openAfterLaunch) {
      if (viewLink) holdViewLink(run.id, viewLink.url, ttl)
      await router.push(`/runs/${encodeURIComponent(run.id)}`)
      return
    }
    if (viewLink) {
      shownLink.value = { url: viewLink.url, runId: run.id, name: c.name, hours: hours(ttl) }
      if (!mounted) {
        toast.add({
          title: `${c.name} launched`,
          description: 'Its view link is shown once, on the Crews page.',
          icon: 'i-lucide-link',
          color: 'success',
          actions: [{ label: 'Show the link', onClick: () => router.push(`/crews/${encodeURIComponent(c.id)}`) }],
        })
      }
      return
    }
    toast.add({
      title: `${c.name} launched`,
      icon: 'i-lucide-play',
      color: 'success',
      actions: [{ label: 'Open crew view', icon: 'i-lucide-layout-grid', onClick: () => router.push(`/runs/${encodeURIComponent(run.id)}`) }],
    })
  } finally {
    launching.value = false
  }
}

// "Running" follows the live store: when a crew member's session starts or
// ends, the runs are read again. Nothing polls.
const crewSessions = computed(() =>
  live.sessions.value
    .filter((s) => s.crew)
    .map((s) => `${s.id}:${s.status}`)
    .join(','),
)
let runsTimer: number | undefined
watch(crewSessions, () => {
  window.clearTimeout(runsTimer)
  runsTimer = window.setTimeout(refreshRuns, 300)
})

let tick: number | undefined
onMounted(() => {
  mounted = true
  live.start()
  refresh()
  tick = window.setInterval(() => (now.value = Date.now()), 30000)
})
onBeforeUnmount(() => {
  mounted = false
  window.clearInterval(tick)
  window.clearTimeout(runsTimer)
})
watch(
  () => admin.token.value,
  () => {
    // Another token may be another server: nothing typed or read under the
    // old one stays, and neither does a view link held from it.
    drafts.value = {}
    crews.value = []
    full.value = {}
    total.value = 0
    shownLink.value = null
    if (page.value !== 1) page.value = 1 // its watcher reads the list
    else refresh()
  },
)
watch(routeId, loadSelected)
</script>

<template>
  <UDashboardPanel id="crews" :ui="{ body: 'p-0 sm:p-0 gap-0' }">
    <template #header>
      <UDashboardNavbar title="Crews">
        <template #right>
          <UButton label="New crew" icon="i-lucide-plus" color="neutral" variant="outline" @click="newCrew" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div class="flex min-h-full flex-col md:flex-row">
        <nav class="flex flex-none flex-col gap-2 border-b border-default p-4 md:w-60 md:border-b-0 md:border-r" aria-label="Crews" data-crew-list>
          <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
          <NuxtLink
            v-for="item in list"
            :key="item.key || 'new'"
            :to="item.to"
            class="flex flex-col gap-1.5 rounded-md p-3 transition-colors"
            :class="item.key === selectedKey ? 'bg-default ring ring-default shadow-xs' : 'hover:bg-elevated/60'"
            :aria-current="item.key === selectedKey ? 'page' : undefined"
            data-crew-item
          >
            <span class="flex items-center gap-2">
              <span class="truncate text-sm font-semibold text-highlighted">{{ item.crew.name || 'Untitled crew' }}</span>
              <UBadge :label="status(item.key).label" :color="status(item.key).color" variant="subtle" size="sm" class="ml-auto flex-none" />
            </span>
            <span class="flex flex-wrap gap-1">
              <template v-for="(m, i) in item.crew.members" :key="i">
                <span v-if="agentOf(m.agentId)?.icon" class="grid size-6 place-items-center rounded-md bg-elevated text-primary" :title="`${m.name} · ${agentOf(m.agentId)?.name}`">
                  <UIcon :name="agentIcon(agentOf(m.agentId)!.icon)" class="size-3.5" />
                </span>
                <SessionAvatar v-else :agent-id="m.agentId" />
              </template>
            </span>
            <!-- The directory is cut short on its own line; what follows always shows whole. -->
            <span class="flex min-w-0 flex-col font-mono text-[11px] text-muted" data-crew-meta>
              <span class="truncate" :title="meta(item.crew, item.key || undefined).cwd">{{ meta(item.crew, item.key || undefined).cwd }}</span>
              <span>{{ meta(item.crew, item.key || undefined).rest }}</span>
            </span>
          </NuxtLink>
          <p v-if="loaded && !list.length" class="px-1 text-sm text-muted">No crews yet.</p>
          <UPagination v-if="total > PAGE_SIZE" v-model:page="page" :total="total" :items-per-page="PAGE_SIZE" size="xs" class="mt-2 self-center" />
        </nav>

        <section class="min-w-0 flex-1 p-4 sm:p-6">
          <CrewEditor
            v-if="draft"
            :key="selectedKey"
            v-model="draft"
            :agents="agents"
            :dirty="isDirty(selectedKey!)"
            :saving="saving"
            :launching="launching"
            @save="onSave"
            @discard="discard"
            @duplicate="duplicate"
            @launch="launch"
            @delete="deleteOpen = true"
          />
          <div v-else-if="unreadable" class="flex flex-col items-start gap-3" data-crew-unreadable>
            <UAlert color="error" variant="subtle" icon="i-lucide-file-warning" :title="`The file of ${routeId} cannot be used`" :description="unreadable" />
            <UButton label="Delete it" icon="i-lucide-trash-2" color="error" variant="soft" @click="deleteOpen = true" />
          </div>
          <div v-else-if="routeId && (crewLoading === routeId || !loaded)" class="flex items-center gap-2 text-sm text-muted" data-crew-loading>
            <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading the crew…
          </div>
          <div v-else-if="loaded && routeId && !saved" class="flex flex-col items-start gap-3 text-sm text-muted">
            <p>No crew has the id <code>{{ routeId }}</code>.</p>
            <UButton label="All crews" icon="i-lucide-arrow-left" color="neutral" variant="soft" to="/crews" />
          </div>
          <UEmpty
            v-else-if="loaded && !list.length"
            icon="i-lucide-users"
            title="No crews yet"
            description="A crew is a saved team of agents: each with a role prompt, its own git worktree and a start condition. Launch it here or with conductor up <crew>."
            :actions="[{ label: 'New crew', icon: 'i-lucide-plus', onClick: newCrew }]"
          />
          <div v-else-if="loading" class="flex items-center gap-2 text-sm text-muted"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading crews…</div>
        </section>
      </div>

      <UModal
        v-model:open="shownOpen"
        :title="`${shownLink?.name ?? 'The crew'} launched`"
        :description="`Its view link lets anyone watch every agent of the run, view only, for ${shownLink?.hours ?? 0} h. It is shown this once: copy it now.`"
        data-view-link
      >
        <template #body>
          <div class="flex items-start gap-2.5 rounded-md bg-elevated px-3.5 py-3">
            <code class="min-w-0 flex-1 break-all font-mono text-xs select-all" data-view-link-url>{{ shownLink?.url }}</code>
            <UButton label="Copy" icon="i-lucide-clipboard" size="sm" class="flex-none" @click="shownLink && copy(shownLink.url, 'Link copied', 'Anyone with it can watch every member.')" />
          </div>
        </template>
        <template #footer>
          <div class="flex w-full justify-end gap-2">
            <UButton label="Open crew view" icon="i-lucide-layout-grid" color="neutral" variant="outline" :to="shownLink ? `/runs/${encodeURIComponent(shownLink.runId)}` : undefined" @click="shownLink = null" />
            <UButton label="Done" @click="shownLink = null" />
          </div>
        </template>
      </UModal>

      <UModal v-model:open="deleteOpen" :title="`Delete ${saved?.name ?? routeId ?? 'crew'}?`" description="The saved crew goes. Runs already launched keep going, and their worktrees and branches stay.">
        <template #footer>
          <div class="flex w-full justify-end gap-2">
            <UButton label="Cancel" color="neutral" variant="ghost" @click="deleteOpen = false" />
            <UButton label="Delete" icon="i-lucide-trash-2" color="error" :loading="deleting" @click="confirmDelete" />
          </div>
        </template>
      </UModal>
    </template>
  </UDashboardPanel>
</template>
