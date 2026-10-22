<script setup lang="ts">
import { agentInitials } from '~/utils/sessions'

/** `label`, when given, replaces the agent's initials: the rail tells two squares of one agent apart with it (#314). */
const props = withDefaults(defineProps<{ agentId: string; size?: 'sm' | 'md'; solid?: boolean; dashed?: boolean; label?: string }>(), { size: 'sm', solid: false, dashed: false, label: undefined })
const label = computed(() => props.label || agentInitials(props.agentId))
</script>

<template>
  <span
    class="grid place-items-center rounded-md font-mono font-semibold flex-none select-none"
    :class="[
      size === 'sm' ? 'size-6 text-[10px]' : 'size-7 text-[10.5px]',
      dashed ? 'border border-dashed border-accented text-muted' : solid ? 'bg-primary text-inverted' : 'bg-elevated text-primary',
    ]"
    :title="agentId"
    :data-avatar-label="label"
  >{{ label }}</span>
</template>
