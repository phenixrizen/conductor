<script setup lang="ts">
/**
 * How many chat messages wait unread (design 2b, 2g): a neutral pill with a
 * speech bubble, never a status colour, so a glance at the colours still reads
 * the state alone. Nothing when none wait.
 */
const props = defineProps<{ count?: number; sessionId?: string; runId?: string }>()
const unread = useChatUnread()
const n = computed(() => props.count ?? (props.sessionId ? unread.count(`session:${props.sessionId}`) : props.runId ? unread.count(`run:${props.runId}`) : 0))
</script>

<template>
  <UBadge v-if="n > 0" color="neutral" variant="subtle" size="sm" icon="i-lucide-message-circle" :label="String(n)" :title="`${n} unread in chat`" :data-chat-unread="n" />
</template>
