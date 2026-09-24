<script setup lang="ts">
import type { Attention } from '~/utils/protocol'

const props = defineProps<{ attention?: Attention; size?: 'sm' | 'md' }>()

const label = computed(() => {
  switch (props.attention?.state) {
    case 'needs_input':
      return 'needs input'
    case 'working':
      return 'working'
    case 'done':
      return 'done'
  }
  return ''
})

const color = computed(() => (props.attention?.state === 'needs_input' ? 'secondary' : props.attention?.state === 'done' ? 'success' : 'neutral'))
const icon = computed(() => (props.attention?.state === 'needs_input' ? 'i-lucide-hand' : props.attention?.state === 'done' ? 'i-lucide-check' : 'i-lucide-loader-circle'))

const tooltip = computed(() => {
  const a = props.attention
  if (!a?.state) return ''
  const parts = [a.message || label.value]
  if (a.source) parts.push(`via ${a.source}`)
  if (a.since) parts.push(new Date(a.since).toLocaleTimeString())
  return parts.join(' · ')
})
</script>

<template>
  <UTooltip v-if="label" :text="tooltip">
    <UChip :show="attention?.state === 'needs_input'" color="secondary" inset>
      <UBadge :label="label" :icon="icon" :color="color" :variant="attention?.state === 'needs_input' ? 'solid' : 'subtle'" :size="size || 'sm'" :class="{ 'animate-pulse': attention?.state === 'needs_input' }" />
    </UChip>
  </UTooltip>
</template>
