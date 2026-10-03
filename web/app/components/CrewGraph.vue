<script setup lang="ts">
import { ConnectionMode, MarkerType, useVueFlow, VueFlow, type Connection, type Edge, type Node, type NodeMouseEvent } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import type { CrewStart } from '~/composables/useSessions'
import type { MemberStatus } from '~/utils/crews'
import { connectError, crewGraph, type GraphDirection, type GraphMember, type HandoffSeen } from '~/utils/crewGraph'
import type { MemberNodeData } from '~/components/CrewGraphNode.vue'
import type { CrewEdgeData } from '~/components/CrewGraphEdge.vue'

/**
 * The crew graph: members as nodes laid out by their start rules (roots on
 * the left), solid edges for "after X idle", dashed edges for the handoffs
 * of a run. On a run page it shows the members' live states and offers
 * Start now, Resume and Open; in the crew editor (`editable`) dragging from
 * one member's right handle to another's left sets "starts after", the
 * edge's × deletes the rule, and a node's menu sets the rule by hand. The
 * server's rules hold here too: one parent, no self edge, no cycle. On a
 * phone (under md) the graph is a list with the same badges.
 */
const props = withDefaults(
  defineProps<{
    members: GraphMember[]
    /** Live member states in a run (memberStatus). */
    states?: Map<string, MemberStatus>
    handoffs?: HandoffSeen[]
    editable?: boolean
    runId?: string
    stopped?: boolean
    selected?: string
    now?: number
    /** Fill the height the parent gives (a page body); else the canvas is a fixed 24 rem tall (a form). */
    fill?: boolean
  }>(),
  { editable: false, runId: '', stopped: false, selected: '', now: 0, fill: false },
)
const emit = defineEmits<{
  select: [name: string]
  open: [name: string]
  start: [name: string]
  /** `to` now starts after `from`, or on its own again (start given). */
  setStart: [name: string, start: CrewStart]
  remove: [name: string]
  /** A rule the editor refused, in words for a toast. */
  refused: [message: string]
}>()

const STORAGE_KEY = 'conductor.crewGraph'
function readPrefs(): { direction: GraphDirection; handoffs: boolean } {
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}') as { direction?: unknown; handoffs?: unknown }
    return { direction: raw.direction === 'TB' ? 'TB' : 'LR', handoffs: raw.handoffs !== false }
  } catch {
    return { direction: 'LR', handoffs: true }
  }
}
const prefs = ref(readPrefs())
watch(
  prefs,
  (p) => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(p))
    } catch {
      /* storage refused: the choice lasts the page */
    }
  },
  { deep: true },
)

// One Vue Flow store per graph, so the editor's and another page's never share nodes.
let seq = 0
const flowId = `crew-graph-${++seq}`
const { setNodes, setEdges, fitView } = useVueFlow({ id: flowId })

const graph = computed(() => crewGraph(props.members, { direction: prefs.value.direction, states: props.states, handoffs: props.editable || !prefs.value.handoffs ? [] : props.handoffs }))

const flowNodes = computed<Node<MemberNodeData>[]>(() =>
  graph.value.nodes.map((n) => ({
    id: n.id,
    type: 'member',
    position: { x: n.x, y: n.y },
    data: { node: n, direction: prefs.value.direction, editable: props.editable, runId: props.runId, stopped: props.stopped },
    connectable: props.editable,
    draggable: false,
    selected: n.id === props.selected,
  })),
)
const flowEdges = computed<Edge<CrewEdgeData>[]>(() =>
  graph.value.edges.map((e) => ({
    id: e.id,
    type: 'crew',
    source: e.from,
    target: e.to,
    data: { edge: e, editable: props.editable, now: props.now || Date.now() },
    markerEnd: MarkerType.ArrowClosed,
    animated: e.kind === 'handoff',
    selectable: false,
    focusable: false,
  })),
)

// The store takes the nodes and edges as they change; the view refits when the members or the direction change.
const wide = ref(true)
let media: MediaQueryList | undefined
function onMedia(e: MediaQueryListEvent | MediaQueryList) {
  wide.value = e.matches
}
onMounted(() => {
  media = window.matchMedia('(min-width: 768px)')
  onMedia(media)
  media.addEventListener('change', onMedia)
  // The canvas has its size now: fit once more than init did.
  nextTick(() => fitView({ padding: 0.2 }))
})
onBeforeUnmount(() => media?.removeEventListener('change', onMedia))

watch(
  [flowNodes, flowEdges],
  ([nodes, edges]) => {
    setNodes(nodes)
    setEdges(edges)
  },
  { immediate: true },
)
watch(
  () => [props.members.length, prefs.value.direction, wide.value] as const,
  () => nextTick(() => fitView({ padding: 0.2 })),
  { flush: 'post' },
)

function isValidConnection(c: Connection): boolean {
  return !!c.source && !!c.target && connectError(props.members, c.source, c.target) === ''
}

function onConnect(c: Connection) {
  const why = connectError(props.members, c.source, c.target)
  if (why) {
    emit('refused', why)
    return
  }
  emit('setStart', c.target, { when: 'after', member: c.source })
}

/** A connection dropped on a node that cannot take it: say why (Vue Flow refuses it silently). */
function onConnectEnd(ev?: MouseEvent | TouchEvent) {
  const target = (ev?.target as HTMLElement | null)?.closest?.('[data-graph-node]')?.getAttribute('data-graph-node')
  const from = connecting.value
  connecting.value = ''
  if (!from || !target || target === from) return
  const why = connectError(props.members, from, target)
  if (why) emit('refused', why)
}
const connecting = ref('')
function onConnectStart(p: { nodeId?: string | null }) {
  connecting.value = p.nodeId ?? ''
}

