<script setup lang="ts">
/** The fullscreen toggle of every page header; the layout listens for the change and registers the F key. */
withDefaults(defineProps<{ size?: 'sm' | 'md' }>(), { size: 'md' })
const fs = useFullscreenToggle()
// Read once: the app renders in the browser only (ssr: false). A browser without the API (an iPhone) gets no button.
const supported = import.meta.client && document.fullscreenEnabled === true
</script>

<template>
  <UTooltip v-if="supported" text="Toggle fullscreen" :kbds="['F']">
    <UButton :icon="fs.fullscreen.value ? 'i-lucide-minimize' : 'i-lucide-maximize'" color="neutral" variant="ghost" :size="size" :aria-label="fs.fullscreen.value ? 'Exit fullscreen' : 'Enter fullscreen'" data-fullscreen @click="fs.toggle" />
  </UTooltip>
</template>
