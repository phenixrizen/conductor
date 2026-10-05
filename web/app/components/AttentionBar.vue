<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { isActive } from '~/utils/attention'
import { attentionSlices, enough, type AttentionSlice } from '~/utils/charts'

/**
 * The Yard's sessions by attention state, as one slim stacked bar in its
 * header: needs input (amber), working (green), done (blue) and no state yet
 * (grey), each as wide as its share. The header's buttons carry the counts;
 * the bar is the proportion. Drawn only with two sessions or more.
 */
const props = defineProps<{ sessions: SessionInfo[] }>()

const slices = computed(() => attentionSlices(props.sessions, isActive))
const total = computed(() => slices.value.needs_input + slices.value.working + slices.value.done + slices.value.idle)
const parts: Array<{ key: AttentionSlice; cls: string }> = [
  { key: 'needs_input', cls: 'bg-warning' },
  { key: 'working', cls: 'bg-success' },
  { key: 'done', cls: 'bg-info' },
  { key: 'idle', cls: 'bg-neutral-400' },
]
const label = computed(() => `${total.value} sessions: ${slices.value.needs_input} need input, ${slices.value.working} working, ${slices.value.done} done, ${slices.value.idle} with no state yet`)
</script>

<template>
  <UTooltip v-if="enough(total)" :text="label">
    <div class="flex h-1.5 w-24 flex-none overflow-hidden rounded-full bg-elevated" role="img" :aria-label="label" data-attention-bar :data-total="total">
      <span v-for="p in parts" :key="p.key" :class="p.cls" :style="{ width: `${(slices[p.key] / total) * 100}%` }" :data-attention-slice="p.key" :data-count="slices[p.key]" />
    </div>
  </UTooltip>
</template>