function onNodeClick(e: NodeMouseEvent) {
  emit('select', e.node.id)
}
function onNodeDoubleClick(e: NodeMouseEvent) {
  emit('open', e.node.id)
}

const legend = [
  { dot: 'bg-success', label: 'running' },
  { dot: 'bg-warning', label: 'needs input' },
  { dot: 'bg-info', label: 'starting' },
  { dot: 'bg-neutral-400', label: 'ended' },
  { dot: 'bg-transparent ring-1 ring-neutral-400', label: 'pending' },
]
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-2" :data-graph-mode="editable ? 'edit' : 'run'">
    <div class="flex flex-wrap items-center gap-1.5">
      <UButton v-if="wide" label="Fit" icon="i-lucide-scan" size="xs" color="neutral" variant="outline" data-graph-fit @click="fitView({ padding: 0.2 })" />
      <UButton
        v-if="wide"
        :label="prefs.direction === 'LR' ? 'Left to right' : 'Top to bottom'"
        :icon="prefs.direction === 'LR' ? 'i-lucide-arrow-right' : 'i-lucide-arrow-down'"
        size="xs"
        color="neutral"
        variant="outline"
        data-graph-direction
        @click="prefs.direction = prefs.direction === 'LR' ? 'TB' : 'LR'"
      />
      <USwitch v-if="!editable" v-model="prefs.handoffs" label="Handoffs" size="xs" data-graph-handoffs />
      <UPopover :content="{ align: 'end' }">
        <UButton label="Legend" icon="i-lucide-info" size="xs" color="neutral" variant="ghost" class="ml-auto" />
        <template #content>
          <dl class="grid grid-cols-[auto_1fr] items-center gap-x-2 gap-y-1 p-3 text-xs">
            <template v-for="l in legend" :key="l.label">
              <dt class="size-2 rounded-full" :class="l.dot" />
              <dd>{{ l.label }}</dd>
            </template>
            <dt class="h-px w-4 bg-(--ui-text-muted)" />
            <dd>starts after the member it points from is idle</dd>
            <dt class="h-px w-4 border-t border-dashed border-(--ui-info)" />
            <dd>handoffs in this run, with their count</dd>
          </dl>
          <p v-if="editable" class="border-t border-default px-3 py-2 text-xs text-muted">Drag from a member's right handle to another's left: that one starts after it. One parent each; the × on an edge removes the rule.</p>
        </template>
      </UPopover>
    </div>

    <p v-if="!members.length" class="rounded-md border border-dashed border-accented p-6 text-center text-sm text-muted" data-graph-empty>No members yet.</p>
    <p v-else-if="members.length === 1 && !editable" class="text-xs text-muted" data-graph-solo>One member: nothing waits on anything.</p>

    <CrewGraphList v-if="!wide" :graph="graph" :editable="editable" :stopped="stopped" :run-id="runId" :selected="selected" @open="emit('open', $event)" @start="emit('start', $event)" @select="emit('select', $event)" />
    <div v-else class="relative overflow-hidden rounded-md border border-default bg-elevated/30" :class="fill ? 'min-h-72 flex-1' : 'h-96'" data-graph-canvas>
      <VueFlow
        :id="flowId"
        :connection-mode="ConnectionMode.Strict"
        :is-valid-connection="isValidConnection"
        :nodes-draggable="false"
        :nodes-connectable="editable"
        :edges-updatable="false"
        :elements-selectable="true"
        :zoom-on-double-click="false"
        :min-zoom="0.4"
        :max-zoom="1.5"
        :delete-key-code="null"
        fit-view-on-init
        class="h-full w-full"
        @connect="onConnect"
        @connect-start="onConnectStart"
        @connect-end="onConnectEnd"
        @node-click="onNodeClick"
        @node-double-click="onNodeDoubleClick"
      >
        <Background :gap="16" :size="1" pattern-color="var(--ui-border)" />
        <Controls :show-interactive="false" position="bottom-right" />
        <template #node-member="p">
          <CrewGraphNode v-bind="p" @open="emit('open', $event)" @start="emit('start', $event)" @set-start="(name, when) => emit('setStart', name, { when })" @remove="emit('remove', $event)" />
        </template>
        <template #edge-crew="p">
          <CrewGraphEdge v-bind="p" @disconnect="emit('setStart', $event, { when: 'immediately' })" />
        </template>
      </VueFlow>
    </div>
  </div>
</template>

<style>
/* The canvas follows the app's theme: Vue Flow's own greys give way to the brand tokens. */
.vue-flow__controls {
  box-shadow: none;
  border: 1px solid var(--ui-border);
  border-radius: 6px;
  overflow: hidden;
}
.vue-flow__controls-button {
  background: var(--ui-bg);
  border-bottom: 1px solid var(--ui-border);
  fill: var(--ui-text);
}
.vue-flow__controls-button:hover {
  background: var(--ui-bg-elevated);
}
.vue-flow__edge.selected .vue-flow__edge-path,
.vue-flow__edge:focus .vue-flow__edge-path {
  stroke: var(--ui-primary);
}
.vue-flow__node-member {
  padding: 0;
  border: 0;
  background: transparent;
  width: auto;
}
</style>
