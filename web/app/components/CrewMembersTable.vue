<script setup lang="ts">
import type { AgentInfo } from '~/composables/useSessions'
import { agentIcon } from '~/utils/agentIcons'
import { argsFrom, memberNameError, startFrom, startValue, type DraftMember } from '~/utils/crews'

/**
 * The members of a crew, one row each: name, agent, role prompt, extra
 * arguments and when it starts. Rows are reordered by dragging the handle, or
 * with the arrow keys on it (the order is for display only). `single` shows
 * one row alone, for adding a member to a run; `others` are the names outside
 * the table (the run's members), which a row may not take and may start
 * after.
 */
const members = defineModel<DraftMember[]>({ required: true })
const props = withDefaults(defineProps<{ agents: AgentInfo[]; others?: string[]; single?: boolean }>(), { others: () => [], single: false })
const emit = defineEmits<{ add: []; addFromSession: [] }>()

// Every cell keeps its place in the one-column-per-field layout of a wide
// table and moves to a card layout when the table is narrow (a container
// query, so the slideover gets it too). Full class names: Tailwind scans them.
const cell = {
  handle: 'col-start-1 row-start-1 @min-[49rem]:col-start-auto @min-[49rem]:row-start-auto',
  name: 'col-start-2 row-start-1 @min-[49rem]:col-start-auto @min-[49rem]:row-start-auto',
  agent: 'col-start-3 row-start-1 @min-[49rem]:col-start-auto @min-[49rem]:row-start-auto',
  prompt: 'col-start-2 col-span-2 row-start-2 @min-[49rem]:col-start-auto @min-[49rem]:col-span-1 @min-[49rem]:row-start-auto',
  args: 'col-start-2 row-start-3 @min-[49rem]:col-start-auto @min-[49rem]:row-start-auto',
  start: 'col-start-3 row-start-3 @min-[49rem]:col-start-auto @min-[49rem]:row-start-auto',
  remove: 'col-start-4 row-start-1 @min-[49rem]:col-start-auto @min-[49rem]:row-start-auto',
}
const cols = 'grid-cols-[1rem_minmax(0,1fr)_minmax(0,1fr)_1.5rem] gap-x-2.5 gap-y-2 @min-[49rem]:grid-cols-[1rem_6.5rem_9.5rem_minmax(8rem,1fr)_7.5rem_8.5rem_1.5rem]'

const agentItems = computed(() => props.agents.map((a) => ({ label: a.name, value: a.id, icon: agentIcon(a.icon) })))

function agentOf(id: string): AgentInfo | undefined {
  return props.agents.find((a) => a.id === id)
}

/** The agent select's items for a member, with its agent listed even when the catalog no longer has it. */
function agentsFor(m: DraftMember) {
  if (!m.agentId || agentOf(m.agentId)) return agentItems.value
  return [...agentItems.value, { label: `${m.agentId} (not in the catalog)`, value: m.agentId, icon: 'i-lucide-circle-help' }]
}

/** A row's member in labels: its name, or its place while it has none. */
function who(m: DraftMember, i: number): string {
  return m.name || `member ${i + 1}`
}

function taken(m: DraftMember): string[] {
  return [...props.others, ...members.value.filter((x) => x.key !== m.key).map((x) => x.name)]
}

function nameError(m: DraftMember): string {
  return memberNameError(m.name, taken(m))
}

/** Immediately, after any other member with a usable name, or by hand. */
function startItems(m: DraftMember) {
  const names = taken(m).filter((n) => n && !memberNameError(n))
  const current = startValue(m.start)
  const items = [{ label: 'Immediately', value: 'immediately' }, ...names.map((n) => ({ label: `After ${n} idle`, value: `after:${n}` })), { label: 'Manual', value: 'manual' }]
  // A member it started after that is gone or renamed badly stays listed, so the select shows what is saved.
  if (!items.some((i) => i.value === current)) items.splice(items.length - 1, 0, { label: `After ${m.start.member} idle`, value: current })
  return items
}

function update(key: number, patch: Partial<DraftMember>) {
  members.value = members.value.map((m) => (m.key === key ? { ...m, ...patch } : m))
}

