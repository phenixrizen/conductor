<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { agentInitials, relativeTime } from '~/utils/sessions'

const props = defineProps<{ sessions: SessionInfo[]; answered: SessionInfo[]; selected: number; sending: Set<string> }>()
const emit = defineEmits<{ reply: [session: SessionInfo, text: string]; option: [session: SessionInfo, index: number]; open: [session: SessionInfo]; select: [index: number] }>()

const drafts = reactive<Record<string, string>>({})
const now = ref(Date.now())
let tick: number | undefined
onMounted(() => (tick = window.setInterval(() => (now.value = Date.now()), 1000)))
onBeforeUnmount(() => window.clearInterval(tick))

const inputs = new Map<string, { inputRef?: HTMLInputElement }>()
function inputRef(id: string) {
  return (el: unknown) => {
    if (el) inputs.set(id, el as { inputRef?: HTMLInputElement })
    else inputs.delete(id)
  }
}

function submit(s: SessionInfo) {
  const text = (drafts[s.id] ?? '').trim()
  if (!text) return
  emit('reply', s, text)
  drafts[s.id] = ''
}

function agentName(s: SessionInfo) {
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', agy: 'Antigravity' }
  return names[s.agentId] ?? s.agentId
}

/** Puts the keyboard into the selected card's reply box (Enter on the wall). */
function focusSelected() {
  const s = props.sessions[props.selected]
  if (s) inputs.get(s.id)?.inputRef?.focus()
}

defineExpose({ focusSelected })
</script>

<template>
  <aside class="flex h-full min-h-0 w-[340px] flex-none flex-col gap-3 overflow-y-auto border-r border-default p-4" data-wall-queue>
    <h3 class="flex items-center text-[11px] font-semibold uppercase tracking-wider" :class="sessions.length ? 'text-warning' : 'text-muted'">
      Queue · {{ sessions.length ? `${sessions.length} waiting` : 'nothing waiting' }}
      <span class="ml-auto flex items-center gap-1 normal-case tracking-normal"><UKbd value="J" size="sm" /><span class="text-muted">/</span><UKbd value="K" size="sm" /></span>
    </h3>

    <div
      v-for="(s, i) in sessions"
      :key="s.id"
      class="flex flex-col gap-2.5 rounded-md border bg-elevated/40 p-3.5 transition-shadow"
      :class="[i === selected ? 'border-warning ring-2 ring-warning/40' : 'border-default']"
      :data-queue-session="s.id"
      @click="emit('select', i)"
    >
      <div class="flex items-center gap-2">
        <span class="grid size-5.5 place-items-center rounded bg-primary font-mono text-[9.5px] font-semibold text-inverted">{{ agentInitials(s.agentId) }}</span>
        <button type="button" class="truncate text-sm font-semibold hover:underline" @click.stop="emit('open', s)">{{ s.name }}</button>
        <span v-if="s.attention?.since" class="ml-auto font-mono text-[11px] text-muted">{{ relativeTime(s.attention.since, now) }}</span>
      </div>
      <p class="text-sm leading-snug">{{ s.attention?.message || 'Waiting for input' }}</p>
      <div v-if="s.attention?.options?.length" class="flex gap-1.5">
        <UButton
          v-for="(o, oi) in s.attention.options"
          :key="oi"
          :label="o.label"
          size="xs"
          :variant="oi === 0 ? 'solid' : 'outline'"
          :color="oi === 0 ? 'primary' : 'neutral'"
          class="flex-1 justify-center"
          :loading="sending.has(s.id)"
          @click.stop="emit('option', s, oi)"
        />
      </div>
      <form v-else class="flex items-center gap-1.5" @submit.prevent="submit(s)">
        <UInput :ref="inputRef(s.id)" v-model="drafts[s.id]" size="sm" class="flex-1" :placeholder="`Reply to ${agentName(s)}…`" :loading="sending.has(s.id)" @click.stop @focus="emit('select', i)" />
        <UButton type="submit" size="sm" icon="i-lucide-corner-down-left" color="neutral" variant="outline" aria-label="Send" :disabled="!(drafts[s.id] ?? '').trim()" />
      </form>
    </div>

    <template v-if="answered.length">
      <h3 class="mt-1 text-[11px] font-semibold uppercase tracking-wider text-muted">Answered</h3>
      <ol class="flex flex-col gap-1 text-xs text-muted">
        <li v-for="s in answered" :key="s.id" :title="s.lastAnswer?.message">
          <span class="text-default">{{ s.lastAnswer?.byName || 'Someone' }}</span> answered <button type="button" class="text-default hover:underline" @click="emit('open', s)">{{ s.name }}</button>
          <span v-if="s.lastAnswer"> · {{ relativeTime(s.lastAnswer.at, now) }}</span>
        </li>
      </ol>
    </template>
  </aside>
</template>
