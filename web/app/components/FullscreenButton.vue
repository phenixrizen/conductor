<script setup lang="ts">
/** The fullscreen toggle of every page header; the layout listens for the change and registers the F key. */
withDefaults(defineProps<{ size?: 'sm' | 'md' }>(), { size: 'md' })
const fs = useFullscreenToggle()
</script>

<template>
  <UTooltip text="Toggle fullscreen" :kbds="['F']">
    <UButton :icon="fs.fullscreen.value ? 'i-lucide-minimize' : 'i-lucide-maximize'" color="neutral" variant="ghost" :size="size" :aria-label="fs.fullscreen.value ? 'Exit fullscreen' : 'Enter fullscreen'" data-fullscreen @click="fs.toggle" />
  </UTooltip>
</template>
