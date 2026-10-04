<script setup lang="ts">
/** The header of the sessions another machine hosts here (conductor host): a laptop and the machine's name. No page behind it. */
defineProps<{ host: string; label: string }>()
</script>

<template>
  <div class="mt-1 flex items-center gap-1.5 px-2 py-0.5 text-[11.5px] font-semibold text-muted" :title="`Hosted on ${label}`" :data-sidebar-host-group="host">
    <UIcon name="i-lucide-laptop" class="size-3.5 flex-none" />
    <span class="truncate">{{ label }}</span>
  </div>
</template>
