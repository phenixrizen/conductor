<script setup lang="ts">
import { blockSubtitle, runOpen, type RunBlock } from '~/utils/sidebar'

/**
 * The header of a run block (design 3b): the play icon (amber while a member needs you, as the amber dot is), the run's own name
 * or its crew's, and when it started with how many agents. It opens the run page. `open` marks the run of the page open now (the
 * run page, or a member's page), which the list scrolls to.
 */
const props = withDefaults(defineProps<{ block: RunBlock; needsDot?: boolean; open?: boolean }>(), { needsDot: true, open: false })
const route = useRoute()
const current = computed(() => runOpen(route.path, props.block.runId))
const amber = computed(() => props.block.state === 'needs' && props.needsDot)
</script>

<template>
  <NuxtLink
    :to="`/runs/${encodeURIComponent(block.runId)}`"
    class="flex items-center gap-2 rounded-md px-2.5 py-1.5 transition-colors"
    :class="current ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60'"
    :aria-current="current ? 'page' : undefined"
    :title="`${block.title} · ${blockSubtitle(block)}`"
    data-sidebar-run-group
    :data-sidebar-run-open="open || current ? '' : undefined"
  >
    <UIcon name="i-lucide-play" class="size-4 flex-none" :class="amber ? 'text-warning' : 'text-muted'" :data-run-play="amber ? 'needs' : undefined" />
    <span class="flex min-w-0 flex-1 flex-col">
      <span class="flex min-w-0 items-center gap-1.5">
        <span class="truncate text-sm font-semibold text-highlighted">{{ block.title }}</span>
        <slot name="pill" />
      </span>
      <span class="truncate font-mono text-[11px] text-muted">{{ blockSubtitle(block) }}</span>
    </span>
  </NuxtLink>
</template>
