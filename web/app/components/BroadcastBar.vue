<script setup lang="ts">
import { broadcastByName, broadcastSummary } from '~/utils/crews'

/**
 * Types one line into the selected members of a run (POST
 * /api/runs/{run}/broadcast), recorded as input by the display name, or by
 * the server's OS user when none is set (broadcastByName). The server skips a
 * member waiting on a prompt; the button counts the members it will type
 * into (`willType`) and says how many selected ones it will skip (`waiting`);
 * the toast says who got the line and who was skipped, and why.
 */
const props = defineProps<{ runId: string; members: string[]; willType: number; waiting: number; disabled?: boolean }>()

const api = useSessions()
const toast = useToast()
const identity = useIdentity()
const text = ref('')
const sending = ref(false)

/** The server's limit, in bytes. */
const MAX = 4096
const tooLong = computed(() => new TextEncoder().encode(text.value).length > MAX)
const canSend = computed(() => !props.disabled && !sending.value && props.willType > 0 && !!text.value.trim() && !tooLong.value)

async function send() {
  if (!canSend.value) return
  sending.value = true
  try {
    const byName = await broadcastByName(identity.name.value, api.whoami)
    const r = await api.broadcastRun(props.runId, { text: text.value, members: [...props.members], byName })
    const s = broadcastSummary(r)
    toast.add({ title: s.title, description: s.description, color: s.color, icon: 'i-lucide-megaphone' })
    if (r.sent.length) text.value = ''
  } catch (e) {
    toast.add({ title: 'Broadcast failed', description: (e as Error).message, color: 'error', icon: 'i-lucide-triangle-alert' })
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <form class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-md border border-default px-3.5 py-2.5" data-broadcast @submit.prevent="send">
    <UIcon name="i-lucide-megaphone" class="size-[18px] flex-none text-muted" />
    <span class="flex-none text-sm font-semibold text-highlighted">Broadcast</span>
    <span class="flex-none text-xs text-muted" data-broadcast-scope>to {{ members.length }} selected<template v-if="waiting"> · <span class="text-warning">{{ waiting }} waiting skipped</span></template></span>
    <UInput
      v-model="text"
      size="sm"
      placeholder="Main moved. Rebase onto origin/main before your next commit."
      aria-label="Broadcast text"
      :color="tooLong ? 'error' : undefined"
      :highlight="tooLong"
      class="min-w-48 flex-1"
      :disabled="disabled"
    />
    <UButton type="submit" size="sm" :label="`Send to ${props.willType}`" trailing-icon="i-lucide-corner-down-left" :loading="sending" :disabled="!canSend" data-broadcast-send />
  </form>
</template>
