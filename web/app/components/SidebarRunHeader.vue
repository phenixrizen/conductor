<script setup lang="ts">
import { runOpen } from '~/utils/sidebar'

/** The header of a crew run's members in a sidebar section: the run's name, linking to its crew view. */
const props = defineProps<{ runId: string; label?: string }>()
const route = useRoute()
</script>

<template>
  <NuxtLink
    :to="`/runs/${encodeURIComponent(props.runId)}`"
    class="mt-1 flex items-center gap-1.5 rounded-sm px-2 py-0.5 text-[11.5px] font-semibold"
    :class="runOpen(route.path, props.runId) ? 'text-highlighted' : 'text-muted hover:text-highlighted'"
    :aria-current="runOpen(route.path, props.runId) ? 'page' : undefined"
    data-sidebar-run-group
  >
    <UIcon name="i-lucide-layout-grid" class="size-3.5 flex-none" />
    <span class="truncate">{{ props.label ?? props.runId }}</span>
  </NuxtLink>
</template>
