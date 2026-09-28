<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'

const props = defineProps<{
  sessions: SessionInfo[]
  selected: number
  /** 0..1 of the current rotation interval that has elapsed. */
  progress: number
  /** Session id follow mode is holding on, if any. */
  holdingId?: string
  rotating: boolean
}>()
const emit = defineEmits<{ select: [index: number] }>()

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
</script>

<template>
  <div class="grid gap-2.5" :style="{ gridTemplateColumns: `repeat(${Math.max(sessions.length, 1)}, minmax(0, 1fr))` }" data-carousel-strip>
    <button
      v-for="(s, i) in sessions"
      :key="s.id"
      type="button"
      class="flex flex-col overflow-hidden rounded-md border bg-default text-left transition-colors"
      :class="i === selected ? 'border-primary ring-1 ring-primary' : 'border-default hover:border-accented'"
      :aria-current="i === selected ? 'true' : undefined"
      @click="emit('select', i)"
    >
      <div class="flex items-center gap-1.5 px-2 py-1.5">
        <SessionAvatar :agent-id="s.agentId" :solid="i === selected" />
        <span class="truncate text-xs font-semibold">{{ s.name }}</span>
        <span class="ml-auto size-1.5 rounded-full flex-none" :class="dot(s)" aria-hidden="true" />
      </div>
      <div class="h-[3px] w-full bg-elevated">
        <div class="h-[3px] transition-[width] duration-200 ease-linear" :class="bar(s, i).cls" :style="{ width: bar(s, i).width }" />
      </div>
    </button>
  </div>
</template>
