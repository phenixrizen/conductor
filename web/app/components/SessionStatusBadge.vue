<script setup lang="ts">
import { statusColor, statusLabel, type SessionStatus } from '~/composables/useSessions'

const props = defineProps<{ status: SessionStatus; exitCode?: number }>()
const label = computed(() => (props.status === 'exited' && props.exitCode !== undefined ? `exited (${props.exitCode})` : statusLabel(props.status)))
</script>

<template>
  <UBadge :label="label" :color="statusColor(status)" variant="subtle" size="sm" />
</template>
