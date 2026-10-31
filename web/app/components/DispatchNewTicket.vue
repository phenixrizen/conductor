<script setup lang="ts">
import type { TicketReply, TicketStatus } from '~/composables/useDispatch'
import type { DispatchOrder } from '~/utils/dispatchOrder'
import { PRIORITIES, TICKET_STATUSES, statusWord } from '~/utils/dispatch'
import { dispatchFailureWords } from '~/composables/useDispatchOps'

/**
 * New ticket in a project (docs/plans/dispatch.md, "web/app"): its title, its description (Markdown, written here and previewed on its
 * page), its status and its priority; the store gives it its key, the project's next number (CND-12), never reassigned. Ctrl+Enter
 * (⌘+Enter) in the description makes it too. A refusal shows in the dialog. While the ticket is made the dialog stays: Escape, a click
 * outside, its close button and Cancel do nothing until the store has answered, so that answer closes the opening it was made from,
 * never a later one holding a draft of its own. The ticket made is handed on by its key (`created`), its reply's ticket's, else its
 * receipt's: a creation whose read after the commit failed is answered with its receipt alone, and is made all the same.
 */
const open = defineModel<boolean>('open', { default: false })
const props = defineProps<{ project: string; gate: DispatchOrder }>()
const emit = defineEmits<{ created: [ticket: { key: string; title: string }] }>()

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
    // Made: by the key its reply's ticket has, else the one its receipt names, with the title the reply has, else the one sent.
    const key = reply?.ticket?.key || reply?.receipt.key
    if (key) emit('created', { key, title: reply?.ticket?.title ?? body.title })
    open.value = false
  } catch (e) {
    error.value = dispatchFailureWords(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" :title="`New ticket in ${project}`" :dismissible="!submitting" :close="{ disabled: submitting }" :ui="{ content: 'max-w-xl' }">
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
        <UButton label="Cancel" color="neutral" variant="ghost" :disabled="submitting" @click="open = false" />
        <UButton label="Make the ticket" icon="i-lucide-plus" :loading="submitting" :disabled="invalid" data-new-ticket-submit @click="submit" />
      </div>
    </template>
  </UModal>
</template>
