<script setup lang="ts">
const attention = useAttention()
const admin = useAdminToken()
useAttentionHead()

onMounted(() => {
  if (admin.hasToken.value) attention.start()
})
watch(
  () => admin.hasToken.value,
  (has) => {
    if (has) attention.start()
  },
)
</script>

<template>
  <UApp>
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </UApp>
</template>
