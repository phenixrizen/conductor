<script setup lang="ts">
// The join page's layout: fullscreen as under the default layout, the F key and the change listener once.
const fs = useFullscreenToggle()
fs.listen()
fs.shortcuts()
</script>

<template>
  <div class="h-full flex flex-col bg-default">
    <slot />
  </div>
</template>
