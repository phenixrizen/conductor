<script setup lang="ts">
import type { FileResponse } from '~/utils/protocol'
import { clampSplit, readSplit, toggleFold, writeSplit, type TabsState } from '~/utils/editorTabs'

/**
 * The column a terminal shares with the editor area (design 4b, 4g): the
 * area above, a bar you drag between them (the split kept per browser, the
 * terminal keeping its minimum height), the terminal in the default slot,
 * the reply bar in `bar`. On a phone the editor takes the column and the
 * terminal folds to a bar at the foot that says what the agent is doing;
 * a tap brings the terminal back (the editor folds to its strip).
 */
const props = defineProps<{
  request: (path: string, stat?: boolean) => Promise<FileResponse>
  cwd?: string
  rawUrl?: (path: string) => string | null
  hostAway?: string
  /** What the phone's folded terminal bar says the agent is doing: the attention's message, else the agent's name. */
  barText?: string
  readOnlyBadge?: boolean
}>()
const tabs = defineModel<TabsState>('tabs', { required: true })

const phone = useIsPhone()
const column = ref<HTMLElement>()
const split = ref(0.6)
const editorOpen = computed(() => tabs.value.tabs.length > 0 && !tabs.value.folded)
const terminalFolded = computed(() => phone.value && editorOpen.value)

let dragging = false
function splitDown(e: PointerEvent) {
  dragging = true
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}
function splitMove(e: PointerEvent) {
  if (!dragging || !column.value) return
  const r = column.value.getBoundingClientRect()
  if (r.height > 0) split.value = clampSplit((e.clientY - r.top) / r.height)
}
function splitUp(e: PointerEvent) {
  if (!dragging) return
  dragging = false
  ;(e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId)
  writeSplit(typeof localStorage === 'undefined' ? null : localStorage, split.value)
}
onMounted(() => {
  split.value = readSplit(typeof localStorage === 'undefined' ? null : localStorage)
})

function bringTerminalUp() {
  tabs.value = toggleFold(tabs.value)
}
</script>

<template>
  <div ref="column" class="flex flex-1 min-w-0 flex-col gap-3" data-editor-column>
    <EditorArea
      v-model:tabs="tabs"
      :request="request"
      :cwd="cwd"
      :raw-url="rawUrl"
      :host-away="hostAway"
      :read-only-badge="readOnlyBadge"
      :class="editorOpen ? (terminalFolded ? 'min-h-0 flex-1' : 'min-h-32 shrink') : 'flex-none'"
      :style="editorOpen && !terminalFolded ? { flexBasis: `${Math.round(split * 100)}%` } : undefined"
    />
    <div v-if="editorOpen && !terminalFolded" class="-my-2 flex h-2 flex-none cursor-row-resize items-center justify-center touch-none" data-editor-split @pointerdown="splitDown" @pointermove="splitMove" @pointerup="splitUp" @pointercancel="splitUp">
      <span class="h-0.5 w-10 rounded-full bg-accented" />
    </div>
    <div class="relative" :class="terminalFolded ? 'h-0 flex-none overflow-hidden' : 'flex-1 min-h-32'">
      <slot />
    </div>
    <button v-if="terminalFolded" type="button" class="flex h-11 flex-none items-center gap-2 rounded-md border border-default bg-elevated/60 px-3 text-left text-xs" data-terminal-bar @click="bringTerminalUp">
      <UIcon name="i-lucide-square-terminal" class="size-4 flex-none text-muted" />
      <span class="min-w-0 flex-1 truncate font-medium text-highlighted">{{ barText || 'Terminal' }}</span>
      <span class="flex-none text-muted">tap to bring it up</span>
    </button>
    <slot v-else name="bar" />
  </div>
</template>
