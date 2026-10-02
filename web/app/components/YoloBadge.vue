<script setup lang="ts">
/**
 * "yolo": the session was launched with its agent's yolo recipe, so the agent skips its permission prompts. It says Conductor applied the
 * recipe, not that no dialog can appear. `icon` shows the icon alone, for a sidebar row.
 */
withDefaults(defineProps<{ icon?: boolean }>(), { icon: false })
</script>

<template>
  <UTooltip text="Launched with its agent's yolo recipe: it skips its permission prompts">
    <UIcon v-if="icon" name="i-lucide-shield-off" class="size-3.5 flex-none text-warning" aria-label="yolo" data-yolo-badge />
    <UBadge v-else label="yolo" icon="i-lucide-shield-off" color="warning" variant="subtle" size="sm" class="flex-none" data-yolo-badge />
  </UTooltip>
</template>