/** Renames a member; the members that start after it follow the new name. */
function rename(key: number, name: string) {
  const old = members.value.find((m) => m.key === key)?.name
  members.value = members.value.map((m) => {
    if (m.key === key) return { ...m, name }
    if (old && m.start.when === 'after' && m.start.member === old) return { ...m, start: { when: 'after', member: name } }
    return m
  })
}

function setAgent(key: number, agentId: string) {
  // An agent that takes no extra arguments is refused at launch with some: they go with the switch.
  const takesArgs = agentOf(agentId)?.allowArgs ?? true
  update(key, takesArgs ? { agentId } : { agentId, args: undefined, argsText: '' })
}

function setArgs(key: number, text: string) {
  update(key, { argsText: text, args: argsFrom(text) })
}

/** Removes a member; the members that started after it start immediately. */
function remove(key: number) {
  const gone = members.value.find((m) => m.key === key)?.name
  members.value = members.value
    .filter((m) => m.key !== key)
    .map((m) => (m.start.when === 'after' && m.start.member === gone ? { ...m, start: { when: 'immediately' } } : m))
}

// Role prompts stay one line until focused, then grow with their text.
const focusedPrompt = ref<number | null>(null)

// Reordering. A row is draggable only while its handle is held, so text in
// its fields can still be selected by dragging.
const armed = ref<number | null>(null)
const dragging = ref<number | null>(null)
const table = useTemplateRef<HTMLElement>('table')

function move(from: number, to: number) {
  if (from === to || from < 0 || to < 0 || to >= members.value.length) return
  const next = members.value.slice()
  const [m] = next.splice(from, 1)
  next.splice(to, 0, m!)
  members.value = next
}

function onDragStart(key: number, e: DragEvent) {
  if (armed.value !== key) {
    e.preventDefault()
    return
  }
  dragging.value = key
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(key))
  }
}

function onDragOver(index: number) {
  if (dragging.value === null) return
  move(
    members.value.findIndex((m) => m.key === dragging.value),
    index,
  )
}

function onDragEnd() {
  dragging.value = null
  armed.value = null
}

// Releasing the pointer anywhere but on the handle, or the browser cancelling
// it, disarms the row. A drag that has begun cancels the pointer itself, and
// dragend then disarms.
function disarm() {
  if (dragging.value === null) armed.value = null
}
onMounted(() => {
  document.addEventListener('pointerup', disarm)
  document.addEventListener('pointercancel', disarm)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerup', disarm)
  document.removeEventListener('pointercancel', disarm)
})

/** ↑ and ↓ on a handle move its row; the handle keeps the focus. */
async function moveBy(key: number, delta: number) {
  const from = members.value.findIndex((m) => m.key === key)
  move(from, from + delta)
  await nextTick()
  table.value?.querySelector<HTMLElement>(`[data-handle="${key}"]`)?.focus()
}
</script>

