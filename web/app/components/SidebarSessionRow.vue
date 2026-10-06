<script setup lang="ts">
import { relativeTime, sessionMeta } from '~/utils/sessions'
import { rowMeta, sessionOpen, type SessionRow } from '~/utils/sidebar'

/**
 * One session in the list (design 3b): the agent's initials, the name, a status dot, what it asks when it asks, and a meta line of
 * agent · where · how long. A hosted session's row carries its machine with a laptop, in place of a heading. The path is the
 * row's tooltip. An exited row says how it ended and when, with Resume beside the link (a link holds no button).
 */
const props = withDefaults(defineProps<{ row: SessionRow; now: number; member?: boolean; needsDot?: boolean }>(), { member: false, needsDot: true })
const route = useRoute()

const s = computed(() => props.row.session)
const current = computed(() => sessionOpen(route.path, s.value.id))
const meta = computed(() => rowMeta(s.value, props.member, props.now))
/** The meta line after the machine (which carries the laptop), the parts joined by middle dots as text, so it reads and copies as one line. */
const metaText = computed(() => (props.row.machine ? meta.value.slice(1).map((p) => ` · ${p}`).join('') : meta.value.join(' · ')))
const exitLine = computed(() => `${props.row.exitWord} · ${relativeTime(s.value.endedAt ?? s.value.createdAt, props.now, { suffix: true })}`)
</script>

<template>
  <li class="flex items-center gap-1" :data-sidebar-row="`s:${s.id}`" data-row-kind="session" :data-row-state="row.state">
    <NuxtLink
      :to="`/sessions/${s.id}`"
      class="flex min-w-0 flex-1 gap-2.5 rounded-md px-2.5 py-1.5 transition-colors"
      :class="[current ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60', row.state === 'exited' && !current && 'opacity-75']"
      :aria-current="current ? 'page' : undefined"
      :title="sessionMeta(s, now)"
    >
      <SessionAvatar :agent-id="s.agentId" :solid="row.state === 'needs'" :dashed="row.state === 'exited'" />
      <span class="flex min-w-0 flex-1 flex-col gap-0.5">
        <span class="flex min-w-0 items-center gap-1.5">
          <span class="truncate text-sm" :class="row.state === 'needs' ? 'font-semibold' : 'font-medium'">{{ s.name }}</span>
          <EventMarkBadge :session-id="s.id" />
          <YoloBadge v-if="s.yolo" icon />
          <slot name="pill" />
          <span v-if="row.state === 'needs' && needsDot" class="ml-auto size-2 flex-none rounded-full bg-warning" data-row-dot="needs" aria-hidden="true" />
          <span v-else-if="row.state === 'running'" class="ml-auto size-2 flex-none rounded-full bg-success" data-row-dot="running" aria-hidden="true" />
          <span v-else-if="row.state === 'idle'" class="ml-auto size-2 flex-none rounded-full bg-neutral-400" data-row-dot="idle" aria-hidden="true" />
        </span>
        <span v-if="row.prompt" class="truncate text-xs" data-row-prompt>{{ row.prompt.message }}</span>
        <span v-if="row.state === 'exited'" class="truncate font-mono text-[11px] text-muted">{{ exitLine }}</span>
        <span v-else class="flex items-center overflow-hidden whitespace-nowrap font-mono text-[11px] text-muted" data-row-meta>
          <span v-if="row.machine" class="inline-flex flex-none items-center gap-1" :data-row-machine="row.machine">
            <UIcon name="i-lucide-laptop" class="size-3 flex-none" :aria-label="`Hosted on ${row.machine}`" />{{ meta[0] }}
          </span>
          <!-- Spaces kept: the text starts with the dot that follows the machine. -->
          <span class="overflow-hidden text-ellipsis whitespace-pre">{{ metaText }}</span>
        </span>
      </span>
    </NuxtLink>
    <ResumeButton v-if="row.state === 'exited' && s.kind === 'server'" :session="s" icon-only size="xs" />
  </li>
</template>
