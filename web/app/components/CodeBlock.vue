<script setup lang="ts">
/**
 * A dark code block: `text` shown as it is with one Copy, or `commands` as
 * shell lines with a Copy each. The text stays selectable for pages served
 * without a clipboard API.
 */
defineProps<{
  text?: string
  commands?: string[]
  /** What the text is, for the toast after Copy. */
  name?: string
}>()

const copy = useCopy()
const copyClass = 'text-active-300 hover:bg-forest-900 hover:text-active-200'
</script>

<template>
  <div class="relative rounded-md bg-forest-950 text-forest-100" data-code-block>
    <ul v-if="commands" class="px-3 py-2 font-mono text-xs leading-relaxed">
      <li v-for="c in commands" :key="c" class="flex items-center gap-2">
        <span class="select-none text-forest-400" aria-hidden="true">$</span>
        <span class="min-w-0 flex-1 truncate select-text" :title="c">{{ c }}</span>
        <UButton icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" :aria-label="`Copy ${c}`" :class="copyClass" @click="copy(c, 'Command copied', c)" />
      </li>
    </ul>
    <template v-else-if="text">
      <pre class="max-h-64 overflow-auto p-3 pe-20 font-mono text-xs leading-relaxed select-text">{{ text }}</pre>
      <UButton label="Copy" icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" :class="['absolute top-1.5 end-1.5', copyClass]" @click="copy(text, 'Snippet copied', name)" />
    </template>
  </div>
</template>
