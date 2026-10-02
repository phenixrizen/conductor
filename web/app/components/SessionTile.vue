<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TerminalTransport } from '~/utils/transport/types'
import { isEnded } from '~/utils/attention'
import { agentInitials } from '~/utils/sessions'
import { TILE_FONT_SIZE } from '~/utils/tile'

/**
 * A session as a live tile: its terminal fits the tile and takes keys in place (a click on it focuses it); the full view is one action
 * away: the open button, a double-click on the header, or Enter or Space on the tile's frame when the frame itself has the focus.
 */
const props = defineProps<{
  session: SessionInfo
  createTransport: () => TerminalTransport
}>()

const emit = defineEmits<{ select: [] }>()

const events = useEvents()
// An ended session may still carry the prompt it was waiting on: it waits no more (as in the sidebar).
const needsInput = computed(() => props.session.attention?.state === 'needs_input' && !isEnded(props.session.status))
const status = computed(() => {
  // The amber dot follows the Events page's Badge route for needs_input; the label stays.
  if (needsInput.value) return { label: 'Needs input', cls: 'text-warning', dot: events.routes.value.needs_input.badge ? 'bg-warning' : '' }
  if (props.session.status === 'running') return { label: 'Running', cls: 'text-success', dot: 'bg-success' }
  return { label: props.session.status.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
})
const host = computed(() => (props.session.kind === 'hosted' ? `hosted · ${props.session.hostName || 'dev machine'}` : 'server'))
</script>

<template>
  <!-- The page's own control (the crew view's selection box) sits beside the tile, not in it: one control per element. -->
  <div class="relative h-full min-h-0 min-w-0">
    <div v-if="$slots.leading" class="absolute left-[11px] top-2 z-10 flex">
      <slot name="leading" />
    </div>
    <div
      class="group flex h-full min-h-0 flex-col overflow-hidden rounded-lg border bg-elevated/40 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-primary"
      :class="needsInput ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented focus-within:border-accented'"
      role="group"
      tabindex="0"
      data-session-tile
      :data-session-id="props.session.id"
      :aria-label="`${props.session.name}: a live terminal. Enter opens it in full.`"
      @keydown.enter.self.prevent="emit('select')"
      @keydown.space.self.prevent="emit('select')"
    >
      <div class="flex cursor-pointer items-center gap-2 border-b border-default px-2.5 py-1 text-xs shrink-0 select-none" :class="$slots.leading && 'pl-8'" data-tile-header @dblclick="emit('select')">
        <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(props.session.agentId) }}</span>
        <span class="font-semibold truncate flex-1 text-[13px]">{{ props.session.name }}</span>
        <EventMarkBadge :session-id="props.session.id" />
        <YoloBadge v-if="props.session.yolo" icon />
        <span class="flex items-center gap-1.5 text-[11.5px]" :class="status.cls"><span v-if="status.dot" class="size-[7px] rounded-full" :class="status.dot" aria-hidden="true" />{{ status.label }}</span>
        <ResumeButton v-if="isEnded(props.session.status)" :session="props.session" stay icon-only size="xs" />
        <UTooltip text="Open in full (or double-click here)">
          <UButton icon="i-lucide-maximize-2" size="xs" color="neutral" variant="ghost" :aria-label="`Open ${props.session.name}`" data-tile-open @click.stop="emit('select')" />
        </UTooltip>
      </div>
      <div class="flex-1 min-h-0" data-tile-terminal>
        <TerminalView :create-transport="props.createTransport" fit="tile" compact :auto-focus="false" :font-size="TILE_FONT_SIZE" :scrollback="0" />
      </div>
      <div class="flex items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted shrink-0">
        <slot name="footer">
          <span class="truncate">{{ host }}</span>
          <span class="ml-auto flex-none">{{ props.session.viewers ? `${props.session.viewers} here` : '—' }}</span>
        </slot>
      </div>
    </div>
  </div>
</template>
