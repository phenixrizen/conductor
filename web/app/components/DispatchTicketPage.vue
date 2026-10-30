<script setup lang="ts">
import type { DispatchChange, DispatchTicket, Horizon, ProjectRead, TicketEdit, TicketReply, TicketStatus } from '~/composables/useDispatch'
import type { AgentInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { ticketWatch } from '~/composables/useDispatchLive'
import { HORIZONS, PRIORITIES, TICKET_STATUSES, assigneesEdit, historyEntries, horizonWord, labelDot, parseTicketKey, priorityWord, projectPath, statusWord, ticketPath, ticketWords, validProjectKey } from '~/utils/dispatch'
import { relativeTime } from '~/utils/sessions'
import { serially } from '~/utils/dispatchOrder'

/**
 * A ticket's page (docs/plans/dispatch.md, PR-8): its key and title, its description and acceptance criteria as Markdown with a
 * preview, its status, priority, horizon, owner and assignees, and its history as the store recorded it. Every edit is one operation on
 * the revision shown (useDispatchOps), and every read and every operation's reply goes through the page's ordering gate, the ticket's by
 * its key, a 404 among them. The live events read it again: at once when one names the ticket, after the window on any other of its
 * project, and on a snapshot, which forgets what the gate knew (a store restored from a backup). Its comments, attachments, parent and
 * relations, labels and milestones, and the close policy come with the next changes, and the page says so. One column on a phone.
 */
const props = defineProps<{ project: string; ticketKey: string }>()

const { api, gate, read, readOrAbsent } = useDispatch()
const ops = useDispatchOps(gate)
const sessions = useSessions()
const admin = useWorkbenchToken()

const ticket = ref<DispatchTicket | null>(null)
const projectRead = ref<ProjectRead | null>(null)
const changes = ref<DispatchChange[]>([])
/** The seq after which the history's next page starts; 0 when it was read whole. */
const historyNext = ref(0)
/** How many pages of the history are shown: a re-read reads them all again. */
const historyPages = ref(1)
const historyLoading = ref(false)
const agents = ref<AgentInfo[]>([])
const error = ref('')
/** No ticket has the key. */
const missing = ref(false)
const now = ref(Date.now())

useHead({ title: computed(() => (ticket.value ? `${ticket.value.key} ${ticket.value.title}` : props.ticketKey)) })

/**
 * Reads the ticket in full; a key no ticket has says so, and is no failure to ask again. That answer goes through the gate as any reply,
 * so a slow 404 never hides the ticket a later read found (one made since the page opened).
 */
async function readTicket(): Promise<void> {
  const key = props.ticketKey
  if (!parseTicketKey(key)) {
    missing.value = true
    return
  }
  await readOrAbsent(`ticket:${key}`, () => api.ticket(key), {
    apply: (t) => {
      if (key !== props.ticketKey) return
      ticket.value = t
      missing.value = false
    },
    rev: (t) => t.rev,
    absent: notFound,
    gone: () => {
      if (key === props.ticketKey) missing.value = true
    },
  })
}

/** A 404, or a 400 for what is no key: the ticket or the project has no row, which reading again does not change. */
function notFound(e: unknown): boolean {
  return e instanceof ApiError && ((e.status === 404 && e.code === 'not_found') || (e.status === 400 && e.code === 'invalid_request'))
}

/** Reads the pages of the history shown, from the start, as one reply. */
async function readHistory(): Promise<void> {
  const key = props.ticketKey
  if (!parseTicketKey(key)) return
  const pages = historyPages.value
  historyLoading.value = true
  try {
    await read(
      `history:${key}`,
      async () => {
        const all: DispatchChange[] = []
        let after = 0
        let next = 0
        for (let i = 0; i < pages; i++) {
          const page = await api.history(key, after)
          all.push(...(page.changes ?? []))
          next = page.next ?? 0
          if (!next) break
          after = next
        }
        return { all, next }
      },
      (r) => {
        if (key !== props.ticketKey) return
        changes.value = r.all
        historyNext.value = r.next
      },
    )
  } catch (e) {
    if (!notFound(e)) throw e
  } finally {
    historyLoading.value = false
  }
}

/** Reads the ticket's project: its labels and milestones by name, and whether it is archived. */
async function readProject(): Promise<void> {
  const key = props.project
  if (!validProjectKey(key)) return
  try {
    await read(
      `project:${key}`,
      () => api.project(key),
      (p) => {
        if (key === props.project) projectRead.value = p
      },
      (p) => p.project.rev,
    )
  } catch (e) {
    if (!notFound(e)) throw e
  }
}

/** Everything the page shows, read again: the ticket, its history, its project. Rejects when a read failed (the live store asks again). */
async function reload(): Promise<void> {
  const results = await Promise.allSettled([readTicket(), readHistory(), readProject()])
  const failed = results.find((r): r is PromiseRejectedResult => r.status === 'rejected')
  if (failed) {
    error.value = (failed.reason as Error).message
    throw failed.reason
  }
  error.value = ''
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
  sessions
    .catalog()
    .then((a) => (agents.value = a))
    .catch(() => {})
  // The clock of "updated 5m ago": no read of the server.
  tick = window.setInterval(() => (now.value = Date.now()), 30_000)
})
onBeforeUnmount(() => window.clearInterval(tick))
watch(() => admin.token.value, load)

