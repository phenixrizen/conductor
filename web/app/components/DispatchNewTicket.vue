<script setup lang="ts">
import type { DispatchTicket, TicketReply, TicketStatus } from '~/composables/useDispatch'
import type { DispatchOrder } from '~/utils/dispatchOrder'
import { PRIORITIES, TICKET_STATUSES, statusWord } from '~/utils/dispatch'
import { dispatchFailureWords } from '~/composables/useDispatchOps'

/**
 * New ticket in a project (docs/plans/dispatch.md, "web/app"): its title, its description (Markdown, written here and previewed on its
 * page), its status and its priority; the store gives it its key, the project's next number (CND-12), never reassigned. Ctrl+Enter
 * (⌘+Enter) in the description makes it too. A refusal shows in the dialog.
 */
const open = defineModel<boolean>('open', { default: false })
const props = defineProps<{ project: string; gate: DispatchOrder }>()
const emit = defineEmits<{ created: [ticket: DispatchTicket] }>()

const api = useDispatchApi()
const ops = useDispatchOps(props.gate)

const title = ref('')
const description = ref('')
const status = ref<TicketStatus>('backlog')
const priority = ref(0)
const error = ref('')
const submitting = ref(false)

watch(open, (o) => {
  if (!o) return
  title.value = ''
  description.value = ''
  status.value = 'backlog'
  priority.value = 0
  error.value = ''
})

const statusItems = TICKET_STATUSES.map((s) => ({ label: statusWord(s).label, value: s, icon: statusWord(s).icon }))
const priorityItems = PRIORITIES.map((p) => ({ label: p.label, value: p.value, icon: p.icon }))
/** The description's bound, the store's, in bytes. */
const MAX_DESCRIPTION = 64 << 10
const over = computed(() => new TextEncoder().encode(description.value).length > MAX_DESCRIPTION)
const invalid = computed(() => !title.value.trim() || over.value)

async function submit() {
  if (invalid.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    const body = {
      project: props.project,
      title: title.value.trim(),
      ...(description.value ? { description: description.value } : {}),
      ...(status.value !== 'backlog' ? { status: status.value } : {}),
      ...(priority.value ? { priority: priority.value } : {}),
    }
    const reply = await ops.run<TicketReply>((op) => api.createTicket({ ...op, ...body }), { thing: `new-ticket:${props.project}`, what: 'New ticket', raise: true })
    if (reply?.ticket) emit('created', reply.ticket)
    open.value = false
  } catch (e) {
    error.value = dispatchFailureWords(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" :title="`New ticket in ${project}`" :ui="{ content: 'max-w-xl' }">
    <template #body>
      <form class="flex flex-col gap-4" data-new-ticket @submit.prevent="submit">
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" data-new-ticket-error />
        <UFormField label="Title" name="title">
          <UInput v-model="title" placeholder="What is to be done" maxlength="200" autofocus class="w-full" aria-label="Title" data-new-ticket-title />
        </UFormField>
        <UFormField label="Description" name="description" hint="Markdown, optional" :error="over ? 'More than the 64 KiB a description may hold' : undefined">
          <UTextarea
            v-model="description"
            autoresize
            :rows="5"
            :maxrows="16"
            placeholder="Why, and what done looks like"
            class="w-full"
            :ui="{ base: 'font-mono text-[13px]' }"
            aria-label="Description"
            data-new-ticket-description
            @keydown.meta.enter.prevent="submit"
            @keydown.ctrl.enter.prevent="submit"
          />
        </UFormField>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <UFormField label="Status" name="status">
            <USelect v-model="status" :items="statusItems" class="w-full" data-new-ticket-status />
          </UFormField>
          <UFormField label="Priority" name="priority">
            <USelect v-model="priority" :items="priorityItems" class="w-full" data-new-ticket-priority />
          </UFormField>
        </div>
        <button type="submit" class="hidden" />
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton label="Cancel" color="neutral" variant="ghost" @click="open = false" />
        <UButton label="Make the ticket" icon="i-lucide-plus" :loading="submitting" :disabled="invalid" data-new-ticket-submit @click="submit" />
      </div>
    </template>
  </UModal>
</template>
