<script setup lang="ts">
import type { PendingChat } from '~/composables/useChat'
import type { ChatMessage } from '~/utils/protocol'
import { avatarTone } from '~/utils/avatar'
import { chatTime, linkify, markerLine, systemLine } from '~/utils/chat'
import { initials } from '~/utils/sessions'

/**
 * The messages of a chat (design 2a): a person's avatar, name, a `view` tag
 * for a view-only person, the time, the text with its links; a quiet line
 * for a join or leave; a marker where a message was typed into the agent;
 * this browser's own messages on their way, and the ones that failed with
 * Retry. Hovering a message offers Send to agent to a controller.
 */
const props = defineProps<{ messages: ChatMessage[]; pending: PendingChat[]; canSendToAgent: boolean }>()
const emit = defineEmits<{ sendToAgent: [ref: string]; retry: [nonce: string] }>()

const scroller = useTemplateRef<HTMLElement>('scroller')
let stick = true
function onScroll() {
  const el = scroller.value
  if (!el) return
  stick = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}
watch(
  () => [props.messages.length, props.pending.length],
  async () => {
    if (!stick) return
    await nextTick()
    const el = scroller.value
    if (el) el.scrollTop = el.scrollHeight
  },
  { flush: 'post' },
)
onMounted(() => {
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
})
</script>

<template>
  <div ref="scroller" class="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 py-3" data-chat-thread @scroll.passive="onScroll">
    <template v-for="m in messages" :key="m.id">
      <p v-if="m.kind === 'system'" class="text-center text-[11px] text-muted" :data-chat-message="m.id" data-chat-kind="system" data-chat-system>{{ systemLine(m) }} · {{ chatTime(m.at) }}</p>
      <p v-else-if="m.kind === 'sent_to_agent'" class="flex items-center gap-1.5 pl-9 text-[11px] text-muted" :data-chat-message="m.id" data-chat-kind="sent_to_agent" data-chat-marker>
        <UIcon name="i-lucide-corner-down-right" class="size-3 flex-none" aria-hidden="true" />{{ markerLine(m) }}
      </p>
      <div v-else class="group flex gap-2.5" :data-chat-message="m.id" data-chat-kind="message">
        <span class="grid size-7 flex-none place-items-center rounded-full text-[11px] font-semibold" :class="avatarTone(m.by.name)">{{ initials(m.by.name) }}</span>
        <div class="flex min-w-0 flex-1 flex-col gap-0.5">
          <div class="flex items-center gap-1.5 text-xs">
            <span class="truncate font-semibold text-highlighted">{{ m.by.name }}</span>
            <span v-if="m.by.role === 'view'" class="rounded-sm border border-default px-1 text-[10px] text-muted" data-chat-view-tag>view</span>
            <span class="font-mono text-[11px] text-muted">{{ chatTime(m.at) }}</span>
            <UButton
              v-if="canSendToAgent && m.text"
              label="Send to agent"
              icon="i-lucide-corner-down-right"
              size="xs"
              color="neutral"
              variant="ghost"
              class="ml-auto opacity-0 transition-opacity focus-visible:opacity-100 group-hover:opacity-100"
              data-chat-send-to-agent
              @click="emit('sendToAgent', m.id)"
            />
          </div>
          <p class="whitespace-pre-wrap break-words text-sm">
            <template v-for="(seg, i) in linkify(m.text ?? '')" :key="i">
              <a v-if="seg.href" :href="seg.href" target="_blank" rel="noopener noreferrer" class="break-all text-primary underline underline-offset-2">{{ seg.text }}</a>
              <template v-else>{{ seg.text }}</template>
            </template>
          </p>
        </div>
      </div>
    </template>
    <div v-for="p in pending" :key="p.nonce" class="flex gap-2.5 opacity-80" :data-chat-pending="p.nonce" :data-chat-pending-state="p.state">
      <span class="grid size-7 flex-none place-items-center rounded-full bg-elevated text-[11px] font-semibold text-muted">…</span>
      <div class="flex min-w-0 flex-1 flex-col gap-0.5">
        <p class="whitespace-pre-wrap break-words text-sm">{{ p.text }}</p>
        <p v-if="p.state === 'failed'" class="flex items-center gap-2 text-[11px] text-warning">
          Not sent<template v-if="p.error"> · {{ p.error }}</template>
          <UButton label="Retry" size="xs" color="neutral" variant="link" class="p-0" data-chat-retry @click="emit('retry', p.nonce)" />
        </p>
        <p v-else-if="p.state === 'queued'" class="text-[11px] text-muted">Waiting for the connection…</p>
      </div>
    </div>
  </div>
</template>
