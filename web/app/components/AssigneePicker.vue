<script setup lang="ts">
import type { AgentInfo } from '~/composables/useSessions'
import { assigneeOf } from '~/utils/dispatch'
import { avatarTone } from '~/utils/avatar'
import { initials } from '~/utils/sessions'

/**
 * A ticket's assignees (docs/plans/dispatch.md, "web/app"): people by name (`person:<name>`; a new name typed is added to the directory
 * as it is assigned) and the catalog's agents (`agent:<id>`), each as a chip, a person's with their initials and an agent's as the
 * sidebar shows it. The model is the actors; the page sends what was added and what was removed as one operation. The directory is read
 * again whole each time the picker opens.
 */
const props = defineProps<{ modelValue: string[]; agents: readonly AgentInfo[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [actors: string[]] }>()

/** A ticket's assignees' bound (the store's MaxAssignees). */
const MAX_ASSIGNEES = 8

const api = useDispatchApi()
const people = ref<string[]>([])

async function load() {
  try {
    people.value = (await api.people()).map((p) => p.name)
  } catch {
    // The names read before stay offered; a typed name still assigns.
  }
}
onMounted(load)

/** An actor in words: the person's name, or the agent's from the catalog. */
function nameOf(actor: string): string {
  const a = assigneeOf(actor)
  return a.kind === 'agent' ? (props.agents.find((x) => x.id === a.name)?.name ?? a.name) : a.name
}

const items = computed(() => {
  const named = new Set([...people.value.map((n) => `person:${n}`), ...props.modelValue.filter((a) => a.startsWith('person:'))])
  const persons = [...named].sort((a, b) => a.localeCompare(b)).map((value) => ({ label: nameOf(value), value }))
  const agents = props.agents.map((a) => ({ label: a.name, value: `agent:${a.id}`, icon: 'i-lucide-bot' }))
  for (const actor of props.modelValue) if (actor.startsWith('agent:') && !agents.some((a) => a.value === actor)) agents.push({ label: nameOf(actor), value: actor, icon: 'i-lucide-bot' })
  // A group's heading is no item to pick; it carries a value only so that every item has the key the menu reads.
  return [{ type: 'label' as const, label: 'People', value: 'heading:people' }, ...persons, { type: 'label' as const, label: 'Agents', value: 'heading:agents' }, ...agents]
})

function pick(v: unknown) {
  const actors = Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string' && !x.startsWith('heading:')) : []
  emit('update:modelValue', actors)
}

function create(name: string) {
  const n = name.trim()
  if (n && !props.modelValue.includes(`person:${n}`)) emit('update:modelValue', [...props.modelValue, `person:${n}`])
}
</script>

<template>
  <USelectMenu
    :model-value="modelValue"
    :items="items"
    value-key="value"
    multiple
    create-item="always"
    :disabled="disabled"
    :search-input="{ placeholder: 'A name or an agent…' }"
    size="sm"
    class="w-full"
    data-assignee-picker
    @update:open="(open: boolean) => open && load()"
    @update:model-value="pick"
    @create="create"
  >
    <template #default>
      <span v-if="modelValue.length" class="flex min-w-0 flex-wrap items-center gap-1" data-assignee-chips>
        <span v-for="a in modelValue" :key="a" class="flex min-w-0 items-center gap-1 text-xs" :data-assignee="a">
          <SessionAvatar v-if="assigneeOf(a).kind === 'agent'" :agent-id="assigneeOf(a).name" />
          <span v-else class="grid size-5 flex-none place-items-center rounded-full text-[9px] font-semibold" :class="avatarTone(assigneeOf(a).name)">{{ initials(assigneeOf(a).name) }}</span>
          <span class="truncate">{{ nameOf(a) }}</span>
        </span>
      </span>
      <span v-else class="text-dimmed">No one</span>
    </template>
    <template #create-item-label="{ item }">Assign {{ item }}</template>
    <template #content-bottom>
      <p v-if="modelValue.length >= MAX_ASSIGNEES" class="px-2 py-1.5 text-xs text-muted">A ticket has at most {{ MAX_ASSIGNEES }} assignees.</p>
    </template>
  </USelectMenu>
</template>
