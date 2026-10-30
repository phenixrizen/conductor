<script setup lang="ts">
import type { DispatchProject, ProjectSummary } from '~/composables/useDispatch'
import { projectsWatch } from '~/composables/useDispatchLive'
import { TRACKERS, projectPath } from '~/utils/dispatch'
import { relativeTime } from '~/utils/sessions'

// Dispatch's projects (docs/plans/dispatch.md, PR-8): each with its key, its name, how many of its tickets are open and done, and its
// tracker; New project makes one. The list is read through the page's ordering gate as the page opens, and again after the window on
// any dispatch event at all (a ticket's status moves its project's counts), and on a snapshot, which forgets what the gate knew. Nothing
// polls.

useHead({ title: 'Dispatch' })

const { api, gate, read } = useDispatch()
const admin = useWorkbenchToken()
const router = useRouter()

const projects = ref<ProjectSummary[]>([])
/** The projects were cut at MAX_PAGES pages. */
const cut = ref(false)
const loaded = ref(false)
const error = ref('')
const newOpen = ref(false)
const now = ref(Date.now())

/** At most this many pages of 200 projects are read for the page. */
const MAX_PAGES = 10

/** Reads the projects, every page up to MAX_PAGES, as one reply. */
async function readProjects(): Promise<void> {
  await read(
    'projects',
    async () => {
      const all: ProjectSummary[] = []
      let cursor = ''
      for (let i = 0; i < MAX_PAGES; i++) {
        const page = await api.projects(cursor)
        all.push(...(page.projects ?? []))
        cursor = page.next ?? ''
        if (!cursor) break
      }
      return { all, cut: !!cursor }
    },
    (r) => {
      projects.value = r.all
      cut.value = r.cut
      loaded.value = true
    },
  )
}

/** The projects read again; rejects when the read failed (the live store asks again). */
async function reload(): Promise<void> {
  try {
    await readProjects()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
    throw e
  }
}

async function load() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  await reload().catch(() => {})
}

let tick: number | undefined
onMounted(() => {
  void load()
  tick = window.setInterval(() => (now.value = Date.now()), 30_000)
})
onBeforeUnmount(() => window.clearInterval(tick))
watch(() => admin.token.value, load)
useDispatchLive({ ...projectsWatch(reload), reset: () => gate.reset() })

/** A project made: its page. */
function created(p: DispatchProject) {
  void router.push(projectPath(p.key))
}
</script>

<template>
  <UDashboardPanel id="dispatch" :ui="{ body: 'p-0 sm:p-0 gap-0' }">
    <template #header>
      <UDashboardNavbar :toggle="false" title="Dispatch">
        <template #right>
          <UButton icon="i-lucide-plus" aria-label="New project" data-new-project-open @click="newOpen = true"><span class="hidden sm:inline">New project</span></UButton>
          <FullscreenButton class="hidden sm:inline-flex" />
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div v-if="error" class="flex-none px-4 pt-3 sm:px-6">
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
      </div>

      <div v-if="!loaded" class="flex flex-1 items-center justify-center p-8 text-sm text-muted">{{ error ? '' : 'Reading the projects…' }}</div>

      <div v-else-if="!projects.length" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center" data-projects-empty>
        <UIcon name="i-lucide-ticket" class="size-8 text-muted" />
        <p class="max-w-md text-sm text-muted">Dispatch keeps a team's work as tickets: what is asked, who owns it, what became of it. A project holds them under its key.</p>
        <UButton label="New project" icon="i-lucide-plus" @click="newOpen = true" />
      </div>

      <div v-else class="flex flex-col divide-y divide-default" data-projects>
        <NuxtLink
          v-for="p in projects"
          :key="p.id"
          :to="projectPath(p.key)"
          class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 outline-none hover:bg-elevated/50 focus-visible:bg-elevated focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary sm:flex-nowrap sm:px-6"
          :data-project-row="p.key"
        >
          <span class="flex min-w-0 basis-full items-center gap-3 sm:flex-1 sm:basis-auto">
            <span class="w-16 flex-none font-mono text-sm font-semibold text-highlighted" data-project-key>{{ p.key }}</span>
            <span class="min-w-0 truncate text-sm text-default" data-project-name>{{ p.name }}</span>
            <UBadge v-if="p.archivedAt" label="archived" color="neutral" variant="subtle" size="sm" class="flex-none" />
          </span>
          <span class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted sm:flex-none sm:flex-nowrap">
            <span class="tabular-nums" data-project-open :data-count="p.open"><b class="font-semibold text-default">{{ p.open }}</b> open</span>
            <span class="tabular-nums" data-project-done :data-count="p.done"><b class="font-semibold text-default">{{ p.done }}</b> done</span>
            <span class="flex items-center gap-1" data-project-tracker><UIcon :name="TRACKERS[p.tracker]?.icon ?? 'i-lucide-ticket'" class="size-3.5" />{{ TRACKERS[p.tracker]?.label ?? p.tracker }}</span>
            <time class="w-14 text-right" :datetime="p.updatedAt" :title="`Updated ${new Date(p.updatedAt).toLocaleString()}`">{{ relativeTime(p.updatedAt, now) }}</time>
          </span>
        </NuxtLink>
        <p v-if="cut" class="px-4 py-3 text-xs text-muted sm:px-6">The first {{ projects.length }} projects by key.</p>
      </div>
    </template>
  </UDashboardPanel>

  <DispatchNewProject v-model:open="newOpen" :gate="gate" @created="created" />
</template>
