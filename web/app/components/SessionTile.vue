<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TerminalTransport } from '~/utils/transport/types'

const props = defineProps<{
  session: SessionInfo
  createTransport: () => TerminalTransport
}>()

const emit = defineEmits<{ select: [] }>()

const needsInput = computed(() => props.session.attention?.state === 'needs_input')
</script>

<template>
  <div
    class="group flex h-full min-h-0 flex-col overflow-hidden rounded-lg border bg-elevated/40 transition-colors cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-primary"
    :class="needsInput ? 'border-secondary ring-2 ring-secondary/60' : 'border-default hover:border-accented'"
    role="button"
    tabindex="0"
    data-session-tile
    :aria-label="`Open ${props.session.name}`"
    @click="emit('select')"
    @keydown.enter.prevent="emit('select')"
    @keydown.space.prevent="emit('select')"
  >
    <div class="flex items-center gap-2 px-2 py-1 text-xs shrink-0">
      <UIcon :name="props.session.kind === 'hosted' ? 'i-lucide-laptop' : 'i-lucide-server'" class="size-3.5 text-muted flex-none" />
      <span class="font-medium truncate flex-1">{{ props.session.name }}</span>
      <AttentionBadge :attention="props.session.attention" />
      <SessionStatusBadge :status="props.session.status" :exit-code="props.session.exitCode" />
    </div>
    <div class="flex-1 min-h-0 pointer-events-none">
      <TerminalView :create-transport="props.createTransport" read-only fit="scale" compact :auto-focus="false" />
    </div>
  </div>
</template>
