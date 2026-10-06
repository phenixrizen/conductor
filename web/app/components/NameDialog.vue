<script setup lang="ts">
/** Your name, shown to others on a session: a label, not a login. Opened from your menu, and from More on a phone. */
const open = defineModel<boolean>('open', { default: false })
const identity = useIdentity()
const draft = ref('')
watch(open, (o) => {
  if (o) draft.value = identity.name.value
})
function save() {
  identity.set(draft.value)
  open.value = false
}
</script>

<template>
  <UModal v-model:open="open" title="Your name" description="Shown to others on a session. Defaults to the server's user; a label, not a login.">
    <template #body>
      <form class="flex flex-col gap-3" data-name-dialog @submit.prevent="save">
        <UInput v-model="draft" placeholder="Your name" aria-label="Your name" maxlength="40" autofocus />
        <UButton type="submit" label="Save" class="self-end" />
      </form>
    </template>
  </UModal>
</template>
