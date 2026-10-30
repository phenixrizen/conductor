<script setup lang="ts">
import type { DispatchLabel, DispatchMilestone, DispatchSort, Horizon, TicketStatus } from '~/composables/useDispatch'
import type { AgentInfo } from '~/composables/useSessions'
import { HORIZONS, LIST_SORTS, PRIORITIES, TICKET_STATUSES, labelDot, sortDesc, statusWord } from '~/utils/dispatch'
import { emptyFilter, filterNarrows, MAX_SEARCH, type DispatchFilter } from '~/utils/dispatchQuery'
import { DispatchOrder, through } from '~/utils/dispatchOrder'

/**
 * A list's filter bar (docs/plans/dispatch.md, "web/app"): a search, then status, priority, labels, owner, assignees, milestone,
 * horizon and blocked, then the sort and its direction. The filter is the page's link (utils/dispatchQuery.ts): each change is a new
 * filter, which the page writes to the URL and reads the list by. The search waits for a pause in the typing. People are read from the
 * directory when a menu that offers them opens. On a phone the search shows and the rest folds behind Filters, which says how many
 * apply; opened, it wraps.
 */
const props = defineProps<{
  modelValue: DispatchFilter
  labels: readonly DispatchLabel[]
  milestones: readonly DispatchMilestone[]
  agents: readonly AgentInfo[]
}>()
const emit = defineEmits<{ 'update:modelValue': [filter: DispatchFilter] }>()

function set<K extends keyof DispatchFilter>(key: K, value: DispatchFilter[K]) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}

// The search: what is typed shows at once, and the list is asked once the typing pauses.
const SEARCH_PAUSE_MS = 300
const search = ref(props.modelValue.q)
let searchTimer: number | undefined
watch(
  () => props.modelValue.q,
  (q) => {
    if (q !== search.value.slice(0, MAX_SEARCH)) search.value = q
  },
)
function onSearch(v: string | number) {
  search.value = String(v)
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => set('q', search.value.slice(0, MAX_SEARCH)), SEARCH_PAUSE_MS)
}
onBeforeUnmount(() => window.clearTimeout(searchTimer))

const api = useDispatchApi()
const people = ref<string[]>([])
// The directory read whole, through a gate of its own (utils/dispatchOrder.ts): a slow reply never replaces a newer one.
const gate = new DispatchOrder()
async function loadPeople(open: boolean) {
  if (!open) return
  try {
    await through(gate, 'people', () => api.people(), (list) => (people.value = list.map((p) => p.name)))
  } catch {
    // The names read before stay offered.
  }
}

const statusItems = TICKET_STATUSES.map((s) => ({ label: statusWord(s).label, value: s, icon: statusWord(s).icon }))
const priorityItems = [...PRIORITIES].reverse().map((p) => ({ label: p.label, value: p.value, icon: p.icon }))
// A menu's value is never empty: not planned goes as its name.
const horizonItems = HORIZONS.map((h) => ({ label: h.label, value: h.name, icon: h.icon }))
const labelItems = computed(() => [...new Set(props.labels.map((l) => l.name))].sort((a, b) => a.localeCompare(b)).map((name) => ({ label: name, value: name, color: props.labels.find((l) => l.name === name)?.color ?? 'gray' })))
const milestoneItems = computed(() => [...new Set(props.milestones.map((m) => m.name))].map((name) => ({ label: name, value: name })))
const ownerItems = computed(() => [...new Set([...people.value, ...(props.modelValue.owner ? [props.modelValue.owner] : [])])].sort((a, b) => a.localeCompare(b)))
const assigneeItems = computed(() => [
  ...[...new Set([...people.value.map((n) => `person:${n}`), ...props.modelValue.assignees.filter((a) => a.startsWith('person:'))])].map((value) => ({ label: value.slice(7), value, icon: 'i-lucide-user' })),
  ...props.agents.map((a) => ({ label: a.name, value: `agent:${a.id}`, icon: 'i-lucide-bot' })),
])
const sortItems = LIST_SORTS.map((s) => ({ label: s.label, value: s.value }))

const horizonNames = computed(() => props.modelValue.horizon.map((h) => HORIZONS.find((x) => x.value === h)?.name ?? 'none'))
function setHorizons(v: unknown) {
  const names = Array.isArray(v) ? v : []
  set(
    'horizon',
    HORIZONS.filter((h) => names.includes(h.name)).map((h) => h.value as Horizon),
  )
}
function setSort(v: unknown) {
  if (typeof v !== 'string' || v === props.modelValue.sort) return
  // A sort is read in its own direction until turned.
  emit('update:modelValue', { ...props.modelValue, sort: v as DispatchSort, desc: sortDesc(v) })
}

