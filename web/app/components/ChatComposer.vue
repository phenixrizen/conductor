<script setup lang="ts">
import type { ScopeItem } from '~/utils/chat'
import { chatBytes, chatCounter, chatTooLong, cleanChatText } from '~/utils/chat'

/**
 * Where a message is written (design 2a, 2c, 2e): Enter sends, Shift+Enter is
 * a new line (on a phone the keyboard's own key), a counter shows past 1.5
 * KiB, To agent sends and types it into the agent (a controller's). On a
 * run's chat the menu beside Send says where it goes: chat only, or also
 * typed into a member (`scopeItems`), a member that cannot take it disabled
 * with why. Offline, it says messages go once the connection is back, and
 * keeps taking them.
 */
const props = withDefaults(defineProps<{ disabled?: boolean; canSendToAgent?: boolean; offline?: boolean; placeholder?: string; phone?: boolean; scopeItems?: ScopeItem[] }>(), {
  disabled: false,
  canSendToAgent: false,
  offline: false,
  placeholder: 'Message everyone here',
  phone: false,
  scopeItems: undefined,
})
/** `to` is '' for the chat alone, `agent` for this session's agent, else a run member's name. */
const emit = defineEmits<{ send: [text: string, to: string] }>()

const draft = ref('')
const bytes = computed(() => chatBytes(cleanChatText(draft.value)))
const counter = computed(() => chatCounter(bytes.value))
const tooLong = computed(() => chatTooLong(bytes.value))
const empty = computed(() => cleanChatText(draft.value) === '')

/** The run chat's choice: where the next message goes; back to chat only when the member can no longer take it. */
const scope = ref('')
const chosen = computed(() => props.scopeItems?.find((s) => s.to === scope.value) ?? props.scopeItems?.[0])
watch(
  () => props.scopeItems,
  (items) => {
    if (scope.value && !items?.some((s) => s.to === scope.value && !s.disabled)) scope.value = ''
  },
)
// `member`, never `to`: a menu item's `to` is a link.
const scopeMenu = computed(() => [(props.scopeItems ?? []).map((s) => ({ label: s.label, member: s.to, detail: s.detail, disabled: s.disabled, onSelect: () => (scope.value = s.to) }))])

function send(to: string) {
  if (props.disabled || empty.value || tooLong.value) return
  emit('send', draft.value, to)
  draft.value = ''
}

function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Enter' || e.isComposing) return
  if (e.shiftKey && !props.phone) return
  e.preventDefault()
  send(scope.value)
}

defineExpose({ focus: () => input.value?.textareaRef?.focus() })
const input = useTemplateRef<{ textareaRef?: HTMLTextAreaElement }>('input')

/** A choice made, the person goes on typing: the menu would hand focus back to its button. */
function keepTyping(e: Event) {
  e.preventDefault()
  input.value?.textareaRef?.focus()
}
</script>

<template>
  <form class="flex flex-col gap-1.5 border-t border-default px-3 py-2.5" data-chat-composer @submit.prevent="send(scope)">
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
      <UDropdownMenu v-if="scopeItems" :items="scopeMenu" :content="{ align: 'end', onCloseAutoFocus: keepTyping }">
        <UButton :label="chosen?.label ?? 'Chat only'" trailing-icon="i-lucide-chevron-down" size="xs" color="neutral" variant="outline" :disabled="disabled" data-chat-scope-menu />
        <template #item-label="{ item }">
          <span class="flex flex-col" :data-chat-scope-item="item.member">
            <span>{{ item.label }}</span>
            <span class="text-[11px] text-muted">{{ item.detail }}</span>
          </span>
        </template>
      </UDropdownMenu>
      <UButton v-else-if="canSendToAgent" label="To agent" icon="i-lucide-corner-down-right" size="xs" color="neutral" variant="outline" :disabled="disabled || empty || tooLong" data-chat-to-agent @click="send('agent')" />
      <UButton type="submit" label="Send" icon="i-lucide-send" size="xs" :disabled="disabled || empty || tooLong" data-chat-send />
    </div>
  </form>
</template>
