<script setup lang="ts">
import { relativeTime, sessionMeta } from '~/utils/sessions'
import { rowMeta, sessionOpen, type SessionRow } from '~/utils/sidebar'
import { rowMenuItems, rowPrompt, sessionLive, stopQuestion } from '~/utils/sidebarActions'

/**
 * One session in the list (design 3b, 3c): the agent's initials, the name, a status dot, what it asks when it asks, and a meta
 * line of agent · where · how long. A hosted session's row carries its machine with a laptop, in place of a heading. The path is
 * the row's tooltip. An exited row says how it ended and when, with Resume beside the link (a link holds no button). Hovering
 * offers Share, Stop and More in place of the dot; Stop asks first, in the row; a right-click or a long press opens the same
 * menu as More. A prompt is answered in the row, below the link: its choices as numbered buttons, or a reply field. The list
 * drives the keys: `focused` marks the row they act on, `confirming` (a model: the hover Stop sets it too) shows the question.
 */
const props = withDefaults(defineProps<{ row: SessionRow; now: number; member?: boolean; needsDot?: boolean; busy?: boolean; focused?: boolean }>(), { member: false, needsDot: true, busy: false, focused: false })
const emit = defineEmits<{ share: [row: SessionRow]; stop: [row: SessionRow]; yard: [row: SessionRow]; openRun: [row: SessionRow]; answer: [row: SessionRow, index: number]; reply: [row: SessionRow, text: string] }>()
const route = useRoute()
const router = useRouter()

const s = computed(() => props.row.session)
const current = computed(() => sessionOpen(route.path, s.value.id))
const meta = computed(() => rowMeta(s.value, props.member, props.now))
/** The meta line after the machine (which carries the laptop), the parts joined by middle dots as text, so it reads and copies as one line. */
const metaText = computed(() => (props.row.machine ? meta.value.slice(1).map((p) => ` · ${p}`).join('') : meta.value.join(' · ')))
const exitLine = computed(() => `${props.row.exitWord} · ${relativeTime(s.value.endedAt ?? s.value.createdAt, props.now, { suffix: true })}`)
const live = computed(() => sessionLive(s.value.status))
const confirming = defineModel<boolean>('confirming', { default: false })
const question = computed(() => stopQuestion({ kind: 'session', name: s.value.name }))
const prompt = computed(() => rowPrompt(props.row))

const target = computed(() => ({ kind: 'session' as const, name: s.value.name, live: live.value, inRun: !!s.value.crew }))
const menu = computed(() =>
  rowMenuItems(target.value, {
    open: () => router.push(`/sessions/${s.value.id}`),
    openRun: () => emit('openRun', props.row),
    share: () => emit('share', props.row),
    yard: () => emit('yard', props.row),
    stop: () => (confirming.value = true),
  }),
)

function confirmStop() {
  confirming.value = false
  emit('stop', props.row)
}
</script>

<template>
  <UContextMenu :items="menu">
    <!-- The open session's box is the row's, so that a prompt answered below the link sits inside it. -->
    <li class="group relative flex flex-col rounded-md" :class="[current && 'bg-default border border-default shadow-xs', focused && 'ring-2 ring-primary/40']" :data-sidebar-row="`s:${s.id}`" data-row-kind="session" :data-row-state="row.state" :data-row-focused="focused ? '' : undefined">
      <div class="flex items-center gap-1">
      <NuxtLink
        :to="`/sessions/${s.id}`"
        class="flex min-w-0 flex-1 gap-2.5 rounded-md px-2.5 py-1.5 transition-colors"
        :class="[!current && 'hover:bg-elevated/60', row.state === 'exited' && !current && 'opacity-75']"
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
            <!-- The dot gives way to the actions on hover. -->
            <span v-if="row.state === 'needs' && needsDot" class="ml-auto size-2 flex-none rounded-full bg-warning group-hover:opacity-0" data-row-dot="needs" aria-hidden="true" />
            <span v-else-if="row.state === 'running'" class="ml-auto size-2 flex-none rounded-full bg-success group-hover:opacity-0" data-row-dot="running" aria-hidden="true" />
            <span v-else-if="row.state === 'idle'" class="ml-auto size-2 flex-none rounded-full bg-neutral-400 group-hover:opacity-0" data-row-dot="idle" aria-hidden="true" />
          </span>
          <template v-if="confirming">
            <span class="text-xs font-medium text-highlighted" data-row-stop-confirm>{{ question.title }}</span>
            <span class="text-[11px] leading-snug text-muted">{{ question.detail }}</span>
          </template>
          <template v-else>
            <span v-if="row.prompt" class="truncate text-xs" data-row-prompt>{{ row.prompt.message }}</span>
            <span v-if="row.state === 'exited'" class="truncate font-mono text-[11px] text-muted">{{ exitLine }}</span>
            <span v-else class="flex items-center overflow-hidden whitespace-nowrap font-mono text-[11px] text-muted" data-row-meta>
              <span v-if="row.machine" class="inline-flex flex-none items-center gap-1" :data-row-machine="row.machine">
                <UIcon name="i-lucide-laptop" class="size-3 flex-none" :aria-label="`Hosted on ${row.machine}`" />{{ meta[0] }}
              </span>
              <!-- Spaces kept: the text starts with the dot that follows the machine. -->
              <span class="overflow-hidden text-ellipsis whitespace-pre">{{ metaText }}</span>
            </span>
          </template>
        </span>
      </NuxtLink>
      <ResumeButton v-if="row.state === 'exited' && s.kind === 'server'" :session="s" icon-only size="xs" />
      </div>
      <!-- The confirm's buttons and the hover actions sit beside the link: a link holds no button. -->
      <span v-if="confirming" class="absolute right-1.5 bottom-1.5 flex items-center gap-1 rounded-md bg-default/95 p-0.5 shadow-xs ring ring-default">
        <UButton label="Cancel" size="xs" color="neutral" variant="ghost" @click="confirming = false" />
        <UButton label="Stop" icon="i-lucide-square" size="xs" color="error" :loading="busy" data-confirm-stop @click="confirmStop" />
      </span>
      <span
        v-else
        class="absolute right-1.5 top-1.5 flex items-center gap-0.5 rounded-md bg-default/95 p-0.5 opacity-0 shadow-xs ring ring-default transition-opacity focus-within:opacity-100 group-hover:opacity-100"
        data-row-actions
      >
        <UTooltip v-if="live" text="Share">
          <UButton icon="i-lucide-share-2" size="xs" color="neutral" variant="ghost" :aria-label="`Share ${s.name}`" data-row-share @click="emit('share', row)" />
        </UTooltip>
        <UTooltip v-if="live" text="Stop">
          <UButton icon="i-lucide-square" size="xs" color="neutral" variant="ghost" :aria-label="`Stop ${s.name}`" data-row-stop @click="confirming = true" />
        </UTooltip>
        <UDropdownMenu :items="menu" :content="{ align: 'end' }">
          <UButton icon="i-lucide-ellipsis" size="xs" color="neutral" variant="ghost" :aria-label="`More for ${s.name}`" data-row-more />
        </UDropdownMenu>
      </span>
      <SidebarPrompt v-if="prompt && !confirming" :prompt="prompt" :busy="busy" @answer="emit('answer', row, $event)" @reply="emit('reply', row, $event)" />
    </li>
  </UContextMenu>
</template>
