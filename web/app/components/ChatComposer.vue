<script setup lang="ts">
import { chatBytes, chatCounter, chatTooLong, cleanChatText } from '~/utils/chat'

/**
 * Where a message is written (design 2a, 2c): Enter sends, Shift+Enter is a
 * new line (on a phone the keyboard's own key), a counter shows past 1.5 KiB,
 * To agent sends and types it into the agent (a controller's). Offline, it
 * says messages go once the connection is back, and keeps taking them.
 */
const props = withDefaults(defineProps<{ disabled?: boolean; canSendToAgent?: boolean; offline?: boolean; placeholder?: string; phone?: boolean }>(), {
  disabled: false,
  canSendToAgent: false,
  offline: false,
  placeholder: 'Message everyone here',
  phone: false,
})
const emit = defineEmits<{ send: [text: string, toAgent: boolean] }>()

const draft = ref('')
const bytes = computed(() => chatBytes(cleanChatText(draft.value)))
const counter = computed(() => chatCounter(bytes.value))
const tooLong = computed(() => chatTooLong(bytes.value))
const empty = computed(() => cleanChatText(draft.value) === '')

function send(toAgent = false) {
  if (props.disabled || empty.value || tooLong.value) return
  emit('send', draft.value, toAgent)
  draft.value = ''
}

function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Enter' || e.isComposing) return
  if (e.shiftKey && !props.phone) return
  e.preventDefault()
  send(false)
}

defineExpose({ focus: () => input.value?.textareaRef?.focus() })
const input = useTemplateRef<{ textareaRef?: HTMLTextAreaElement }>('input')
</script>

<template>
  <form class="flex flex-col gap-1.5 border-t border-default px-3 py-2.5" data-chat-composer @submit.prevent="send(false)">
    <p v-if="offline" class="text-[11px] text-warning" data-chat-offline>Messages send once you are back online</p>
    <UTextarea
      ref="input"
      v-model="draft"
      :placeholder="placeholder"
      :rows="1"
      autoresize
      :maxrows="6"
      :disabled="disabled"
      aria-label="Message everyone here"
      :enterkeyhint="phone ? 'send' : undefined"
      class="w-full"
      :ui="{ base: 'text-sm' }"
      data-chat-input
      @keydown="onKeydown"
    />
    <div class="flex items-center gap-2 text-[11px] text-muted">
      <span v-if="!phone">Enter sends · Shift+Enter new line</span>
      <span v-if="counter" class="font-mono" :class="tooLong && 'text-error'" data-chat-counter>{{ counter }}</span>
      <span class="flex-1" />
      <UButton v-if="canSendToAgent" label="To agent" icon="i-lucide-corner-down-right" size="xs" color="neutral" variant="outline" :disabled="disabled || empty || tooLong" data-chat-to-agent @click="send(true)" />
      <UButton type="submit" label="Send" icon="i-lucide-send" size="xs" :disabled="disabled || empty || tooLong" data-chat-send />
    </div>
  </form>
</template>