const narrows = computed(() => filterNarrows(props.modelValue))
/** How many of the bar's filters apply, for the phone's Filters button. */
const applied = computed(() => {
  const f = props.modelValue
  return f.status.length + f.priority.length + f.labels.length + (f.owner ? 1 : 0) + f.assignees.length + (f.milestone ? 1 : 0) + f.horizon.length + (f.blocked ? 1 : 0)
})
/** On a phone: the filters shown. */
const unfolded = ref(false)
function clear() {
  window.clearTimeout(searchTimer)
  search.value = ''
  emit('update:modelValue', { ...emptyFilter(), sort: props.modelValue.sort, desc: props.modelValue.desc, group: props.modelValue.group })
}

const strings = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 sm:items-start" data-dispatch-filters>
    <div class="flex w-full items-center gap-2 sm:w-auto">
      <UInput :model-value="search" icon="i-lucide-search" placeholder="Search the tickets…" size="sm" class="min-w-0 flex-1 sm:w-56 sm:flex-none" :maxlength="MAX_SEARCH" aria-label="Search the tickets" data-filter-search @update:model-value="onSearch" />
      <UButton
        :label="applied ? `Filters · ${applied}` : 'Filters'"
        icon="i-lucide-list-filter"
        size="sm"
        color="neutral"
        :variant="unfolded ? 'soft' : 'outline'"
        class="sm:hidden"
        :aria-expanded="unfolded"
        data-filter-unfold
        @click="unfolded = !unfolded"
      />
    </div>
    <div class="w-full flex-wrap items-center gap-2 sm:flex sm:w-auto sm:flex-1" :class="unfolded ? 'flex' : 'hidden'" data-filter-fields>
      <USelectMenu
        :model-value="modelValue.status"
        :items="statusItems"
        value-key="value"
        multiple
        :search-input="false"
        placeholder="Status"
        size="sm"
        class="w-36"
        data-filter-status
        @update:model-value="(v: unknown) => set('status', strings(v) as TicketStatus[])"
      />
      <USelectMenu
        :model-value="modelValue.priority"
        :items="priorityItems"
        value-key="value"
        multiple
        :search-input="false"
        placeholder="Priority"
        size="sm"
        class="w-36"
        data-filter-priority
        @update:model-value="(v: unknown) => set('priority', Array.isArray(v) ? v.filter((x): x is number => typeof x === 'number') : [])"
      />
      <USelectMenu :model-value="modelValue.labels" :items="labelItems" value-key="value" multiple placeholder="Labels" size="sm" class="w-36" data-filter-labels @update:model-value="(v: unknown) => set('labels', strings(v))">
        <template #item-leading="{ item }">
          <span class="size-2 rounded-full" :class="labelDot(item.color)" />
        </template>
        <template #empty>No labels in this project yet.</template>
      </USelectMenu>
      <USelectMenu
        :model-value="modelValue.owner || undefined"
        :items="ownerItems"
        clear
        placeholder="Owner"
        size="sm"
        class="w-36"
        data-filter-owner
        @update:open="loadPeople"
        @update:model-value="(v: unknown) => set('owner', typeof v === 'string' ? v : '')"
      />
      <USelectMenu :model-value="modelValue.assignees" :items="assigneeItems" value-key="value" multiple placeholder="Assignees" size="sm" class="w-36" data-filter-assignees @update:open="loadPeople" @update:model-value="(v: unknown) => set('assignees', strings(v))" />
      <USelectMenu :model-value="modelValue.milestone || undefined" :items="milestoneItems" value-key="value" clear placeholder="Milestone" size="sm" class="w-36" data-filter-milestone @update:model-value="(v: unknown) => set('milestone', typeof v === 'string' ? v : '')">
        <template #empty>No milestones in this project yet.</template>
      </USelectMenu>
      <USelectMenu :model-value="horizonNames" :items="horizonItems" value-key="value" multiple :search-input="false" placeholder="Horizon" size="sm" class="w-36" data-filter-horizon @update:model-value="setHorizons" />
      <UButton
        label="Blocked"
        icon="i-lucide-octagon-pause"
        size="sm"
        color="neutral"
        :variant="modelValue.blocked ? 'soft' : 'outline'"
        :aria-pressed="modelValue.blocked"
        data-filter-blocked
        @click="set('blocked', !modelValue.blocked)"
      />
      <UButton v-if="narrows" label="Clear" icon="i-lucide-x" size="sm" color="neutral" variant="ghost" data-filter-clear @click="clear" />
      <div class="flex items-center gap-1 sm:ml-auto">
        <USelect :model-value="modelValue.sort" :items="sortItems" size="sm" class="w-32" aria-label="Sort by" data-filter-sort @update:model-value="setSort" />
        <UButton
          :icon="modelValue.desc ? 'i-lucide-arrow-down-wide-narrow' : 'i-lucide-arrow-up-narrow-wide'"
          size="sm"
          color="neutral"
          variant="outline"
          :aria-label="modelValue.desc ? 'Descending: turn it' : 'Ascending: turn it'"
          data-filter-direction
          @click="set('desc', !modelValue.desc)"
        />
      </div>
    </div>
  </div>
</template>
