<script setup lang="ts">
import { Handle, Position, type NodeProps } from '@vue-flow/core'
import type { GraphDirection, GraphNode } from '~/utils/crewGraph'
import { memberDot } from '~/utils/runs'

/**
 * One member on the crew graph: its agent, name, status dot and needs-input
 * badge, its branch and diff in a run, a warning when its start rule could
 * not be drawn, and what can be done with it (Start now, Resume, the start
 * menu in the editor). The target handle takes the one "after" edge, the
 * source handle starts them. Enter opens the member; the graph handles
 * clicks.
 */
export interface MemberNodeData {
  node: GraphNode
  direction: GraphDirection
  editable: boolean
  /** The run the member is in; empty for a saved crew. */
  runId: string
  stopped: boolean
}

const props = defineProps<NodeProps<MemberNodeData>>()
const emit = defineEmits<{ open: [name: string]; start: [name: string]; setStart: [name: string, when: 'immediately' | 'manual']; remove: [name: string] }>()

const n = computed(() => props.data.node)
const state = computed(() => n.value.state)
const dot = computed(() => (state.value ? memberDot(state.value) : null))
const targetPos = computed(() => (props.data.direction === 'LR' ? Position.Left : Position.Top))
const sourcePos = computed(() => (props.data.direction === 'LR' ? Position.Right : Position.Bottom))

const startText = computed(() => {
  const s = n.value.member.start
  if (s.when === 'after') return `after ${s.member} idle`
  if (s.when === 'manual') return 'starts by hand'
  return 'starts at once'
})

const menu = computed(() => [
  [
    { label: 'Starts immediately', icon: 'i-lucide-play', onSelect: () => emit('setStart', n.value.id, 'immediately') },
    { label: 'Starts by hand', icon: 'i-lucide-hand', onSelect: () => emit('setStart', n.value.id, 'manual') },
  ],
  [{ label: 'Remove member', icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => emit('remove', n.value.id) }],
])
</script>

<template>
  <div
    class="flex h-24 w-60 flex-col gap-1 rounded-lg border bg-default p-2.5 text-left shadow-xs outline-none transition-shadow focus-visible:ring-2 focus-visible:ring-primary"
    :class="[selected ? 'border-primary ring-1 ring-primary' : state === 'needs_input' ? 'border-warning' : 'border-default', state === 'ended' && 'opacity-70']"
    tabindex="0"
    :data-graph-node="n.id"
    :data-status="state ?? ''"
    :aria-label="`${n.id}: ${dot?.label ?? startText}`"
    @keydown.enter.prevent="emit('open', n.id)"
  >
    <Handle type="target" :position="targetPos" :connectable="data.editable" :connectable-start="false" class="!size-2.5 !border-2 !border-default !bg-primary" :class="!data.editable && n.member.start.when !== 'after' && '!opacity-0'" />
    <div class="flex items-center gap-2">
      <SessionAvatar :agent-id="n.member.agentId" :solid="state === 'needs_input'" :dashed="state === 'ended' || state === 'pending'" />
      <span class="truncate text-[13px] font-semibold text-highlighted">{{ n.id }}</span>
      <span v-if="dot" class="size-2 flex-none rounded-full" :class="dot.dot" :title="dot.label" aria-hidden="true" />
      <UBadge v-if="state === 'needs_input'" label="needs input" color="warning" variant="subtle" size="sm" class="ml-auto flex-none" />
      <UDropdownMenu v-else-if="data.editable" :items="menu" :content="{ align: 'end' }" class="ml-auto">
        <UButton icon="i-lucide-ellipsis" size="xs" color="neutral" variant="ghost" :aria-label="`${n.id}: start rule`" class="nodrag" />
      </UDropdownMenu>
    </div>
    <div class="flex min-w-0 items-center gap-2 font-mono text-[11px] text-muted">
      <span class="truncate" :class="n.warning && 'text-warning'" :title="n.warning ?? n.member.branch ?? ''">{{ n.warning ?? n.member.branch ?? n.member.agentId }}</span>
      <span v-if="n.member.diff" class="ml-auto flex-none">+{{ n.member.diff.added }} −{{ n.member.diff.removed }}</span>
    </div>
    <div class="mt-auto flex min-w-0 items-center gap-1.5 text-[11px] text-muted">
      <template v-if="data.editable || !state">
        <span class="truncate">{{ startText }}</span>
      </template>
      <template v-else-if="state === 'pending'">
        <span class="truncate">{{ startText }}</span>
        <UButton v-if="!data.stopped" label="Start now" icon="i-lucide-play" size="xs" color="neutral" variant="outline" class="nodrag ml-auto" @click.stop="emit('start', n.id)" />
      </template>
      <template v-else-if="state === 'ended'">
        <span class="truncate" :class="n.member.error && 'text-error'" :title="n.member.error">{{ n.member.error ? `ended: ${n.member.error}` : 'ended' }}</span>
        <ResumeButton v-if="data.runId" :run-id="data.runId" :member="n.id" stay size="xs" class="nodrag ml-auto" @click.stop />
      </template>
      <template v-else>
        <span class="truncate">{{ dot?.label }}</span>
        <UButton label="Open" icon="i-lucide-terminal" size="xs" color="neutral" variant="ghost" class="nodrag ml-auto" @click.stop="emit('open', n.id)" />
      </template>
    </div>
    <Handle type="source" :position="sourcePos" :connectable="data.editable" :connectable-end="false" class="!size-2.5 !border-2 !border-default !bg-primary" :class="!data.editable && '!opacity-0'" />
  </div>
</template>
