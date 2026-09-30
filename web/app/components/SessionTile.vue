<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TerminalTransport } from '~/utils/transport/types'
import { isEnded } from '~/utils/attention'
import { agentInitials } from '~/utils/sessions'

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
  <div
    class="group flex h-full min-h-0 flex-col overflow-hidden rounded-lg border bg-elevated/40 transition-colors cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-primary"
    :class="needsInput ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented'"
    role="button"
    tabindex="0"
    data-session-tile
    :aria-label="`Open ${props.session.name}`"
    @click="emit('select')"
    @keydown.enter.prevent="emit('select')"
    @keydown.space.prevent="emit('select')"
  >
    <div class="flex items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs shrink-0">
      <!-- A control of the page's own (the crew view's selection box); its clicks and keys stay off the tile. -->
      <span v-if="$slots.leading" class="flex flex-none" @click.stop @keydown.stop><slot name="leading" /></span>
      <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(props.session.agentId) }}</span>
      <span class="font-semibold truncate flex-1 text-[13px]">{{ props.session.name }}</span>
      <EventMarkBadge :session-id="props.session.id" />
      <span class="flex items-center gap-1.5 text-[11.5px]" :class="status.cls"><span v-if="status.dot" class="size-[7px] rounded-full" :class="status.dot" aria-hidden="true" />{{ status.label }}</span>
    </div>
    <div class="flex-1 min-h-0 pointer-events-none">
      <TerminalView :create-transport="props.createTransport" read-only fit="scale" compact :auto-focus="false" />
    </div>
    <div class="flex items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted shrink-0">
      <slot name="footer">
        <span class="truncate">{{ host }}</span>
        <span class="ml-auto flex-none">{{ props.session.viewers ? `${props.session.viewers} here` : '—' }}</span>
      </slot>
    </div>
  </div>
</template>
