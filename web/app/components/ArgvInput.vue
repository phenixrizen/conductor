<script setup lang="ts">
import theme from '#build/ui/input-tags'
import { tv } from '@nuxt/ui/utils/tv'
import { hasOpenQuote, splitArgs } from '~/utils/argv'

/**
 * Edits an argv as chips, one per element. Enter, or a space outside quotes,
 * turns the typed text into chips; Backspace on an empty field removes the
 * last one. Pasted text with spaces or quotes is split like the Launch
 * dialog's extra arguments. Nothing here is a shell: quotes only group. Text
 * with a quote still open stays typed (`pending`), for the form to say so,
 * rather than turning into a chip that holds the quote.
 */
const model = defineModel<string[]>({ default: () => [] })
/** What is typed and not yet a chip. */
const pending = defineModel<string>('pending', { default: '' })
const props = defineProps<{ placeholder?: string; invalid?: boolean }>()

const appConfig = useAppConfig()
// The ring, padding and focus outline of Nuxt UI's tag input, which this is a version of.
const ui = computed(() =>
  tv({ extend: theme, ...((appConfig.ui as Record<string, object> | undefined)?.inputTags ?? {}) })({
    variant: 'outline',
    size: 'md',
    color: props.invalid ? 'error' : 'primary',
    highlight: props.invalid,
  }),
)
const field = useTemplateRef<{ inputRef: HTMLInputElement | null }>('field')

function add(args: string[]) {
  if (args.length) model.value = [...model.value, ...args]
}

/** Turns what is typed into chips, unless a quote is still open. */
function commit() {
  if (hasOpenQuote(pending.value)) return
  const args = splitArgs(pending.value)
  pending.value = ''
  add(args)
}

function remove(i: number) {
  model.value = model.value.filter((_, at) => at !== i)
  field.value?.inputRef?.focus()
}

// A space ends an argument unless a quote is still open. Watching the text
// rather than the key also covers soft keyboards and pasted text that ends in
// a space.
watch(pending, (t) => {
  if (/\s$/.test(t) && !hasOpenQuote(t)) commit()
})

function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return
  if (e.key === 'Enter') {
    e.preventDefault()
    commit()
  } else if (e.key === 'Backspace' && pending.value === '' && model.value.length) {
    e.preventDefault()
    model.value = model.value.slice(0, -1)
  }
}

function onPaste(e: ClipboardEvent) {
  const pasted = e.clipboardData?.getData('text') ?? ''
  // One plain word goes into the field as usual; anything with spaces or quotes becomes chips.
  if (!/[\s"']/.test(pasted.trim())) return
  e.preventDefault()
  add([...splitArgs(pending.value), ...splitArgs(pasted)])
  pending.value = ''
}
</script>

<template>
  <div :class="ui.root({ class: ui.base({ class: 'flex min-h-8 w-full cursor-text flex-wrap items-center gap-1.5' }) })" @click="field?.inputRef?.focus()">
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
      v-model="pending"
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
