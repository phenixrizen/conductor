<script setup lang="ts">
/** The join page's frame: a panel beside the sidebar inside the workbench, the plain full-height column for a guest. */
defineProps<{ workbench: boolean }>()
</script>

<template>
  <UDashboardPanel v-if="workbench" id="join" :ui="{ body: 'p-0 sm:p-0 gap-0 flex flex-col min-h-0' }" data-join-frame="workbench">
    <template #body>
      <slot />
    </template>
  </UDashboardPanel>
  <div v-else class="flex h-full min-h-0 flex-1 flex-col" data-join-frame="bare">
    <slot />
  </div>
</template>
