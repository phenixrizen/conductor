<script setup lang="ts">
/**
 * A ticket's Markdown field, its description or its acceptance criteria (docs/plans/dispatch.md, "web/app"): the text rendered by the
 * editor's preview (MarkdownPreview, with no folder and its relative links as words, since a ticket's text names no file of a
 * repository), and Edit: a textarea with a Preview beside it, saved as one operation by the page (`save`, which answers whether it was
 * done; the editor stays open with the text when it was not). Its bound is the store's, in bytes: past it Save is off and the field
 * says so. Ctrl+Enter (⌘+Enter) saves, Escape leaves the text as it was.
 */
const props = defineProps<{
  /** The text as the store holds it. */
  text: string
  label: string
  /** What an empty field says, and offers to write. */
  empty: string
  /** The store's bound, in bytes of UTF-8. */
  maxBytes: number
  /** Saves the text; true once the store has it. */
  save: (text: string) => Promise<boolean>
  disabled?: boolean
  /** The field's name, for the page's tests. */
  name: string
}>()

const editing = ref(false)
const draft = ref('')
const tab = ref<'write' | 'preview'>('write')
const saving = ref(false)
const area = useTemplateRef<{ textareaRef?: HTMLTextAreaElement }>('area')

const bytes = computed(() => new TextEncoder().encode(draft.value).length)
const over = computed(() => bytes.value > props.maxBytes)
const kib = (n: number) => `${Math.round((n / 1024) * 10) / 10} KiB`
const tabs = [
  { label: 'Write', value: 'write', icon: 'i-lucide-pencil' },
  { label: 'Preview', value: 'preview', icon: 'i-lucide-eye' },
]

function edit() {
  if (props.disabled) return
  draft.value = props.text
  tab.value = 'write'
  editing.value = true
  void nextTick(() => area.value?.textareaRef?.focus())
}

function cancel() {
  editing.value = false
}

async function submit() {
  if (over.value || saving.value) return
  if (draft.value === props.text) {
    editing.value = false
    return
  }
  saving.value = true
  try {
    if (await props.save(draft.value)) editing.value = false
  } finally {
    saving.value = false
  }
}

// A ticket deleted, or its project archived, while the field is open: nothing can be saved, so the editor goes.
watch(
  () => props.disabled,
  (d) => {
    if (d) editing.value = false
  },
)
</script>

<template>
  <section class="flex min-w-0 flex-col gap-2" :data-ticket-markdown="name">
    <div class="flex items-center gap-2">
      <h3 class="text-xs font-semibold uppercase tracking-wide text-muted">{{ label }}</h3>
      <UButton v-if="!editing && !disabled" icon="i-lucide-pencil" size="xs" color="neutral" variant="ghost" :aria-label="`Edit the ${label.toLowerCase()}`" class="ml-auto" data-markdown-edit @click="edit" />
    </div>

    <template v-if="!editing">
      <div v-if="text" class="min-w-0 rounded-md ring ring-default" data-markdown-view>
        <MarkdownPreview :text="text" dir="" :relative-links="false" />
      </div>
      <button v-else type="button" class="rounded-md border border-dashed border-default px-4 py-3 text-left text-sm text-dimmed enabled:hover:border-accented enabled:hover:text-muted" :disabled="disabled" data-markdown-empty @click="edit">
        {{ empty }}
      </button>
    </template>

    <div v-else class="flex flex-col gap-2" data-markdown-editor>
      <UTabs v-model="tab" :items="tabs" :content="false" size="xs" color="neutral" variant="link" class="w-fit" data-markdown-tabs />
      <UTextarea
        v-if="tab === 'write'"
        ref="area"
        v-model="draft"
        autoresize
        :rows="6"
        :maxrows="24"
        :aria-label="label"
        class="w-full"
        :ui="{ base: 'font-mono text-[13px] leading-relaxed' }"
        data-markdown-input
        @keydown.meta.enter.prevent="submit"
        @keydown.ctrl.enter.prevent="submit"
        @keydown.escape.prevent.stop="cancel"
      />
      <div v-else class="min-h-24 min-w-0 rounded-md ring ring-default" data-markdown-draft-preview>
        <MarkdownPreview v-if="draft" :text="draft" dir="" :relative-links="false" />
        <p v-else class="px-5 py-4 text-sm text-dimmed">Nothing to preview.</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs" :class="over ? 'text-error' : 'text-dimmed'" data-markdown-bytes>{{ over ? `${kib(bytes)}: more than the ${kib(maxBytes)} it may hold` : `Markdown · ${kib(bytes)} of ${kib(maxBytes)}` }}</span>
        <div class="flex-1" />
        <UButton label="Cancel" color="neutral" variant="ghost" size="sm" @click="cancel" />
        <UButton label="Save" icon="i-lucide-check" size="sm" :loading="saving" :disabled="over" data-markdown-save @click="submit" />
      </div>
    </div>
  </section>
</template>
