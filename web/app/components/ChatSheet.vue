<script setup lang="ts">
import type { ChatThread } from '~/composables/useChat'
import type { Role, ViewerInfo } from '~/utils/protocol'
import { SHEET_SNAPS } from '~/utils/chat'

/**
 * The chat where there is no room for the panel beside the terminal (design
 * 2c): on a phone a sheet from the bottom, two thirds of the screen and
 * dragging to full height, the terminal live behind it; wider, a slideover
 * from the right. Swipe down, Escape or × closes it. The header names who is
 * here, so the panel inside goes without its own. The drawer itself is as
 * tall as the window allows and slid down to its snap point (vaul writes the
 * slide as `--snap-point-height`), so the body takes what is left above the
 * window's edge, less the handle, and the composer sits at the visible
 * bottom at either height.
 */
withDefaults(defineProps<{ thread: ChatThread; role: Role; ended?: boolean; offline?: boolean; viewers?: ViewerInfo[]; note?: string; scope?: 'session' | 'run' }>(), {
  ended: false,
  offline: false,
  viewers: () => [],
  note: '',
  scope: 'session',
})
const emit = defineEmits<{ send: [text: string, toAgent: boolean]; sendToAgent: [ref: string]; retry: [nonce: string] }>()
const open = defineModel<boolean>('open', { default: false })
const phone = useIsPhone()
const SNAPS = [...SHEET_SNAPS]
/** The drawer's height above the window's edge, less its handle (a 1rem margin and the bar). */
const BODY_HEIGHT = 'calc(100% - var(--snap-point-height, 0px) - 1.375rem)'
</script>

<template>
  <UDrawer v-if="phone" v-model:open="open" direction="bottom" :snap-points="SNAPS" :overlay="false" :modal="false" handle>
    <template #content>
      <div class="flex w-full flex-col overflow-hidden" :style="{ height: BODY_HEIGHT }" data-chat-sheet>
        <div class="flex min-h-8 items-center gap-2 border-b border-default px-4 py-2">
          <span class="flex-1 font-semibold text-highlighted">Chat</span>
          <ViewerAvatars :viewers="viewers" :max="3" />
          <span v-if="viewers.length" class="text-xs text-muted">{{ viewers.length }} here</span>
          <UButton icon="i-lucide-x" color="neutral" variant="ghost" aria-label="Close the chat" data-chat-close @click="open = false" />
        </div>
        <ChatPanel :thread="thread" :role="role" :ended="ended" :offline="offline" :viewers="viewers" :note="note" :scope="scope" bare phone @send="(text, toAgent) => emit('send', text, toAgent)" @send-to-agent="emit('sendToAgent', $event)" @retry="emit('retry', $event)" />
      </div>
    </template>
  </UDrawer>
  <USlideover v-else v-model:open="open" side="right" title="Chat" :ui="{ content: 'sm:max-w-md', body: 'flex min-h-0 flex-1 flex-col p-0 sm:p-0' }">
    <template #actions>
      <ViewerAvatars :viewers="viewers" :max="3" />
      <span v-if="viewers.length" class="text-xs text-muted">{{ viewers.length }} here</span>
    </template>
    <template #close>
      <UButton icon="i-lucide-x" color="neutral" variant="ghost" aria-label="Close the chat" data-chat-close />
    </template>
    <template #body>
      <div class="flex min-h-0 flex-1 flex-col" data-chat-sheet>
        <ChatPanel :thread="thread" :role="role" :ended="ended" :offline="offline" :viewers="viewers" :note="note" :scope="scope" bare @send="(text, toAgent) => emit('send', text, toAgent)" @send-to-agent="emit('sendToAgent', $event)" @retry="emit('retry', $event)" />
      </div>
    </template>
  </USlideover>
</template>
