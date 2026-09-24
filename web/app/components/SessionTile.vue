<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TerminalTransport } from '~/utils/transport/types'

const props = defineProps<{
  session: SessionInfo
  active?: boolean
  createTransport: () => TerminalTransport
}>()

const emit = defineEmits<{ select: []; open: [] }>()
</script>

<template>
  <div
    class="group flex flex-col overflow-hidden rounded-lg border bg-elevated/40 transition-colors cursor-pointer"
    :class="[
      props.active ? 'border-primary ring-2 ring-primary/40' : 'border-default hover:border-accented',
      props.session.attention?.state === 'needs_input' ? 'ring-2 ring-secondary/60 border-secondary' : '',
    ]"
    role="button"
    tabindex="0"
    :aria-label="`Show ${props.session.name} in the spotlight`"
    @click="emit('select')"
    @dblclick="emit('open')"
    @keydown.enter="emit('select')"
  >
    <div class="flex items-center gap-2 px-2 py-1 text-xs">
      <UIcon :name="props.session.kind === 'hosted' ? 'i-lucide-laptop' : 'i-lucide-server'" class="size-3.5 text-muted flex-none" />
      <span class="font-medium truncate flex-1">{{ props.session.name }}</span>
      <AttentionBadge :attention="props.session.attention" />
      <SessionStatusBadge :status="props.session.status" :exit-code="props.session.exitCode" />
      <UButton icon="i-lucide-external-link" size="xs" color="neutral" variant="ghost" aria-label="Open session" class="opacity-0 group-hover:opacity-100" @click.stop="emit('open')" />
    </div>
    <div class="h-40 pointer-events-none">
      <TerminalView :create-transport="props.createTransport" read-only :font-size="9" :scrollback="200" compact />
    </div>
  </div>
</template>
