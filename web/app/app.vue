<script setup lang="ts">
useHead({ titleTemplate: (t) => (t ? `${t} · Conductor` : 'Conductor') })
</script>

<template>
  <UApp>
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </UApp>
</template>
