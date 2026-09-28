<script setup lang="ts">
import type { ViewerInfo } from '~/utils/protocol'
import { initials } from '~/utils/sessions'

const props = withDefaults(defineProps<{ viewers: ViewerInfo[]; max?: number }>(), { max: 4 })
const shown = computed(() => props.viewers.slice(0, props.max))
const extra = computed(() => Math.max(0, props.viewers.length - props.max))
const tones = ['bg-primary text-inverted', 'bg-forest-400 text-white', 'bg-forest-200 text-forest-900', 'bg-elevated text-primary']
</script>

<template>
  <div v-if="viewers.length" class="flex items-center" :title="viewers.map((v) => v.name).join(', ')">
    <span
      v-for="(v, i) in shown"
      :key="v.id"
      class="grid size-6.5 place-items-center rounded-full text-[10px] font-semibold ring-2 ring-default"
      :class="[tones[i % tones.length], i > 0 && '-ml-1.5']"
    >{{ initials(v.name) }}</span>
    <span v-if="extra" class="-ml-1.5 grid size-6.5 place-items-center rounded-full bg-elevated text-[10px] text-muted ring-2 ring-default">+{{ extra }}</span>
  </div>
</template>
