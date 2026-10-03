<script setup lang="ts">
import type { RunInfo } from '~/composables/useSessions'
import { enough, roleColors, runBars, runBarSeries } from '~/utils/charts'

/**
 * A crew's last runs, up to twenty, as bars: the height is how long each
 * ran, the colour its outcome (finished, stopped, still running), with how
 * many members waited for input in all beside. The runs are the live ones
 * the store holds and the records the server kept of those that ended
 * (GET /api/crews/{id}/runs), read when the card mounts and again, a moment
 * later, whenever the live runs change. Nothing is drawn before two runs.
 */
const props = defineProps<{ crewId: string; live: RunInfo[]; now: number }>()

const api = useSessions()
const colorMode = useColorMode()
const recorded = ref<RunInfo[]>([])
const error = ref('')
let timer: number | undefined
let seq = 0

async function read() {
  const n = ++seq
  try {
    const r = await api.crewRuns(props.crewId)
    if (n !== seq) return
    recorded.value = r.runs.slice(r.live)
    error.value = ''
  } catch (e) {
    if (n === seq) error.value = (e as Error).message
  }
}
onMounted(read)
onBeforeUnmount(() => window.clearTimeout(timer))
watch(
  () => props.live.map((r) => `${r.id}:${r.state}:${r.stoppedAt ?? ''}`).join(','),
  () => {
    window.clearTimeout(timer)
    timer = window.setTimeout(read, 1000)
  },
)

/** Live first (newest first, as the store lists them), then the records not among them. */
const runs = computed(() => {
  const live = [...props.live].sort((a, b) => b.startedAt.localeCompare(a.startedAt))
  const ids = new Set(live.map((r) => r.id))
  return [...live, ...recorded.value.filter((r) => !ids.has(r.id))]
})
const bars = computed(() => runBars(runs.value, props.now))
const series = computed(() => runBarSeries(bars.value))
const needs = computed(() => bars.value.reduce((n, b) => n + b.needsInput, 0))
const errors = computed(() => bars.value.reduce((n, b) => n + b.errors, 0))

const colors = ref(roleColors())
watch(() => colorMode.value, () => nextTick(() => (colors.value = roleColors())))
const categories = computed(() => ({
  finished: { name: 'finished', color: colors.value.success },
  stopped: { name: 'stopped', color: colors.value.neutral },
  running: { name: 'running', color: colors.value.primary },
}))
const xFormatter = (i: number) => series.value[i]?.label ?? ''
const yFormatter = (v: number) => `${v}m`
const tooltipTitle = (d: (typeof series.value)[number]) => `${d.label} · ${d.id}`
</script>

<template>
  <div class="flex flex-col gap-1" data-runs-chart :data-runs="bars.length">
    <p v-if="!enough(bars.length)" class="text-[11px] text-muted" data-runs-chart-empty>{{ bars.length ? 'A chart of the last runs appears after two.' : 'No runs yet.' }}</p>
    <template v-else>
      <div class="flex items-center gap-2 text-[11px] text-muted">
        <span>last {{ bars.length }} runs, minutes</span>
        <UBadge v-if="needs" :label="`needed input ×${needs}`" color="warning" variant="subtle" size="sm" data-runs-needs />
        <UBadge v-if="errors" :label="`errors ×${errors}`" color="error" variant="subtle" size="sm" />
        <span v-if="error" class="truncate text-warning" :title="error">records unavailable</span>
      </div>
      <BarChart
        :data="series"
        :height="72"
        :categories="categories"
        :y-axis="['finished', 'stopped', 'running']"
        stacked
        hide-legend
        :radius="2"
        :x-formatter="xFormatter"
        :y-formatter="yFormatter"
        :y-num-ticks="2"
        :x-num-ticks="Math.min(4, series.length)"
        :tooltip-title-formatter="tooltipTitle"
        :bar-padding="0.15"
      />
    </template>
  </div>
</template>
