<script setup lang="ts">
import type { Attention, Role } from '~/utils/protocol'

const props = defineProps<{ attention?: Attention; agentName: string; role: Role; busy?: boolean }>()
const emit = defineEmits<{ reply: [text: string]; option: [index: number] }>()

const text = ref('')
const show = computed(() => props.attention?.state === 'needs_input')
const options = computed(() => (show.value ? (props.attention?.options ?? []) : []))
const canReply = computed(() => props.role === 'control' && !props.busy)

function send() {
  const t = text.value.trim()
  if (!t || !canReply.value) return
  emit('reply', t)
  text.value = ''
}

function pick(i: number) {
  // The state may have changed under us (someone else answered): options
  // disappear with it, so a stale click is a no-op.
  if (!canReply.value || !options.value[i]) return
  emit('option', i)
}

// Number keys pick an option while the bar is visible and no field has focus.
function onKey(e: KeyboardEvent) {
  if (!options.value.length || e.metaKey || e.ctrlKey || e.altKey) return
  const target = e.target as HTMLElement | null
  // Nor while the sidebar's list has the focus: a digit there answers the focused row (SessionSidebar).
  if (target?.closest?.('input, textarea, select, [contenteditable], .terminal-host, [data-session-list]')) return
  const n = Number(e.key)
  if (Number.isInteger(n) && n >= 1 && n <= options.value.length) {
    e.preventDefault()
    pick(n - 1)
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div v-if="show" class="flex flex-wrap items-center gap-3 rounded-md border border-default bg-default px-3.5 py-3" data-quick-reply>
    <span class="size-2 rounded-full bg-warning flex-none" aria-hidden="true" />
    <div class="min-w-0 flex flex-col gap-0.5">
      <span class="text-sm font-semibold">{{ agentName }} is waiting on you</span>
      <span class="text-xs text-muted truncate">{{ attention?.message || 'Answer here or in the terminal. First reply wins; everyone sees who answered.' }}</span>
    </div>
    <div v-if="options.length" class="ml-auto flex flex-wrap items-center gap-2 flex-none">
      <UButton
        v-for="(o, i) in options"
        :key="i"
        :label="o.label"
        size="sm"
        :variant="i === 0 ? 'solid' : 'outline'"
        :color="i === 0 ? 'primary' : 'neutral'"
        :disabled="!canReply"
        :loading="busy"
        @click="pick(i)"
      >
        <template #leading><UKbd :value="String(i + 1)" size="sm" :class="i === 0 ? 'opacity-70' : ''" /></template>
      </UButton>
    </div>
    <form v-else class="ml-auto flex items-center gap-2 flex-none" @submit.prevent="send">
      <UInput v-model="text" size="sm" class="w-64" :placeholder="canReply ? `Reply to ${agentName}…` : 'View only'" :disabled="!canReply" />
      <UButton type="submit" size="sm" label="Send" trailing-icon="i-lucide-corner-down-left" :disabled="!canReply || !text.trim()" />
    </form>
  </div>
</template>
