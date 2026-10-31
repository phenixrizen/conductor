<script setup lang="ts">
// A ticket's page at /dispatch/<KEY>/<n> (docs/plans/dispatch.md, Owner decisions, 9): the ticket CND-12 at /dispatch/CND/12.
const route = useRoute()
const project = computed(() => String(route.params.project ?? '').toUpperCase())
const ticketKey = computed(() => `${project.value}-${String(route.params.number ?? '')}`)
</script>

<template>
  <DispatchTicketPage :project="project" :ticket-key="ticketKey" />
</template>