// Another ticket on the same page (a link from this one): what was shown goes, and its late replies with it.
watch(
  () => [props.project, props.ticketKey] as const,
  ([projectKey, key], [oldProject, oldKey]) => {
    gate.drop(`ticket:${oldKey}`)
    gate.drop(`history:${oldKey}`)
    if (projectKey !== oldProject) {
      gate.drop(`project:${oldProject}`)
      projectRead.value = null
    }
    ticket.value = null
    changes.value = []
    historyPages.value = 1
    historyNext.value = 0
    missing.value = false
    if (key) void load()
  },
)

useDispatchLive({ ...ticketWatch(() => ticket.value?.id ?? '', () => props.project, reload), reset: () => gate.reset() })

const projectRow = computed(() => projectRead.value?.project ?? null)
const archived = computed(() => !!projectRow.value?.archivedAt)
const deleted = computed(() => !!ticket.value?.deletedAt)
/** Nothing is edited on a deleted ticket or in an archived project: the store would refuse it. */
const locked = computed(() => !ticket.value || deleted.value || archived.value)

const words = computed(() => (ticket.value ? ticketWords(ticket.value) : null))
const labelsById = computed(() => new Map((projectRead.value?.labels ?? []).map((l) => [l.id, l])))
const milestonesById = computed(() => new Map((projectRead.value?.milestones ?? []).map((m) => [m.id, m])))
const entries = computed(() =>
  historyEntries(changes.value, {
    labels: new Map([...labelsById.value].map(([id, l]) => [id, l.name])),
    milestones: new Map([...milestonesById.value].map(([id, m]) => [id, m.name])),
    agents: new Map(agents.value.map((a) => [a.id, a.name])),
    tickets: ticket.value?.parent ? new Map([[ticket.value.parent.id, ticket.value.parent.key]]) : undefined,
  }),
)

/**
 * One edit of the ticket, as one operation on the revision shown when it is sent; true once the store has it. The page's edits go one
 * after another (serially): each is sent once the one before has answered, so it expects the revision that one left, where two
 * assignees ticked in a row would both expect the revision shown at the first click and the second be refused stale_revision. The
 * change is made then too (`make`), against the ticket as the one before left it: null when nothing is left to change, which is no
 * operation (the store refuses an edit that changes nothing), so a field set twice in a row, or a member ticked and unticked, sends
 * what it comes to.
 */
