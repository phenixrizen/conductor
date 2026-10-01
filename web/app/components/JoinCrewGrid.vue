<script setup lang="ts">
import type { JoinRunMember } from '~/composables/useSessions'
import type { Role } from '~/utils/protocol'
import type { TerminalTransport } from '~/utils/transport/types'
import { joinTileStatus } from '~/utils/crews'
import { agentInitials } from '~/utils/sessions'

/**
 * The member tiles of a run link: a live, read-only terminal for each member that runs, a placeholder for the rest. Opening one is the
 * page's to do. What the tiles heard is the page's too (v-model:heard), so it outlasts the grid while a member is open in full.
 */
const props = defineProps<{
  members: JoinRunMember[]
  role: Role
  transportFor: (m: JoinRunMember) => () => TerminalTransport
  agentName: (agentId: string) => string
}>()
const emit = defineEmits<{ open: [member: JoinRunMember, heard: { status?: string; attention?: string } | undefined] }>()

/** What each member's tile last heard from its session, by member name. */
const heard = defineModel<Record<string, { status?: string; attention?: string }>>('heard', { required: true })
// The map as last set here or by the page. heard.value shows the page's copy only once the page has rendered again, so a second report
// before that (two tiles, or a status and an attention, in one task) builds on this one and does not drop the first.
let latest = heard.value
watch(heard, (v) => (latest = v), { flush: 'sync' })
function hear(name: string, patch: { status?: string; attention?: string }) {
  latest = { ...latest, [name]: { ...latest[name], ...patch } }
  heard.value = latest
}
function open(m: JoinRunMember) {
  if (m.sessionId) emit('open', m, latest[m.name])
}
</script>

<template>
  <div class="grid auto-rows-[18rem] gap-3 sm:grid-cols-2 xl:grid-cols-3" data-join-tiles>
    <template v-for="m in members" :key="m.name">
      <div
        v-if="m.sessionId"
        class="flex min-h-0 cursor-pointer flex-col overflow-hidden rounded-lg border bg-elevated/40 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary"
        :class="heard[m.name]?.attention === 'needs_input' ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented'"
        role="button"
        tabindex="0"
        :aria-label="`Open ${m.name}`"
        :data-member="m.name"
        @click="open(m)"
        @keydown.enter.prevent="open(m)"
        @keydown.space.prevent="open(m)"
      >
        <div class="flex flex-none items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
          <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
          <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
          <span class="flex items-center gap-1.5 text-[11.5px]" :class="joinTileStatus(m, heard[m.name]).cls"><span class="size-[7px] rounded-full" :class="joinTileStatus(m, heard[m.name]).dot" aria-hidden="true" />{{ joinTileStatus(m, heard[m.name]).label }}</span>
        </div>
        <div class="pointer-events-none min-h-0 flex-1">
          <TerminalView
            :create-transport="props.transportFor(m)"
            read-only
            fit="scale"
            compact
            :auto-focus="false"
            @status="(s) => hear(m.name, { status: s })"
            @attention="(a) => hear(m.name, { attention: a.state })"
          />
        </div>
        <div class="flex flex-none items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted">
          <span class="truncate">{{ props.agentName(m.agentId) }}</span>
          <span class="ml-auto flex-none">{{ role === 'control' ? 'open to type' : 'open' }}</span>
        </div>
      </div>
      <div v-else class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-dashed border-accented" :data-member="m.name">
        <div class="flex items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
          <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
          <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
        </div>
        <div class="flex flex-1 items-center justify-center p-3 text-sm text-muted">{{ m.status === 'ended' ? 'ended' : m.status === 'starting' ? 'starting…' : 'not started yet' }}</div>
      </div>
    </template>
  </div>
</template>
