<script setup lang="ts">
const open = defineModel<boolean>('open', { default: false })
const admin = useAdminToken()
const draft = ref('')

watch(open, (v) => {
  if (v) draft.value = admin.token.value
})
// A 401 anywhere opens the gate.
watch(
  () => admin.needsToken.value,
  (v) => {
    if (v) open.value = true
  },
)

function save() {
  admin.set(draft.value)
  open.value = false
}
</script>

<template>
  <UModal v-model:open="open" title="Admin token" description="The token from CONDUCTOR_ADMIN_TOKEN (or the one printed at server start). It is kept in this browser only.">
    <template #body>
      <form class="flex flex-col gap-3" @submit.prevent="save">
        <UFormField label="Token" name="token">
          <UInput v-model="draft" type="password" autocomplete="off" placeholder="paste the admin token" class="w-full" autofocus />
        </UFormField>
        <div class="flex justify-end gap-2">
          <UButton label="Cancel" color="neutral" variant="ghost" @click="open = false" />
          <UButton label="Save" type="submit" :disabled="!draft.trim()" />
        </div>
      </form>
    </template>
  </UModal>
</template>
