<script setup lang="ts">
import type { FeedEntry } from '~/utils/events'
import { activityBuckets, activityTotal, enough, roleColors } from '~/utils/charts'
import { tickLabel } from '~/utils/timeline'

/**
 * Activity per minute over the last hour, stacked by what it was: attention
 * changes, the agents' reports (progress, artifacts), handoffs, tool calls
 * and errors, from the live feed as the Events page has it. Nothing is drawn
 * before two events fall in the hour.
 */
const props = defineProps<{ entries: FeedEntry[]; now: number }>()

const colorMode = useColorMode()
const colors = ref(roleColors())
watch(() => colorMode.value, () => nextTick(() => (colors.value = roleColors())))

const buckets = computed(() => activityBuckets(props.entries, props.now, 60))
const total = computed(() => activityTotal(buckets.value))
const categories = computed(() => ({
  attention: { name: 'attention', color: colors.value.warning },
  reports: { name: 'reports', color: colors.value.success },
  handoff: { name: 'handoffs', color: colors.value.info },
  tool: { name: 'tools', color: colors.value.neutral },
  error: { name: 'errors', color: colors.value.error },
}))
const xFormatter = (i: number) => {
  const b = buckets.value[i]
  return b ? tickLabel(b.minute, 3_600_000) : ''
}
const yFormatter = (v: number) => (Number.isInteger(v) ? String(v) : '')
</script>

<template>
  <section class="flex flex-col gap-2 rounded-md border border-default p-3" aria-labelledby="activity-chart-title" data-activity-chart :data-total="total">
    <div class="flex items-center gap-2">
      <h3 id="activity-chart-title" class="text-[11px] font-semibold uppercase tracking-wider text-muted">Last hour</h3>
      <span class="text-[11px] text-muted">per minute</span>
    </div>
    <p v-if="!enough(total)" class="py-4 text-center text-sm text-muted" data-activity-chart-empty>Activity per minute appears once two events arrived in the last hour.</p>
    <AreaChart v-else :data="buckets" :height="160" :categories="categories" stacked :x-formatter="xFormatter" :y-formatter="yFormatter" :x-num-ticks="6" :y-num-ticks="3" :line-width="1" />
  </section>
</template>
