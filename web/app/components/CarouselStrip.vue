<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TerminalTransport } from '~/utils/transport/types'
import { dropIndex, keyMove, moveInOrder, type StripView } from '~/utils/carousel'

/**
 * The strip at the Roundhouse's foot: one chip per session in the kept
 * order, the current one marked, the rotation's progress under it. The
 * chips are rearranged by drag or by keyboard (#310): Space picks the
 * focused chip up, the arrows move it, Space or Enter drops it, Escape
 * puts it back. As thumbnails the chips carry a small live view of the
 * current session and its neighbours (the ones the Roundhouse connects
 * anyway) and the idle mark for the rest.
 */
const props = defineProps<{
  sessions: SessionInfo[]
  selected: number
  /** 0..1 of the current rotation interval that has elapsed. */
  progress: number
  /** Session id follow mode is holding on, if any. */
  holdingId?: string
  rotating: boolean
  /** A transport for a session's thumbnail, and whether its slide is one the Roundhouse keeps connected. */
  transportFor?: (s: SessionInfo) => () => TerminalTransport
  near?: (index: number) => boolean
}>()
const emit = defineEmits<{ select: [index: number]; reorder: [ids: string[]] }>()
const view = defineModel<StripView>('view', { default: 'chips' })

const ids = computed(() => props.sessions.map((s) => s.id))

function dot(s: SessionInfo) {
  if (s.attention?.state === 'needs_input') return 'bg-warning'
  if (s.status === 'running') return 'bg-success'
  return 'bg-neutral-400'
}

function bar(s: SessionInfo, i: number): { width: string; cls: string } {
  if (s.id === props.holdingId) return { width: '100%', cls: 'bg-warning' }
  if (i === props.selected && props.rotating) return { width: `${Math.round(props.progress * 100)}%`, cls: 'bg-primary' }
  return { width: '0%', cls: 'bg-primary' }
}

// Drag: the chip picked up, the place it would land, as HTML5 drag and drop reports them.
const dragFrom = ref<number | null>(null)
const dropAt = ref<number | null>(null)
function onDragStart(i: number, e: DragEvent) {
  dragFrom.value = i
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', ids.value[i] ?? '')
  }
}
function onDragOver(i: number, e: DragEvent) {
  if (dragFrom.value === null) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  dropAt.value = dropIndex(i, r.width > 0 ? (e.clientX - r.left) / r.width : 0, dragFrom.value)
}
function onDrop(e: DragEvent) {
  e.preventDefault()
  if (dragFrom.value !== null && dropAt.value !== null && dropAt.value !== dragFrom.value) emit('reorder', moveInOrder(ids.value, dragFrom.value, dropAt.value))
  dragFrom.value = null
  dropAt.value = null
}
function onDragEnd() {
  dragFrom.value = null
  dropAt.value = null
}

// Keyboard: the chip picked up with Space and the order it was picked from, for Escape.
const grabbed = ref<number | null>(null)
let pickedFrom: string[] = []
const announce = ref('')
const chips = new Map<string, HTMLElement>()
function chipRef(id: string) {
  return (el: unknown) => {
    if (el) chips.set(id, el as HTMLElement)
    else chips.delete(id)
  }
}
async function focusChip(id: string | undefined) {
  if (!id) return
  await nextTick()
  chips.get(id)?.focus()
}
function onKeyDown(i: number, e: KeyboardEvent) {
  if (e.altKey || e.ctrlKey || e.metaKey) return
  if (e.key === 'Enter' && grabbed.value === null) {
    e.preventDefault()
    emit('select', i)
    return
  }
  if (!(e.key === ' ' || e.key === 'Enter' || e.key === 'Escape' || e.key.startsWith('Arrow'))) return
  if (grabbed.value === null && e.key !== ' ') return
  // While a chip is picked up, the arrows move it and the page's own arrow keys (stepping) stay out of it.
  e.preventDefault()
  e.stopPropagation()
  const before = ids.value
  const r = keyMove(before, i, grabbed.value, e.key)
  const name = (idx: number) => props.sessions[idx]?.name ?? ''
  if (r.done === 'cancelled') {
    if (pickedFrom.join('\n') !== before.join('\n')) emit('reorder', pickedFrom)
    announce.value = `${name(i)} put back`
    void focusChip(pickedFrom[r.focused])
  } else if (r.done === 'dropped') {
    announce.value = `${name(i)} dropped at place ${r.focused + 1} of ${before.length}`
    void focusChip(before[r.focused])
  } else if (grabbed.value === null && r.grabbed !== null) {
    pickedFrom = [...before]
    announce.value = `${name(i)} picked up. The arrows move it, Space drops it, Escape puts it back`
  } else if (r.ids.join('\n') !== before.join('\n')) {
    emit('reorder', r.ids)
    announce.value = `${name(i)} moved to place ${r.focused + 1} of ${before.length}`
    void focusChip(r.ids[r.focused])
  }
  grabbed.value = r.grabbed
}
function onKeyUp(e: KeyboardEvent) {
  if (e.key === ' ') e.preventDefault()
}
/** A chip the keyboard moved keeps its grab across the re-render; one that left the list drops it. */
watch(ids, (now) => {
  if (grabbed.value !== null && grabbed.value >= now.length) grabbed.value = null
})
</script>

