<script setup lang="ts">
const sidebar = useSidebar()
</script>

<template>
  <UTooltip v-if="sidebar.hidden.value" text="Show sidebar" :kbds="['meta', 'B']">
    <UButton icon="i-lucide-panel-left-open" color="neutral" variant="ghost" aria-label="Show sidebar" class="hidden lg:inline-flex" @click="sidebar.show()" />
  </UTooltip>
</template>