const edit = serially(async (make: () => TicketEdit | null): Promise<boolean> => {
  const t = ticket.value
  if (!t || locked.value) return false
  const change = make()
  if (!change) return true
  const key = t.key
  let refused = false
  const reply = await ops.run<TicketReply>((body) => api.editTicket(key, { ...body, ...change }), {
    thing: `ticket:${key}`,
    what: key,
    expectedRev: t.rev,
    apply: (r) => {
      if (r.ticket && key === props.ticketKey) ticket.value = r.ticket
    },
    rev: (r) => r.ticket?.rev,
    refused: () => (refused = true),
  })
  // A refusal (a stale revision: another person's edit came first) shows the ticket as the store holds it now, read before the next
  // edit is sent, so that one expects the revision the store has.
  if (refused) await reload().catch(() => {})
  else if (reply) void readHistory().catch(() => {})
  return !!reply
})

// The title: shown, or edited in place (Enter saves, Escape leaves it).
const editingTitle = ref(false)
const titleDraft = ref('')
const titleInput = useTemplateRef<{ inputRef?: HTMLInputElement }>('titleInput')
function editTitle() {
  if (!ticket.value || locked.value) return
  titleDraft.value = ticket.value.title
  editingTitle.value = true
  void nextTick(() => titleInput.value?.inputRef?.select())
}
async function saveTitle(e?: KeyboardEvent) {
  // Enter that ends an input method's composition is the composition's.
  if (e?.isComposing) return
  const title = titleDraft.value.trim()
  if (!ticket.value || !title || title === ticket.value.title) {
    editingTitle.value = false
    return
  }
  if (await edit(() => (title !== ticket.value?.title ? { set: { title } } : null))) editingTitle.value = false
}

/** A text field saved: its edit, or none when the ticket holds the text already. */
function saveText(field: 'description' | 'acceptance', text: string): Promise<boolean> {
  return edit(() => (text !== ticket.value?.[field] ? { set: { [field]: text } } : null))
}

const statusItems = TICKET_STATUSES.map((s) => ({ label: statusWord(s).label, value: s, icon: statusWord(s).icon }))
const priorityItems = PRIORITIES.map((p) => ({ label: p.label, value: p.value, icon: p.icon }))
// A select's value is never empty: not planned goes as its name.
const horizonItems = HORIZONS.map((h) => ({ label: h.label, value: h.name, icon: h.icon }))

function setStatus(v: unknown) {
  if (typeof v !== 'string') return
  void edit(() => (v !== ticket.value?.status ? { set: { status: v as TicketStatus } } : null))
}
function setPriority(v: unknown) {
  if (typeof v !== 'number') return
  void edit(() => (v !== ticket.value?.priority ? { set: { priority: v } } : null))
}
function setHorizon(v: unknown) {
  const h = HORIZONS.find((x) => x.name === v)?.value
  if (h === undefined) return
  void edit(() => (h !== ticket.value?.horizon ? { set: { horizon: h as Horizon } } : null))
}
function setOwner(name: string) {
  void edit(() => (name !== ticket.value?.owner ? { set: { owner: name } } : null))
}
/** The assignees the picker left: what they add and remove, against the ticket as it is when the edit is sent. */
function setAssignees(next: string[]) {
  void edit(() => assigneesEdit(ticket.value?.assignees ?? [], next))
}

function moreHistory() {
  historyPages.value++
  void readHistory().catch((e: Error) => (error.value = e.message))
}

/** The Markdown fields' bounds, the store's, in bytes. */
const MAX_DESCRIPTION = 64 << 10
const MAX_ACCEPTANCE = 16 << 10
</script>

