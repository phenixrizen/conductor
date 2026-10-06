<script setup lang="ts">
/** The alert settings: a browser notification and a chime when a session needs input, and for events routed to Browser. */
const alerts = useAttentionSettings()
</script>

<template>
  <div class="flex flex-col gap-3" data-alert-switches>
    <p class="text-xs text-muted">When a session needs input, and for events routed to Browser on the Events page:</p>
    <USwitch
      :model-value="alerts.settings.value.notifications"
      label="Browser notification"
      :description="alerts.permission.value === 'denied' ? 'Blocked by the browser' : undefined"
      :disabled="alerts.permission.value === 'denied' || alerts.permission.value === 'unsupported'"
      @update:model-value="alerts.setNotifications"
    />
    <USwitch :model-value="alerts.settings.value.chime" label="Chime" @update:model-value="alerts.setChime" />
    <p class="text-xs text-muted">The tab title and favicon always show the count.</p>
  </div>
</template>
