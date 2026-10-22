<script setup lang="ts">
import { choiceKey } from '~/utils/attention'
import { replyFailure } from '~/utils/reply'
import { rowPrompt } from '~/utils/sidebarActions'
import { sessionRow } from '~/utils/sidebar'

/**
 * The pill `]` and `[` show (#303): at the top of the viewport for four
 * seconds, the agent chip, the name, the question, "n of m" and the
 * choices as numbered buttons. 1–9 answer over the quick reply without
 * leaving the page, Enter opens the session, Escape dismisses it. A
 * component of its own, not a toast. Its keys are read on the document
 * while it shows, from anywhere but a text field or a terminal, and from
 * the pill itself when the Alt twins gave it the focus.
 */
const cursor = useNeedsCursor()
const attention = useAttention()
const quick = useQuickReply()
const toast = useToast()
const router = useRouter()
const root = useTemplateRef<HTMLElement>('root')

const session = computed(() => (cursor.shown.value ? attention.sessions.value.find((s) => s.id === cursor.shown.value!.id) : undefined))
const prompt = computed(() => (session.value ? rowPrompt(sessionRow(session.value)) : null))
/** The session answered or gone: the pill goes with it. */
watch(
  () => session.value?.attention?.state,
  (state) => {
    if (cursor.shown.value && state !== 'needs_input') cursor.hide()
  },
)

function open() {
  const s = session.value
  if (!s) return
  cursor.hide()
  void router.push(`/sessions/${s.id}`)
}
function answer(i: number) {
  const s = session.value
  const p = prompt.value
  const o = p?.choices[i]
  if (!s || !p || !o || p.held || p.away) return
  quick.send(s, o.input, { question: p.question }).catch((e: unknown) => toast.add(replyFailure(e, s.name)))
  cursor.hide()
}
function onKey(e: KeyboardEvent) {
  if (!cursor.shown.value) return
  const target = e.target as HTMLElement | null
  const inPill = !!target?.closest?.('[data-needs-pill]')
  // A text field or a terminal keeps its keys unless the pill itself has the focus.
  if (!inPill && target?.closest?.('input, textarea, select, [contenteditable], .terminal-host')) return
  if (e.altKey || e.ctrlKey || e.metaKey) return
  if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation()
    cursor.hide()
    return
  }
  if (e.key === 'Enter') {
    e.preventDefault()
    e.stopPropagation()
    open()
    return
  }
  const n = Number(e.key)
  if (Number.isInteger(n) && n >= 1 && n <= 9) {
    const o = prompt.value?.choices[n - 1]
    if (o && choiceKey(o, n - 1) === n) {
      e.preventDefault()
      e.stopPropagation()
      answer(n - 1)
    }
  }
}
onMounted(() => document.addEventListener('keydown', onKey, true))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey, true))
// From the Alt twins the pill takes the focus; a plain ] leaves it where it is.
watch(
  () => cursor.shown.value?.seq,
  async () => {
    if (!cursor.shown.value?.focus) return
    await nextTick()
    root.value?.focus()
  },
)
</script>

<template>
  <Transition enter-active-class="transition duration-150" enter-from-class="-translate-y-2 opacity-0" leave-active-class="transition duration-150" leave-to-class="-translate-y-2 opacity-0">
    <div
      v-if="cursor.shown.value && session"
      ref="root"
      class="fixed inset-x-0 top-3 z-50 mx-auto flex w-[min(40rem,calc(100vw-2rem))] items-start gap-3 rounded-md border border-warning/40 bg-default/95 px-3.5 py-2.5 shadow-lg outline-none backdrop-blur-sm focus-visible:ring-2 focus-visible:ring-primary"
      role="dialog"
      aria-live="polite"
      :aria-label="`${session.name} needs you, ${cursor.shown.value.n} of ${cursor.shown.value.of}`"
      tabindex="-1"
      data-needs-pill
      :data-pill-session="session.id"
    >
      <SessionAvatar :agent-id="session.agentId" solid class="mt-0.5" />
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <span class="truncate text-sm font-semibold text-highlighted">{{ session.name }}</span>
          <span class="flex-none font-mono text-[11px] text-muted" data-pill-count>{{ cursor.shown.value.n }} of {{ cursor.shown.value.of }}</span>
          <span class="ml-auto flex flex-none items-center gap-1 font-mono text-[11px] text-muted"><UKbd value="enter" size="sm" /> open <UKbd value="escape" size="sm" /></span>
        </div>
        <p class="break-words text-xs text-muted" data-pill-message>{{ prompt?.message || session.attention?.message || 'needs you' }}</p>
        <div v-if="prompt?.choices.length" class="mt-1.5 flex flex-wrap gap-1">
          <UButton
            v-for="(o, i) in prompt.choices"
            :key="i"
            size="xs"
            color="neutral"
            variant="soft"
            :disabled="prompt.held || prompt.away"
            :label="`${choiceKey(o, i) ?? '·'} ${o.label}`"
            :data-pill-choice="choiceKey(o, i) ?? ''"
            @click="answer(i)"
          />
        </div>
        <p v-else-if="prompt && (prompt.held || prompt.away)" class="mt-1 text-xs text-muted">{{ prompt.placeholder }}</p>
        <p v-else-if="prompt" class="mt-1 text-xs text-muted">A line to type: open it (Enter) and reply there.</p>
      </div>
    </div>
  </Transition>
</template>