<template>
  <UDashboardPanel id="dispatch-ticket" :ui="{ body: 'p-0 sm:p-0 gap-0' }">
    <template #header>
      <UDashboardNavbar :toggle="false" :ui="{ left: 'min-w-0 flex-1' }" data-ticket-header>
        <template #leading>
          <UButton icon="i-lucide-arrow-left" color="neutral" variant="ghost" :aria-label="`Back to ${project}`" :to="projectPath(project)" data-back-to-project />
        </template>
        <template #title>
          <span class="flex min-w-0 items-baseline gap-2">
            <NuxtLink to="/dispatch" class="hidden text-muted hover:underline sm:inline">Dispatch</NuxtLink>
            <span class="hidden text-dimmed sm:inline">/</span>
            <NuxtLink :to="projectPath(project)" class="flex-none text-muted hover:underline" data-ticket-project>{{ project }}</NuxtLink>
            <span class="text-dimmed">/</span>
            <span class="flex-none font-mono" data-ticket-key>{{ ticketKey }}</span>
          </span>
        </template>
      </UDashboardNavbar>
    </template>

    <template #body>
      <div v-if="error" class="flex-none px-4 pt-3 sm:px-6">
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
      </div>

      <div v-if="missing" class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-muted" data-ticket-missing>
        <UIcon name="i-lucide-search-x" class="size-8" />
        <p class="text-sm">No ticket has the key <code>{{ ticketKey }}</code>.</p>
        <UButton :label="`Back to ${project}`" icon="i-lucide-arrow-left" color="neutral" variant="soft" :to="projectPath(project)" />
      </div>

      <div v-else-if="ticket" class="flex flex-col gap-5 px-4 py-5 sm:px-6" :data-ticket="ticket.key" :data-ticket-rev="ticket.rev">
        <UAlert v-if="deleted" color="neutral" variant="subtle" icon="i-lucide-trash-2" title="This ticket is deleted." description="Its key is kept; nothing of it can be changed." data-ticket-deleted />
        <UAlert v-else-if="archived" color="neutral" variant="subtle" icon="i-lucide-archive" :title="`${project} is archived.`" description="Nothing of its tickets can be changed until it is unarchived." data-ticket-archived />

        <!-- The title, edited in place, and the ticket's state in words. -->
        <div class="flex flex-col gap-1.5">
          <div v-if="editingTitle" class="flex items-center gap-2">
            <UInput
              ref="titleInput"
              v-model="titleDraft"
              aria-label="Title"
              maxlength="200"
              size="lg"
              class="min-w-0 flex-1"
              data-title-input
              @keydown.enter.prevent="(e: KeyboardEvent) => saveTitle(e)"
              @keydown.escape.prevent.stop="editingTitle = false"
            />
            <UButton label="Save" icon="i-lucide-check" :loading="ops.busy.value === `ticket:${ticket.key}`" :disabled="!titleDraft.trim()" data-title-save @click="saveTitle()" />
            <UButton label="Cancel" color="neutral" variant="ghost" @click="editingTitle = false" />
          </div>
          <h1 v-else class="group flex items-start gap-2 text-xl font-semibold text-highlighted">
            <span class="min-w-0 break-words" data-ticket-title>{{ ticket.title }}</span>
            <UButton v-if="!locked" icon="i-lucide-pencil" size="xs" color="neutral" variant="ghost" aria-label="Edit the title" class="mt-0.5 flex-none opacity-60 group-hover:opacity-100" data-title-edit @click="editTitle" />
          </h1>
          <p class="text-xs text-muted">
            <span :class="{ 'text-warning': words?.tone === 'warning', 'text-success': words?.tone === 'success' }" data-ticket-words>{{ words?.text }}</span>
            <span class="mx-1">·</span>
            made by {{ ticket.createdBy || 'admin' }} {{ relativeTime(ticket.createdAt, now, { suffix: true }) }}
            <span class="mx-1">·</span>
            updated {{ relativeTime(ticket.updatedAt, now, { suffix: true }) }}
          </p>
        </div>

        <!-- Its text and history, its properties beside them on a wide screen; on a phone one column, the properties first. -->
        <div class="flex flex-col gap-6 lg:flex-row lg:items-start">
          <div class="flex min-w-0 flex-1 flex-col gap-6">
            <TicketMarkdown :text="ticket.description" label="Description" empty="Add a description…" :max-bytes="MAX_DESCRIPTION" :save="(text) => saveText('description', text)" :disabled="locked" name="description" />
            <TicketMarkdown :text="ticket.acceptance" label="Acceptance criteria" empty="Add what must hold for it to be done…" :max-bytes="MAX_ACCEPTANCE" :save="(text) => saveText('acceptance', text)" :disabled="locked" name="acceptance" />

            <p class="flex items-start gap-2 rounded-md border border-dashed border-default px-4 py-3 text-sm text-muted" data-ticket-later>
              <UIcon name="i-lucide-construction" class="mt-0.5 size-4 flex-none" />
              <span>Comments, attachments, the parent and relations, and the close policy with its acceptance come with the next changes to this page.</span>
            </p>

            <DispatchHistory :entries="entries" :now="now" :more="!!historyNext" :loading="historyLoading" @more="moreHistory" />
          </div>

          <aside class="order-first grid grid-cols-2 gap-x-3 gap-y-4 rounded-lg p-4 ring ring-default lg:sticky lg:top-4 lg:order-none lg:flex lg:w-80 lg:flex-none lg:flex-col" data-ticket-properties>
            <UFormField label="Status" size="sm">
              <USelect :model-value="ticket.status" :items="statusItems" :disabled="locked" size="sm" class="w-full" :icon="statusWord(ticket.status).icon" data-ticket-status @update:model-value="setStatus" />
            </UFormField>
            <UFormField label="Priority" size="sm">
              <USelect :model-value="ticket.priority" :items="priorityItems" :disabled="locked" size="sm" class="w-full" :icon="priorityWord(ticket.priority).icon" data-ticket-priority @update:model-value="setPriority" />
            </UFormField>
            <UFormField label="Horizon" size="sm">
              <USelect :model-value="horizonWord(ticket.horizon).name" :items="horizonItems" :disabled="locked" size="sm" class="w-full" :icon="horizonWord(ticket.horizon).icon" data-ticket-horizon @update:model-value="setHorizon" />
            </UFormField>
            <UFormField label="Owner" size="sm" hint="accountable">
              <PersonPicker :model-value="ticket.owner" :disabled="locked" data-ticket-owner @update:model-value="setOwner" />
            </UFormField>
            <UFormField label="Assignees" size="sm" class="col-span-2">
              <AssigneePicker :model-value="ticket.assignees" :agents="agents" :disabled="locked" data-ticket-assignees @update:model-value="setAssignees" />
            </UFormField>
            <USeparator class="col-span-2" />
            <div class="flex flex-col gap-1.5 text-sm" data-ticket-labels>
              <span class="text-xs font-medium text-muted">Labels</span>
              <span v-if="!ticket.labels.length" class="text-dimmed">None</span>
              <span v-else class="flex flex-wrap gap-1.5">
                <span v-for="id in ticket.labels" :key="id" class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs ring ring-default">
                  <span class="size-2 rounded-full" :class="labelDot(labelsById.get(id)?.color ?? 'gray')" />{{ labelsById.get(id)?.name ?? 'a label' }}
                </span>
              </span>
            </div>
            <div class="flex flex-col gap-1.5 text-sm" data-ticket-milestone>
              <span class="text-xs font-medium text-muted">Milestone</span>
              <span :class="ticket.milestoneId && milestonesById.get(ticket.milestoneId) ? 'text-default' : 'text-dimmed'">{{ (ticket.milestoneId && milestonesById.get(ticket.milestoneId)?.name) || 'None' }}</span>
            </div>
            <div v-if="ticket.parent" class="col-span-2 flex flex-col gap-1.5 text-sm" data-ticket-parent>
              <span class="text-xs font-medium text-muted">Parent</span>
              <NuxtLink :to="ticketPath(ticket.parent.key)" class="truncate hover:underline">
                <span class="font-mono text-xs text-muted">{{ ticket.parent.key }}</span> {{ ticket.parent.title }}<template v-if="ticket.parent.deletedAt"> (deleted)</template>
              </NuxtLink>
            </div>
            <p class="col-span-2 text-xs text-dimmed">Labels, the milestone and the parent are set here with the next changes.</p>
          </aside>
        </div>
      </div>

      <div v-else class="flex flex-1 items-center justify-center p-8 text-sm text-muted">Reading {{ ticketKey }}…</div>
    </template>
  </UDashboardPanel>
</template>
