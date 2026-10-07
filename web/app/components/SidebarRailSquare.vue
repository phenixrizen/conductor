<script setup lang="ts">
import type { RailShape } from '~/utils/sidebar'

/**
 * One square of the rail (design 3d): a session as its agent's initials,
 * solid while it needs you, dashed once exited; the state top right (amber,
 * green, grey), new events bottom right, where it comes from bottom left (a
 * laptop for another machine, a globe for a link shared with you). A run
 * link shared with you shows a crew. Every square has a tooltip.
 */
const props = withDefaults(defineProps<{ shape: RailShape; member?: boolean; needsDot?: boolean }>(), { member: false, needsDot: true })
const route = useRoute()
const current = computed(() => route.path === props.shape.to || (props.shape.kind !== 'session' && route.path.startsWith(props.shape.to)))
const dot = computed(() => {
  if (props.shape.state === 'needs') return props.needsDot ? 'bg-warning' : ''
  if (props.shape.state === 'running') return 'bg-success'
  if (props.shape.state === 'idle') return 'bg-neutral-400'
  return ''
})
</script>

<template>
  <UTooltip :text="shape.label" :content="{ side: 'right' }">
    <NuxtLink
      :to="shape.to"
      class="relative grid place-items-center rounded-md p-0.5 transition-colors"
      :class="current ? 'bg-default ring-1 ring-default shadow-xs' : 'hover:bg-elevated/60'"
      :aria-label="shape.label"
      :aria-current="current ? 'page' : undefined"
      :data-rail-session="shape.kind === 'session' ? shape.id : undefined"
      :data-rail-shared-entry="shape.kind !== 'session' ? shape.id.replace(/^j:/, '') : undefined"
      :data-rail-member="member ? '' : undefined"
      :data-rail-dashed="shape.dashed ? '' : undefined"
      :data-rail-tile="shape.tile"
    >
      <span v-if="shape.kind === 'run'" class="grid size-6 place-items-center rounded-md bg-elevated text-primary"><UIcon name="i-lucide-users" class="size-3.5" /></span>
      <SessionAvatar v-else :agent-id="shape.agentId" :solid="shape.state === 'needs'" :dashed="shape.dashed" />
      <span v-if="dot" class="absolute -right-0.5 -top-0.5 size-2 rounded-full ring-2 ring-default" :class="dot" :data-rail-state="shape.state" aria-hidden="true" />
      <span v-if="shape.news" class="absolute -bottom-1 -right-1 grid min-w-3.5 place-items-center rounded-full bg-elevated px-0.5 text-[8px] font-semibold leading-3.5 text-highlighted ring-2 ring-default" :data-rail-news="shape.news" aria-hidden="true">{{ shape.news }}</span>
      <span v-if="shape.tile" class="absolute -bottom-1 -left-1 grid size-3.5 place-items-center rounded-sm bg-elevated text-muted ring-2 ring-default" aria-hidden="true">
        <UIcon :name="shape.tile === 'machine' ? 'i-lucide-laptop' : 'i-lucide-globe'" class="size-2.5" />
      </span>
    </NuxtLink>
  </UTooltip>
</template>
