<script setup lang="ts">
import type { DispatchLabel, DispatchMilestone, TicketSummary } from '~/composables/useDispatch'
import type { AgentInfo } from '~/composables/useSessions'
import { assigneeOf, labelDot, listFocusStep, listRefocus, priorityWord, statusWord, ticketPath, ticketWords } from '~/utils/dispatch'
import { avatarTone } from '~/utils/avatar'
import { initials, relativeTime } from '~/utils/sessions'

/**
 * A project's tickets as a list (docs/plans/dispatch.md, "web/app"): a row each, in the order the server sorted them, with its key, its
 * status, its title, its labels, its milestone, its priority, its owner, its assignees as avatar chips (a person's initials, an agent as
 * the sidebar shows it) and when it was last updated. A row is a link to the ticket's page. The keyboard moves a roving focus over the
 * rows (utils/dispatch.ts: J and K, or the arrows, wrapping at the ends; a row gone hands the focus on; Enter opens the row, as a link
 * does). On a phone a row wraps: its key and title on one line, the rest below.
 */
const props = defineProps<{
  tickets: readonly TicketSummary[]
  labels: ReadonlyMap<string, DispatchLabel>
  milestones: ReadonlyMap<string, DispatchMilestone>
  agents: readonly AgentInfo[]
  now: number
}>()

/** The key of the row the roving focus is on, or none. */
const focus = ref<string | null>(null)
const root = useTemplateRef<HTMLElement>('root')

/** The row of a key, as drawn. */
function rowOf(key: string): HTMLElement | null {
  return root.value?.querySelector<HTMLElement>(`[data-ticket-row="${CSS.escape(key)}"]`) ?? null
}

const keys = computed(() => props.tickets.map((t) => t.key))

/** Moves the focus a row down (1) or up (-1), wrapping at the ends, and gives that row the keyboard. */
function step(delta: 1 | -1) {
  focus.value = listFocusStep(keys.value, focus.value, delta)
  void nextTick(() => {
    if (focus.value) rowOf(focus.value)?.focus()
  })
}

/** Leaves the list: no row has the focus. */
function leave() {
  const active = document.activeElement
  if (active instanceof HTMLElement && root.value?.contains(active)) active.blur()
  focus.value = null
}

// The rows change (a re-read, a filter): the focus stays on its row, or a row gone hands it on, and the keyboard follows it when it was
// in the list.
watch(keys, (after, before) => {
  const had = root.value?.contains(document.activeElement) ?? false
  focus.value = listRefocus(before ?? [], after, focus.value)
  if (had && focus.value) {
    void nextTick(() => {
      if (focus.value) rowOf(focus.value)?.focus()
    })
  }
})

function onKeydown(e: KeyboardEvent) {
  if (e.altKey || e.ctrlKey || e.metaKey) return
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    step(e.key === 'ArrowDown' ? 1 : -1)
  }
}

/** An actor's name: the person's, or the agent's from the catalog. */
function actorName(actor: string): string {
  const a = assigneeOf(actor)
  return a.kind === 'agent' ? (props.agents.find((x) => x.id === a.name)?.name ?? a.name) : a.name
}

const iconColor: Record<string, string> = { neutral: 'text-muted', primary: 'text-primary', success: 'text-success', info: 'text-info', warning: 'text-warning', error: 'text-error' }

defineExpose({ step, leave, focus })
</script>

<template>
  <div ref="root" class="flex flex-col divide-y divide-default" data-ticket-list @keydown="onKeydown">
    <NuxtLink
      v-for="t in tickets"
      :key="t.id"
      :to="ticketPath(t.key)"
      class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 outline-none hover:bg-elevated/50 focus-visible:bg-elevated focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary sm:flex-nowrap sm:px-6"
      :class="focus === t.key && 'bg-elevated/60'"
      :data-ticket-row="t.key"
      :data-ticket-row-status="t.status"
      @focus="focus = t.key"
    >
      <!-- The key, the status and the title: one line, the whole width on a phone. -->
      <span class="flex min-w-0 basis-full items-center gap-2 sm:flex-1 sm:basis-auto">
        <UIcon :name="statusWord(t.status).icon" class="size-4 flex-none" :class="iconColor[statusWord(t.status).color]" :title="ticketWords(t).text" data-row-status-icon />
        <span class="sr-only">{{ ticketWords(t).text }}</span>
        <span class="w-[4.5rem] flex-none truncate font-mono text-xs text-muted" data-row-key>{{ t.key }}</span>
        <span class="min-w-0 break-words text-sm text-highlighted sm:truncate" data-row-title>{{ t.title }}</span>
        <UBadge v-if="t.blocked && t.status !== 'done' && t.status !== 'cancelled'" label="blocked" color="warning" variant="subtle" size="sm" class="flex-none" data-row-blocked />
      </span>

      <!-- The rest: below on a phone, under the title, at the end of the line on a wide screen. -->
      <span class="flex min-w-0 flex-wrap items-center gap-2 pl-6 text-xs text-muted sm:flex-none sm:flex-nowrap sm:pl-0">
        <span v-for="id in t.labels" :key="id" class="flex max-w-32 items-center gap-1 rounded-full px-1.5 py-0.5 ring ring-default" data-row-label>
          <span class="size-1.5 flex-none rounded-full" :class="labelDot(labels.get(id)?.color ?? 'gray')" />
          <span class="truncate">{{ labels.get(id)?.name ?? 'label' }}</span>
        </span>
        <span v-if="t.milestoneId && milestones.get(t.milestoneId)" class="flex max-w-32 items-center gap-1" data-row-milestone>
          <UIcon name="i-lucide-milestone" class="size-3.5 flex-none" />
          <span class="truncate">{{ milestones.get(t.milestoneId)?.name }}</span>
        </span>
        <UIcon :name="priorityWord(t.priority).icon" class="size-4 flex-none" :class="t.priority === 4 ? 'text-error' : t.priority === 0 ? 'text-dimmed' : 'text-muted'" :title="priorityWord(t.priority).label" data-row-priority />
        <span class="sr-only">{{ priorityWord(t.priority).label }}</span>
        <span v-if="t.owner" class="flex max-w-28 items-center gap-1" :title="`Owner: ${t.owner}`" data-row-owner>
          <UIcon name="i-lucide-user" class="size-3.5 flex-none" />
          <span class="truncate">{{ t.owner }}</span>
        </span>
        <span v-if="t.assignees.length" class="flex flex-none items-center gap-0.5" :title="`Assigned to ${t.assignees.map(actorName).join(', ')}`" data-row-assignees>
          <template v-for="a in t.assignees.slice(0, 3)" :key="a">
            <SessionAvatar v-if="assigneeOf(a).kind === 'agent'" :agent-id="assigneeOf(a).name" />
            <span v-else class="grid size-6 place-items-center rounded-full text-[10px] font-semibold" :class="avatarTone(assigneeOf(a).name)">{{ initials(assigneeOf(a).name) }}</span>
          </template>
          <span v-if="t.assignees.length > 3" class="grid size-6 place-items-center rounded-full bg-elevated text-[10px]">+{{ t.assignees.length - 3 }}</span>
        </span>
        <time class="w-12 flex-none text-right tabular-nums" :datetime="t.updatedAt" :title="`Updated ${new Date(t.updatedAt).toLocaleString()}`" data-row-updated>{{ relativeTime(t.updatedAt, now) }}</time>
      </span>
    </NuxtLink>
  </div>
</template>
