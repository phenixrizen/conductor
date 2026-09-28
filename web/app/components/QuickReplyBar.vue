<script setup lang="ts">
import type { Attention, Role } from '~/utils/protocol'

const props = defineProps<{ attention?: Attention; agentName: string; role: Role; busy?: boolean }>()
const emit = defineEmits<{ reply: [text: string]; option: [index: number] }>()

const text = ref('')
const show = computed(() => props.attention?.state === 'needs_input')
const canReply = computed(() => props.role === 'control' && !props.busy)

function send() {
  const t = text.value.trim()
  if (!t || !canReply.value) return
  emit('reply', t)
  text.value = ''
}
</script>

<template>
  <div v-if="show" class="flex items-center gap-3 rounded-md border border-default bg-default px-3.5 py-3" data-quick-reply>
    <span class="size-2 rounded-full bg-warning flex-none" aria-hidden="true" />
    <div class="min-w-0 flex flex-col gap-0.5">
      <span class="text-sm font-semibold">{{ agentName }} is waiting on you</span>
      <span class="text-xs text-muted truncate">{{ attention?.message || 'Answer here or in the terminal. First reply wins; everyone sees who answered.' }}</span>
    </div>
    <form class="ml-auto flex items-center gap-2 flex-none" @submit.prevent="send">
      <UInput v-model="text" size="sm" class="w-64" :placeholder="canReply ? `Reply to ${agentName}…` : 'View only'" :disabled="!canReply" />
      <UButton type="submit" size="sm" label="Send" trailing-icon="i-lucide-corner-down-left" :disabled="!canReply || !text.trim()" />
    </form>
  </div>
</template>
