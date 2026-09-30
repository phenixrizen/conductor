<script setup lang="ts">
import type { AgentInfo, CrewInfo, RunInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { crewKey, defaultCrew, runActive, toCrewInput, toDraft, type DraftCrew } from '~/utils/crews'
import { relativeTime, shortCwd } from '~/utils/sessions'

// The crews list and the editor of the selected crew. /crews/<id> selects a
// crew (pages/crews/[id].vue renders this page); /crews shows a new draft, or
// moves to the first crew. Drafts live in app state: an edit survives moving
// to another crew or page until it is saved or discarded.

useHead({ title: 'Crews' })

const route = useRoute()
const router = useRouter()
const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const live = useAttention()

/** The key of a crew never saved in `drafts`. */
const NEW = ''

const crews = useState<CrewInfo[]>('crews', () => [])
const agents = useState<AgentInfo[]>('crewAgents', () => [])
const drafts = useState<Record<string, DraftCrew>>('crewDrafts', () => ({}))
/** The view link of the last launch, which the run view shows once. */
const launchLink = useState<{ runId: string; url: string; ttlSeconds: number } | null>('crewLaunchLink', () => null)
const runs = ref<RunInfo[]>([])
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
// App-wide: saving a new crew moves to its own page mid-launch, and that page must show the launch going on.
const saving = useState<boolean>('crewSaving', () => false)
const launching = useState<boolean>('crewLaunching', () => false)
const now = ref(Date.now())

const routeId = computed(() => (typeof route.params.id === 'string' ? route.params.id : undefined))
const selectedKey = computed<string | undefined>(() => routeId.value ?? (drafts.value[NEW] ? NEW : undefined))
const saved = computed(() => crews.value.find((c) => c.id === selectedKey.value))
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
    const [c, a, r] = await Promise.all([api.listCrews(), api.catalog(), api.listRuns()])
    // A draft nobody edited follows the crew as saved now (another admin, or `conductor crews`, may have changed it).
    const next = { ...drafts.value }
    for (const fresh of c) {
      const d = next[fresh.id]
      const before = crews.value.find((x) => x.id === fresh.id)
      if (d && before && crewKey(d) === crewKey(before)) next[fresh.id] = toDraft(fresh)
    }
    drafts.value = next
    crews.value = c
    agents.value = a
    runs.value = r
    error.value = ''
    loaded.value = true
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

async function refreshRuns() {
  try {
    runs.value = await api.listRuns()
  } catch {
    /* the badges keep what they showed */
  }
}

// A draft for the crew on screen, once it is known; /crews moves to the first crew.
watch(
  [selectedKey, crews, loaded],
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
  const c = crews.value.find((x) => x.id === key)
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

function meta(c: DraftCrew | CrewInfo, id?: string): string {
  const last = id ? runsOf(id)[0] : undefined
  return `${shortCwd(c.cwd) || 'server default'} · ${c.isolation === 'worktree' ? 'worktrees' : 'shared cwd'} · last run ${last ? relativeTime(last.startedAt, now.value) : 'never'}`
}

function agentOf(id: string) {
  return agents.value.find((a) => a.id === id)
}

/** The list: a new draft first, then every saved crew as its draft shows it. */
const list = computed(() => {
  const out: Array<{ key: string; crew: DraftCrew | CrewInfo; to: string }> = []
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
    const at = crews.value.findIndex((x) => x.id === c.id)
    crews.value = at >= 0 ? crews.value.map((x) => (x.id === c.id ? c : x)) : [...crews.value, c].sort((a, b) => a.name.localeCompare(b.name))
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
    crews.value = [...crews.value, copy].sort((a, b) => a.name.localeCompare(b.name))
    toast.add({ title: 'Crew duplicated', description: isDirty(c.id) ? `${copy.name}, from what ${c.name} has saved` : copy.name, icon: 'i-lucide-copy', color: 'success' })
    router.push(`/crews/${encodeURIComponent(copy.id)}`)
  } catch (e) {
    fail('Duplicate failed', e)
  }
}

const deleteOpen = ref(false)
const deleting = ref(false)

async function confirmDelete() {
  const c = saved.value
  if (!c) return
  deleting.value = true
  try {
    await api.deleteCrew(c.id)
    crews.value = crews.value.filter((x) => x.id !== c.id)
    const next = { ...drafts.value }
    delete next[c.id]
    drafts.value = next
    deleteOpen.value = false
    toast.add({ title: 'Crew deleted', description: c.name, icon: 'i-lucide-trash-2', color: 'neutral' })
    router.replace('/crews')
  } catch (e) {
    fail('Delete failed', e)
  } finally {
    deleting.value = false
  }
}

/**
 * Saves what changed, launches the crew, makes its view link when the crew
 * asks for one, and opens the run view or says where it is.
 */
async function launch() {
  const key = selectedKey.value
  if (key === undefined) return
  launching.value = true
  try {
    const c = isDirty(key) ? await save() : saved.value
    if (!c) return
    let run: RunInfo
    try {
      run = await api.launchCrew(c.id)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_a_repo') fail('Not a git repository', e)
      else fail('Launch failed', e)
      return
    }
    runs.value = [run, ...runs.value.filter((r) => r.id !== run.id)]
    let link: string | undefined
    if (c.viewLinkTtlSeconds) {
      try {
        const res = await api.createRunLink(run.id, { role: 'view', label: 'launch', ttlSeconds: c.viewLinkTtlSeconds })
        link = res.url
        launchLink.value = { runId: run.id, url: res.url, ttlSeconds: c.viewLinkTtlSeconds }
      } catch (e) {
        fail('The view link could not be made', e)
      }
    }
    if (c.openAfterLaunch) {
      await router.push(`/runs/${encodeURIComponent(run.id)}`)
      return
    }
    toast.add({
      title: `${c.name} launched`,
      description: link ? 'The view link is in the crew view, shown once.' : undefined,
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
  live.start()
  refresh()
  tick = window.setInterval(() => (now.value = Date.now()), 30000)
})
onBeforeUnmount(() => {
  window.clearInterval(tick)
  window.clearTimeout(runsTimer)
})
watch(
  () => admin.token.value,
  () => {
    // Another token may be another server: nothing typed under the old one stays.
    drafts.value = {}
    crews.value = []
    refresh()
  },
)
</script>

<template>
  <UDashboardPanel id="crews" :ui="{ body: 'p-0 sm:p-0 gap-0' }">
    <template #header>
      <UDashboardNavbar title="Crews">
        <template #leading>
          <SidebarReveal />
        </template>
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
                  <UIcon :name="agentOf(m.agentId)!.icon!" class="size-3.5" />
                </span>
                <SessionAvatar v-else :agent-id="m.agentId" />
              </template>
            </span>
            <span class="truncate font-mono text-[11px] text-muted" :title="meta(item.crew, item.key || undefined)">{{ meta(item.crew, item.key || undefined) }}</span>
          </NuxtLink>
          <p v-if="loaded && !list.length" class="px-1 text-sm text-muted">No crews yet.</p>
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

      <UModal v-model:open="deleteOpen" :title="`Delete ${saved?.name ?? 'crew'}?`" description="The saved crew goes. Runs already launched keep going, and their worktrees and branches stay.">
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