<template>
  <div class="flex items-stretch gap-2.5" data-carousel-strip>
    <div class="grid min-w-0 flex-1 gap-2.5" :style="{ gridTemplateColumns: `repeat(${Math.max(sessions.length, 1)}, minmax(0, 1fr))` }">
      <div
        v-for="(s, i) in sessions"
        :key="s.id"
        :ref="chipRef(s.id)"
        role="button"
        tabindex="0"
        draggable="true"
        class="relative flex cursor-grab flex-col overflow-hidden rounded-md border bg-default text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-primary"
        :class="[
          i === selected ? 'border-primary ring-1 ring-primary' : 'border-default hover:border-accented',
          grabbed === i ? 'cursor-grabbing opacity-80 ring-2 ring-primary' : '',
          dragFrom === i ? 'opacity-50' : '',
        ]"
        :aria-current="i === selected ? 'true' : undefined"
        :aria-grabbed="grabbed === i ? 'true' : 'false'"
        :aria-label="`${s.name}, place ${i + 1} of ${sessions.length}`"
        :data-strip-chip="s.id"
        :data-strip-grabbed="grabbed === i ? '' : undefined"
        @click="emit('select', i)"
        @keydown="onKeyDown(i, $event)"
        @keyup="onKeyUp"
        @dragstart="onDragStart(i, $event)"
        @dragover="onDragOver(i, $event)"
        @drop="onDrop"
        @dragend="onDragEnd"
      >
        <!-- Where a dragged chip would land: a bar at this chip's start, or at the last chip's end. -->
        <span v-if="dragFrom !== null && dropAt !== null && (dropAt === i || (dropAt === sessions.length && i === sessions.length - 1))" class="pointer-events-none absolute inset-y-0 w-0.5 bg-primary" :class="dropAt === i && !(dropAt === sessions.length) ? 'left-0' : 'right-0'" aria-hidden="true" />
        <div class="flex items-center gap-1.5 px-2 py-1.5">
          <SessionAvatar :agent-id="s.agentId" :solid="i === selected" />
          <span class="truncate text-xs font-semibold">{{ s.name }}</span>
          <span class="ml-auto size-1.5 flex-none rounded-full" :class="dot(s)" aria-hidden="true" />
        </div>
        <!-- A thumbnail: a live view for a slide the Roundhouse connects, the idle mark for the rest; never a connection of its own. -->
        <div v-if="view === 'thumbs'" class="pointer-events-none relative h-20 overflow-hidden border-t border-default bg-default" data-strip-thumb>
          <TerminalView v-if="transportFor && near?.(i)" :key="s.id" :create-transport="transportFor(s)" fit="scale" read-only :auto-focus="false" compact class="absolute inset-0" />
          <div v-else class="flex h-full items-center justify-center gap-1.5 text-[11px] text-muted" data-strip-idle>
            <SessionAvatar :agent-id="s.agentId" />
            <span>{{ s.attention?.state === 'needs_input' ? 'needs you' : s.status === 'running' ? 'running' : s.status }}</span>
          </div>
        </div>
        <div class="h-[3px] w-full bg-elevated">
          <div class="h-[3px] transition-[width] duration-200 ease-linear" :class="bar(s, i).cls" :style="{ width: bar(s, i).width }" />
        </div>
      </div>
    </div>
    <UTooltip :text="view === 'thumbs' ? 'Show the strip as chips' : 'Show the strip as thumbnails'" :content="{ side: 'left' }">
      <UButton :icon="view === 'thumbs' ? 'i-lucide-rows-2' : 'i-lucide-images'" size="xs" color="neutral" variant="ghost" class="self-start" :aria-label="view === 'thumbs' ? 'Show the strip as chips' : 'Show the strip as thumbnails'" :data-strip-view="view" @click="view = view === 'thumbs' ? 'chips' : 'thumbs'" />
    </UTooltip>
    <span class="sr-only" aria-live="polite" data-strip-announce>{{ announce }}</span>
  </div>
</template>
