<script setup lang="ts">
import type { RowPrompt } from '~/utils/sidebarActions'

/**
 * A prompt answered in the row (design 3b, 3c): the choices as numbered buttons, a free-text prompt as a reply field where Enter
 * sends. Busy while an answer is on its way; waiting, disabled, with the words while a host is away. It sits beside the row's link,
 * never inside it (a link holds no button), and keeps its keys to itself so that the list's and the window's digit keys never see
 * what is typed.
 */
const props = withDefaults(defineProps<{ prompt: RowPrompt; busy?: boolean; big?: boolean }>(), { busy: false, big: false })
const emit = defineEmits<{ answer: [index: number]; reply: [text: string] }>()

const text = ref('')
const off = computed(() => props.busy || props.prompt.away)

function send() {
  const t = text.value.trim()
  if (!t || off.value) return
  emit('reply', t)
  text.value = ''
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-1.5 pb-1.5 pr-2" :class="big ? 'flex-col items-stretch pl-2 pt-1' : 'pl-10'" :title="prompt.away ? prompt.placeholder : undefined" data-row-answer>
    <template v-if="prompt.choices.length">
      <UButton
        v-for="(o, i) in prompt.choices"
        :key="i"
        :label="o.label"
        :size="big ? 'lg' : 'xs'"
        color="neutral"
        variant="outline"
        :class="big && 'min-h-11 justify-start'"
        :disabled="off"
        :loading="busy"
        :data-row-choice="i + 1"
        @click="emit('answer', i)"
      >
        <template v-if="i < 9" #leading><UKbd :value="String(i + 1)" size="sm" class="opacity-70" /></template>
      </UButton>
    </template>
    <UInput
      v-else
      v-model="text"
      :size="big ? 'lg' : 'xs'"
      :placeholder="prompt.placeholder"
      maxlength="4096"
      :disabled="off"
      :loading="busy"
      class="w-full"
      :ui="{ base: 'font-normal' }"
      aria-label="Reply"
      data-row-reply
      @keydown.stop
      @keydown.enter.prevent="send"
    >
      <template #trailing>
        <UButton icon="i-lucide-send" size="xs" color="neutral" variant="link" class="p-0" aria-label="Send the reply" :disabled="off || !text.trim()" data-row-reply-send @click="send" />
      </template>
    </UInput>
  </div>
</template>
