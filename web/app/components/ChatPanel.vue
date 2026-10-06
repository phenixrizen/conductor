<script setup lang="ts">
import type { ChatThread } from '~/composables/useChat'
import type { Role, ViewerInfo } from '~/utils/protocol'
import { canSendToAgent } from '~/utils/chat'

/**
 * The chat beside a terminal (design 2a, 2d, 2h): who is here, the thread,
 * the composer; empty, offline and ended states. `note` is the line a
 * view-only guest reads above the composer; `bare` drops the header row for
 * a sheet that has its own (design 2c).
 */
const props = withDefaults(defineProps<{ thread: ChatThread; role: Role; ended?: boolean; offline?: boolean; viewers?: ViewerInfo[]; note?: string; scope?: 'session' | 'run'; phone?: boolean; bare?: boolean }>(), {
  ended: false,
  offline: false,
  viewers: () => [],
  note: '',
  scope: 'session',
  phone: false,
  bare: false,
})
const emit = defineEmits<{ send: [text: string, toAgent: boolean]; sendToAgent: [ref: string]; retry: [nonce: string] }>()

const agentOk = computed(() => canSendToAgent(props.role, props.ended))
const here = computed(() => props.viewers.length)
const words = computed(() => (props.scope === 'run' ? `everyone on this run's link sees it` : props.role === 'control' && !props.note ? 'everyone on this session sees the chat' : 'everyone on this link sees it'))
const empty = computed(() => props.thread.messages.every((m) => m.kind === 'system') && !props.thread.pending.length)
</script>

<template>
  <div class="flex h-full min-h-0 flex-col" data-chat :data-chat-scope="scope">
    <div v-if="!bare" class="flex items-center gap-2 border-b border-default px-4 py-2.5 text-xs text-muted">
      <ViewerAvatars :viewers="viewers" :max="3" />
      <span class="min-w-0 leading-snug"><template v-if="here">{{ here }} here · </template>{{ words }}</span>
    </div>
    <p v-if="offline && !ended" class="flex items-center gap-2 border-b border-default bg-elevated/60 px-4 py-2 text-xs text-muted" data-chat-offline-banner><UIcon name="i-lucide-loader-circle" class="size-3.5 animate-spin" />Offline. Reconnecting…</p>
    <p v-if="empty && !ended" class="px-4 pt-6 text-center text-sm text-muted" data-chat-empty>Nobody has said anything.<br />Everyone on this link sees this chat.</p>
    <ChatThread :messages="thread.messages" :pending="thread.pending" :can-send-to-agent="agentOk" @send-to-agent="emit('sendToAgent', $event)" @retry="emit('retry', $event)" />
    <div v-if="ended" class="border-t border-default px-4 py-3 text-xs text-muted" data-chat-ended>
      <p class="font-medium text-default">This session ended</p>
      <p>The chat is read-only and goes with the session. A run's chat stays in its record.</p>
    </div>
    <template v-else>
      <p v-if="note" class="border-t border-default px-4 py-2 text-[11px] text-muted" data-chat-note>{{ note }}</p>
      <ChatComposer :can-send-to-agent="agentOk" :offline="offline" :phone="phone" @send="(text, toAgent) => emit('send', text, toAgent)" />
    </template>
  </div>
</template>
