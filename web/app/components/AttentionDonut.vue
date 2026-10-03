<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { isActive } from '~/utils/attention'
import { attentionSlices, enough, roleColors } from '~/utils/charts'

/**
 * The Wall's sessions by attention state, as a small donut in its header:
 * needs input (amber), working (green), done (idle, blue-grey) and no state
 * yet. Drawn only with two sessions or more; the header's counts say the
 * rest.
 */
const props = defineProps<{ sessions: SessionInfo[] }>()

const colorMode = useColorMode()
const colors = ref(roleColors())
watch(() => colorMode.value, () => nextTick(() => (colors.value = roleColors())))

const slices = computed(() => attentionSlices(props.sessions, isActive))
const total = computed(() => slices.value.needs_input + slices.value.working + slices.value.done + slices.value.idle)
const data = computed(() => [slices.value.needs_input, slices.value.working, slices.value.done, slices.value.idle])
const categories = computed(() => ({
  needs_input: { name: 'needs input', color: colors.value.warning },
  working: { name: 'working', color: colors.value.success },
  done: { name: 'done', color: colors.value.info },
  idle: { name: 'no state yet', color: colors.value.neutral },
}))
const label = computed(() => `${total.value} sessions: ${slices.value.needs_input} need input, ${slices.value.working} working, ${slices.value.done} done, ${slices.value.idle} with no state yet`)
</script>

<template>
  <UTooltip v-if="enough(total)" :text="label">
    <div class="size-8 flex-none" role="img" :aria-label="label" data-attention-donut :data-total="total">
      <DonutChart :data="data" :categories="categories" :height="32" :radius="4" :arc-width="5" hide-legend hide-tooltip />
    </div>
  </UTooltip>
</template>
