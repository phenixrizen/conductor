<script setup lang="ts">
import { hasOpenQuote, splitArgs } from '~/utils/argv'

/**
 * Edits an argv as chips, one per element. Enter, or a space outside quotes,
 * turns the typed text into chips; Backspace on an empty field removes the
 * last one. Pasted text with spaces or quotes is split like the Launch
 * dialog's extra arguments. Nothing here is a shell: quotes only group.
 */
const model = defineModel<string[]>({ default: () => [] })
defineProps<{ placeholder?: string; invalid?: boolean }>()

const text = ref('')
const field = useTemplateRef<{ inputRef: HTMLInputElement | null }>('field')

function add(args: string[]) {
  if (args.length) model.value = [...model.value, ...args]
}

/** Turns what is typed into chips. */
function commit() {
  const args = splitArgs(text.value)
  text.value = ''
  add(args)
}

function remove(i: number) {
  model.value = model.value.filter((_, at) => at !== i)
  field.value?.inputRef?.focus()
}

// A space ends an argument unless a quote is still open. Watching the text
// rather than the key also covers soft keyboards and pasted text that ends in
// a space.
watch(text, (t) => {
  if (/\s$/.test(t) && !hasOpenQuote(t)) commit()
})

function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return
  if (e.key === 'Enter') {
    e.preventDefault()
    commit()
  } else if (e.key === 'Backspace' && text.value === '' && model.value.length) {
    e.preventDefault()
    model.value = model.value.slice(0, -1)
  }
}

function onPaste(e: ClipboardEvent) {
  const pasted = e.clipboardData?.getData('text') ?? ''
  // One plain word goes into the field as usual; anything with spaces or quotes becomes chips.
  if (!/[\s"']/.test(pasted.trim())) return
  e.preventDefault()
  add([...splitArgs(text.value), ...splitArgs(pasted)])
  text.value = ''
}
</script>

<template>
  <div
    class="flex min-h-8 w-full cursor-text flex-wrap items-center gap-1.5 rounded-md bg-default px-2.5 py-1.5 ring ring-inset transition-colors has-focus-visible:outline-3"
    :class="invalid ? 'ring-error outline-error/25 has-focus-visible:ring-error' : 'ring-accented outline-primary/25 has-focus-visible:ring-primary'"
    @click="field?.inputRef?.focus()"
  >
    <UBadge v-for="(arg, i) in model" :key="i" color="neutral" variant="subtle" size="md" class="max-w-full font-mono">
      <span class="truncate" :class="arg === '' && 'text-muted'">{{ arg === '' ? "''" : arg }}</span>
      <template #trailing>
        <button
          type="button"
          class="-me-0.5 inline-flex flex-none rounded-xs text-dimmed transition-colors hover:text-default"
          :aria-label="`Remove ${arg === '' ? 'empty argument' : arg}`"
          @click.stop="remove(i)"
        >
          <UIcon name="i-lucide-x" class="size-3.5" />
        </button>
      </template>
    </UBadge>
    <UInput
      ref="field"
      v-model="text"
      variant="none"
      :placeholder="model.length ? undefined : placeholder"
      :ui="{ root: 'min-w-24 flex-1', base: 'p-0 font-mono ring-0' }"
      autocapitalize="off"
      spellcheck="false"
      @keydown="onKeydown"
      @paste="onPaste"
      @blur="commit"
    />
  </div>
</template>
