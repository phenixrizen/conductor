<script setup lang="ts">
import { GAP_X, GAP_Y, layout, NODE_H, NODE_W, type GraphMember } from '~/utils/crewGraph'

/**
 * A crew's shape at a glance, for the Crews list: the same layout as the
 * graph, drawn as dots and lines in a small SVG (no canvas, no pan), roots
 * on the left. Nothing to click; the list entry is the link.
 */
const props = withDefaults(defineProps<{ members: GraphMember[]; width?: number; height?: number }>(), { width: 120, height: 40 })

const g = computed(() => layout(props.members))

/** Node centres scaled into the box, with a margin for the dots. */
const points = computed(() => {
  const r = 4
  const cols = Math.max(1, ...g.value.nodes.map((n) => n.depth + 1))
  const rows = Math.max(1, ...g.value.nodes.map((n) => n.row + 1))
  const sx = cols > 1 ? (props.width - 2 * r) / ((cols - 1) * (NODE_W + GAP_X)) : 0
  const sy = rows > 1 ? (props.height - 2 * r) / ((rows - 1) * (NODE_H + GAP_Y)) : 0
  const map = new Map<string, { x: number; y: number }>()
  for (const n of g.value.nodes) {
    map.set(n.id, {
      x: cols > 1 ? r + n.x * sx : props.width / 2,
      y: rows > 1 ? r + n.y * sy : props.height / 2,
    })
  }
  return map
})

const lines = computed(() =>
  g.value.edges
    .filter((e) => e.kind === 'after')
    .map((e) => ({ id: e.id, a: points.value.get(e.from)!, b: points.value.get(e.to)! }))
    .filter((l) => l.a && l.b),
)
</script>

<template>
  <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" class="flex-none text-muted" aria-hidden="true" data-crew-graph-thumb>
    <line v-for="l in lines" :key="l.id" :x1="l.a.x" :y1="l.a.y" :x2="l.b.x" :y2="l.b.y" stroke="currentColor" stroke-width="1" opacity="0.6" />
    <circle v-for="[id, p] in points" :key="id" :cx="p.x" :cy="p.y" r="3.5" class="fill-primary" />
  </svg>
</template>
