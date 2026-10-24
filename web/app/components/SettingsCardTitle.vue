<script setup lang="ts">
/** A settings card's header: its title, and one line saying what the card is for (#366). */
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="flex flex-col gap-0.5">
    <h2 class="text-sm font-semibold text-highlighted">{{ title }}</h2>
    <p v-if="description" class="text-xs text-muted">{{ description }}</p>
  </div>
</template>