<template>
  <div ref="table" class="@container overflow-hidden rounded-lg ring ring-default" data-crew-members>
    <div class="hidden px-4 py-2.5 text-[11px] font-semibold uppercase tracking-wider text-muted bg-muted border-b border-default @min-[49rem]:grid" :class="cols" aria-hidden="true">
      <span />
      <span>Name</span>
      <span>Agent</span>
      <span>Role prompt <span class="font-normal normal-case tracking-normal">· $GOAL is the goal</span></span>
      <span>Extra args</span>
      <span>Starts</span>
      <span />
    </div>

    <div
      v-for="(m, i) in members"
      :key="m.key"
      class="grid items-start px-4 py-2.5 border-b border-default last:border-b-0"
      :class="[cols, dragging === m.key && 'bg-elevated/60']"
      :draggable="armed === m.key"
      data-crew-member
      @dragstart="onDragStart(m.key, $event)"
      @dragover.prevent="onDragOver(i)"
      @drop.prevent
      @dragend="onDragEnd"
    >
      <div :class="cell.handle" class="flex h-8 items-center">
        <button
          v-if="!single"
          type="button"
          class="cursor-grab text-dimmed hover:text-default rounded-xs outline-none focus-visible:ring-2 focus-visible:ring-primary"
          :data-handle="m.key"
          :aria-label="`Move ${who(m, i)}: drag, or press the up and down arrows`"
          @pointerdown="armed = m.key"
          @pointerup="armed = null"
          @pointercancel="disarm"
          @keydown.up.prevent="moveBy(m.key, -1)"
          @keydown.down.prevent="moveBy(m.key, 1)"
        >
          <UIcon name="i-lucide-grip-vertical" class="size-4 block" />
        </button>
      </div>

      <div :class="cell.name" class="flex min-w-0 flex-col gap-1">
        <UInput
          :model-value="m.name"
          placeholder="name"
          maxlength="40"
          autocapitalize="off"
          spellcheck="false"
          :color="nameError(m) ? 'error' : undefined"
          :highlight="!!nameError(m)"
          :aria-invalid="!!nameError(m)"
          :aria-label="`Name of ${who(m, i)}`"
          :ui="{ base: 'font-mono font-medium' }"
          class="w-full"
          @update:model-value="rename(m.key, String($event))"
        />
        <span v-if="nameError(m)" class="text-xs text-error leading-snug" data-name-error>{{ nameError(m) }}</span>
      </div>

      <USelect
        :class="cell.agent"
        :model-value="m.agentId"
        :items="agentsFor(m)"
        :icon="agentIcon(agentOf(m.agentId)?.icon)"
        placeholder="Agent"
        :aria-label="`Agent for ${who(m, i)}`"
        class="w-full"
        @update:model-value="setAgent(m.key, String($event))"
      />

      <UTextarea
        :class="cell.prompt"
        :model-value="m.prompt"
        :rows="focusedPrompt === m.key ? 3 : 1"
        :autoresize="focusedPrompt === m.key"
        :maxrows="12"
        maxlength="4000"
        :placeholder="agentOf(m.agentId)?.allowArgs === false ? 'Shell input (optional)' : 'Prompt; $GOAL is the goal'"
        :title="agentOf(m.agentId)?.allowArgs === false ? 'Typed into the shell as one line once it is ready (optional)' : 'What this agent does, typed into the agent as one line. $GOAL is the crew\'s goal.'"
        :aria-label="`Role prompt for ${who(m, i)}`"
        class="w-full"
        :ui="{ base: focusedPrompt === m.key ? 'resize-none' : 'resize-none overflow-hidden whitespace-nowrap text-ellipsis' }"
        @update:model-value="update(m.key, { prompt: String($event) })"
        @focus="focusedPrompt = m.key"
        @blur="focusedPrompt = focusedPrompt === m.key ? null : focusedPrompt"
      />

      <div :class="cell.args" class="min-w-0">
        <UTooltip :disabled="agentOf(m.agentId)?.allowArgs !== false" text="This agent takes no extra arguments">
          <div>
            <UInput
              :model-value="m.argsText"
              :placeholder="agentOf(m.agentId)?.allowArgs === false ? 'not taken' : 'none'"
              :disabled="agentOf(m.agentId)?.allowArgs === false"
              autocapitalize="off"
              spellcheck="false"
              :aria-label="`Extra arguments for ${who(m, i)}`"
              :ui="{ base: 'font-mono text-xs' }"
              class="w-full"
              @update:model-value="setArgs(m.key, String($event))"
            />
          </div>
        </UTooltip>
      </div>

      <USelect :class="cell.start" :model-value="startValue(m.start)" :items="startItems(m)" :aria-label="`When ${who(m, i)} starts`" class="w-full" @update:model-value="update(m.key, { start: startFrom(String($event)) })" />

      <div :class="cell.remove" class="flex h-8 items-center justify-end">
        <UTooltip v-if="!single" text="Remove">
          <UButton icon="i-lucide-x" color="neutral" variant="ghost" size="xs" :aria-label="`Remove ${who(m, i)}`" @click="remove(m.key)" />
        </UTooltip>
      </div>
    </div>

    <p v-if="!single && !members.length" class="px-4 py-4 text-sm text-muted">No members yet. A crew needs at least one to launch.</p>

    <div v-if="!single" class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2 border-t border-default">
      <UButton label="Add agent" icon="i-lucide-plus" color="secondary" variant="link" size="sm" class="px-0" :disabled="!agents.length || members.length >= 12" @click="emit('add')" />
      <UButton label="Add from a running session" icon="i-lucide-plus" color="secondary" variant="link" size="sm" class="px-0" :disabled="members.length >= 12" @click="emit('addFromSession')" />
      <span class="ml-auto text-xs text-muted">Prompts are typed into each agent as one line once it's ready</span>
    </div>
  </div>
</template>
