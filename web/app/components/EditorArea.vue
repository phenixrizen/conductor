<script setup lang="ts">
import type { FileHeader, FileRequester, NvimBridge } from '~/utils/protocol'
import { statusLetter, statusTone } from '~/utils/changes'
import { activateTab, closeAllTabs, closeTab, cycleTab, nvimUnavailableWords, openTab, readKeymap, takeLine, toggleFold, writeKeymap, type EditorTab, type Keymap, type TabsState } from '~/utils/editorTabs'
import { modeWords } from '~/utils/nvimKeys'
import { crumbsOf } from '~/utils/fileTree'

/**
 * The editor area (design 4b, 4c, 4f): a tab per file or URL open, the
 * active one in Monaco (or as an image, a binary's note, a URL's preview,
 * the words of a refused read), under a strip of tabs with the fold button
 * (T; Alt+T in the terminal) and × for all; folded, the strip alone stays
 * above the terminal. The header: the breadcrumb from the working
 * directory down to the file, Ln/Col, copy path, open raw. Ctrl+Tab and
 * Ctrl+Shift+Tab switch tabs, Ctrl+W closes the active one.
 */
const props = defineProps<{
  request: FileRequester
  /** The editor's Neovim, when the page offers it (design round 12, F8): the keymap switch shows then. */
  nvim?: NvimBridge
  cwd?: string
  rawUrl?: (path: string) => string | null
  /** The name of a hosted session's machine while it is away: its files cannot be read until it returns. */
  hostAway?: string
  /** A view-only guest: the editor says Read only where a controller will see Save. */
  readOnlyBadge?: boolean
}>()
const tabs = defineModel<TabsState>('tabs', { required: true })

interface Loaded {
  state: 'loading' | 'text' | 'image' | 'binary' | 'refused' | 'error' | 'url' | 'diff'
  header?: FileHeader
  text?: string
  imageSrc?: string
  dims?: string
  error?: string
  /** A diff: the base's version and the working directory's. */
  original?: string
  modified?: string
}
const inlineDiff = ref(false)
const loaded = reactive(new Map<string, Loaded>())
const pos = ref({ line: 1, col: 1 })
const editorRef = ref<{ showLine: (n: number) => void; find: () => void; gotoLine: () => void; focus: () => void } | null>(null)
const copy = useCopy()

const active = computed<EditorTab | null>(() => tabs.value.tabs.find((t) => t.id === tabs.value.active) ?? null)
const view = computed<Loaded | null>(() => (active.value ? (loaded.get(active.value.id) ?? null) : null))

function fmtSize(n?: number) {
  if (n === undefined) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(1)} MiB`
}

/** A diff tab: the base's version through git show (none for an added file), the working directory's through a read (none for a deleted one). */
async function loadDiff(tab: EditorTab) {
  loaded.set(tab.id, { state: 'loading' })
  const status = tab.meta?.status
  try {
    let original = ''
    let modified = ''
    if (status !== 'A' && status !== '?') {
      const res = await props.request(tab.path, false, { op: 'show', rev: tab.meta?.base || 'HEAD' })
      if (res.header.kind === 'error') {
        loaded.set(tab.id, { state: res.header.error?.code === 'file_denied' ? 'refused' : 'error', header: res.header, error: res.header.error?.message || 'cannot read' })
        return
      }
      original = new TextDecoder().decode(res.body)
    }
    if (status !== 'D') {
      const res = await props.request(tab.path, false)
      if (res.header.kind === 'error') {
        loaded.set(tab.id, { state: res.header.error?.code === 'file_denied' ? 'refused' : 'error', header: res.header, error: res.header.error?.message || 'cannot read' })
        return
      }
      modified = new TextDecoder().decode(res.body)
    }
    loaded.set(tab.id, { state: 'diff', original, modified })
  } catch (e) {
    loaded.set(tab.id, { state: 'error', error: (e as Error).message })
  }
}

async function load(tab: EditorTab) {
  if (tab.kind === 'url') {
    loaded.set(tab.id, { state: 'url' })
    return
  }
  if (tab.kind === 'diff') return loadDiff(tab)
  loaded.set(tab.id, { state: 'loading' })
  try {
    const res = await props.request(tab.path, false)
    const h = res.header
    if (h.kind === 'error') {
      const msg = h.error?.message || 'cannot read'
      loaded.set(tab.id, { state: h.error?.code === 'file_denied' ? 'refused' : 'error', header: h, error: msg })
    } else if (h.kind === 'dir') {
      loaded.set(tab.id, { state: 'error', header: h, error: 'a folder: open it in the Files pane' })
    } else if (h.mime?.startsWith('image/') && res.body.length) {
      let bin = ''
      for (const b of res.body) bin += String.fromCharCode(b)
      const src = `data:${h.mime};base64,${btoa(bin)}`
      loaded.set(tab.id, { state: 'image', header: h, imageSrc: src })
      const img = new Image()
      img.onload = () => {
        const cur = loaded.get(tab.id)
        if (cur?.state === 'image') loaded.set(tab.id, { ...cur, dims: `${img.naturalWidth} × ${img.naturalHeight}` })
      }
      img.src = src
    } else if (h.binary) {
      loaded.set(tab.id, { state: 'binary', header: h })
    } else {
      loaded.set(tab.id, { state: 'text', header: h, text: new TextDecoder().decode(res.body) })
    }
  } catch (e) {
    loaded.set(tab.id, { state: 'error', error: (e as Error).message })
  }
}

watch(
  active,
  (t) => {
    if (t && !loaded.has(t.id)) load(t)
  },
  { immediate: true },
)

/** A tab closed drops what was loaded for it. */
watch(
  () => tabs.value.tabs.map((t) => t.id),
  (ids) => {
    for (const id of [...loaded.keys()]) if (!ids.includes(id)) loaded.delete(id)
  },
)

/** The line a tab was opened at, once the editor is up. */
function onReady() {
  const t = active.value
  if (t?.line) {
    editorRef.value?.showLine(t.line)
    tabs.value = takeLine(tabs.value, t.id)
  }
}
watch(
  () => active.value?.line,
  (l) => {
    if (l && active.value && view.value?.state === 'text' && editorRef.value) {
      editorRef.value.showLine(l)
      tabs.value = takeLine(tabs.value, active.value.id)
    }
  },
)

const crumbs = computed(() => {
  const t = active.value
  if (!t || t.kind === 'url') return []
  const root = (props.cwd || '').replace(/\/+$/, '')
  const rel = root && t.path.startsWith(root + '/') ? t.path.slice(root.length + 1) : null
  if (rel === null) return t.path.split('/').filter(Boolean).map((label) => ({ label }))
  return [{ label: crumbsOf(root).pop()!.label }, ...rel.split('/').map((label) => ({ label }))]
})

const rawHref = computed(() => (active.value?.kind === 'file' && props.rawUrl ? props.rawUrl(active.value.path) : null))

/** A diff's file, as its own tab. */
function openFileOfDiff() {
  if (active.value?.kind === 'diff') tabs.value = openTab(tabs.value, 'file', active.value.path)
}

function refused(msg: string): { title: string; words: string; icon: string } {
  if (/disabled|off/i.test(msg)) return { icon: 'i-lucide-eye-off', title: 'File viewing is off for this session', words: 'Nothing is read from its working directory, for anyone. The owner can turn it on in Settings.' }
  return { icon: 'i-lucide-lock', title: 'Files are for controllers on this session', words: 'This link is view only. Ask for control, or for a controller to open it.' }
}

function reload() {
  if (active.value) load(active.value)
}

// The keymap (design round 12, F8): Monaco's keys, or the real Neovim on the
// session's machine, chosen with the button and kept per browser. Off unless
// chosen: nobody who never presses it meets a Vim key.
const keymap = ref<Keymap>('default')
onMounted(() => {
  keymap.value = readKeymap(typeof localStorage === 'undefined' ? null : localStorage)
})
function setKeymap(k: Keymap) {
  keymap.value = k
  writeKeymap(typeof localStorage === 'undefined' ? null : localStorage, k)
}
const nvimWhy = computed(() => (props.nvim ? nvimUnavailableWords(props.nvim.offer) : 'Neovim is not offered on this page'))
const nvimOn = computed(() => keymap.value === 'nvim' && !!props.nvim && nvimWhy.value === '')
const nvimState = ref<{ mode: string; cmdline: string; message: string; messageKind: string } | null>(null)
const keymapTip = computed(() => {
  if (keymap.value === 'nvim') return nvimWhy.value ? `${nvimWhy.value} · Monaco's keys meanwhile · click for Monaco's keys` : "Neovim keys: the real Neovim on the session's machine holds the file · click for Monaco's keys"
  return "Monaco's keys · click for Neovim keys (the real Neovim on the session's machine)"
})
function closeActive() {
  if (active.value) tabs.value = closeTab(tabs.value, active.value.id)
}

function onKeydown(e: KeyboardEvent) {
  if (!(e.ctrlKey || e.metaKey)) return
  if (e.key === 'Tab') {
    e.preventDefault()
    tabs.value = cycleTab(tabs.value, e.shiftKey ? -1 : 1)
  } else if (e.key.toLowerCase() === 'w' && !e.shiftKey && !e.altKey) {
    if (!active.value) return
    e.preventDefault()
    tabs.value = closeTab(tabs.value, active.value.id)
  }
}

defineExpose({ find: () => editorRef.value?.find(), gotoLine: () => editorRef.value?.gotoLine() })
</script>

<template>
  <div v-if="tabs.tabs.length" class="flex min-h-0 flex-col overflow-hidden rounded-md border border-default bg-default" :data-editor-area="tabs.folded ? 'folded' : 'open'" tabindex="-1" @keydown="onKeydown">
    <!-- Folded: one line above the terminal, the tabs still there. -->
    <div v-if="tabs.folded" class="flex h-9 items-center gap-2 px-2 text-xs" data-editor-folded>
      <UIcon name="i-lucide-files" class="size-4 flex-none text-muted" />
      <span class="font-medium text-highlighted" data-editor-count>{{ tabs.tabs.length === 1 ? '1 file open' : `${tabs.tabs.length} files open` }}</span>
      <span class="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
        <button v-for="t in tabs.tabs" :key="t.id" type="button" class="truncate rounded px-1.5 py-0.5 font-mono text-[11px] text-muted hover:bg-elevated hover:text-default" :data-editor-folded-tab="t.id" @click="tabs = activateTab(tabs, t.id)">{{ t.title }}</button>
      </span>
      <UTooltip text="Show the editor (T · Alt+T in the terminal)">
        <UButton icon="i-lucide-panel-top" size="xs" color="neutral" variant="ghost" aria-label="Show the editor" data-editor-unfold @click="tabs = toggleFold(tabs)" />
      </UTooltip>
      <UTooltip text="Close all">
        <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" aria-label="Close all files" data-editor-close-all @click="tabs = closeAllTabs()" />
      </UTooltip>
    </div>
    <template v-else>
      <!-- The tab strip. -->
      <div class="flex h-9 flex-none items-stretch border-b border-default bg-elevated/40" data-editor-tabs>
        <div class="flex min-w-0 flex-1 items-stretch overflow-x-auto">
          <div
            v-for="t in tabs.tabs"
            :key="t.id"
            class="group flex max-w-56 flex-none items-center gap-1.5 border-r border-default pl-3 pr-1.5 text-xs"
            :class="t.id === tabs.active ? 'bg-default text-highlighted' : 'text-muted hover:text-default'"
            :data-editor-tab="t.id"
            :data-editor-tab-active="t.id === tabs.active ? '' : undefined"
          >
            <button type="button" class="flex min-w-0 items-center gap-1.5" :title="t.path" @click="tabs = activateTab(tabs, t.id)">
              <UIcon :name="t.kind === 'url' ? 'i-lucide-globe' : t.kind === 'diff' ? 'i-lucide-file-diff' : 'i-lucide-file'" class="size-3.5 flex-none" />
              <span class="truncate font-mono">{{ t.title }}</span>
            </button>
            <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" class="opacity-0 group-hover:opacity-100 data-[active]:opacity-100" :aria-label="`Close ${t.title}`" :data-editor-close="t.id" @click="tabs = closeTab(tabs, t.id)" />
          </div>
        </div>
        <div class="flex flex-none items-center gap-0.5 px-1">
          <UTooltip v-if="nvim" :text="keymapTip">
            <UButton :label="keymap === 'nvim' ? 'Neovim' : 'Keys'" icon="i-lucide-keyboard" size="xs" :color="keymap === 'nvim' ? 'primary' : 'neutral'" :variant="keymap === 'nvim' ? 'soft' : 'ghost'" :aria-label="keymap === 'nvim' ? 'Keymap: Neovim' : 'Keymap: default'" :data-editor-keymap="keymap" @click="setKeymap(keymap === 'nvim' ? 'default' : 'nvim')" />
          </UTooltip>
          <UTooltip text="Terminal only (T · Alt+T in the terminal)">
            <UButton icon="i-lucide-panel-bottom" size="xs" color="neutral" variant="ghost" aria-label="Terminal only" data-editor-fold @click="tabs = toggleFold(tabs)" />
          </UTooltip>
          <UTooltip text="Close all">
            <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" aria-label="Close all files" data-editor-close-all @click="tabs = closeAllTabs()" />
          </UTooltip>
        </div>
      </div>
      <!-- The header: crumbs, the position, the buttons. -->
      <div v-if="active" class="flex h-8 flex-none items-center gap-2 border-b border-default px-3 text-xs">
        <template v-if="active.kind === 'url'">
          <UIcon name="i-lucide-globe" class="size-3.5 flex-none text-muted" />
          <span class="min-w-0 flex-1 truncate font-mono text-muted" data-editor-url>{{ active.path }}</span>
          <UButton label="New tab" icon="i-lucide-external-link" size="xs" color="neutral" variant="ghost" :to="active.path" target="_blank" rel="noopener noreferrer" />
          <UButton icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" aria-label="Refresh" @click="reload" />
          <UButton icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" aria-label="Copy URL" @click="copy(active.path, 'URL copied')" />
        </template>
        <template v-else>
          <nav class="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto font-mono" aria-label="path" data-editor-crumbs>
            <template v-for="(c, i) in crumbs" :key="i">
              <UIcon v-if="i > 0" name="i-lucide-chevron-right" class="size-3 flex-none text-muted" />
              <span :class="i === crumbs.length - 1 ? 'text-highlighted' : 'text-muted'">{{ c.label }}</span>
            </template>
          </nav>
          <template v-if="active.kind === 'diff'">
            <span class="flex-none text-muted" data-editor-against>{{ active.meta?.against || 'working directory vs HEAD' }}</span>
            <UBadge v-if="active.meta?.status" :label="statusLetter(active.meta.status as any)" :color="statusTone(active.meta.status as any)" variant="subtle" size="xs" class="font-mono" />
            <span class="flex-none font-mono text-[11px]"><span class="text-success">+{{ active.meta?.added ?? 0 }}</span> <span class="text-error">−{{ active.meta?.removed ?? 0 }}</span></span>
            <UFieldGroup size="xs">
              <UButton label="Side by side" :variant="inlineDiff ? 'ghost' : 'soft'" color="neutral" data-editor-side-by-side @click="inlineDiff = false" />
              <UButton label="Inline" :variant="inlineDiff ? 'soft' : 'ghost'" color="neutral" data-editor-inline @click="inlineDiff = true" />
            </UFieldGroup>
            <UButton label="Open file" icon="i-lucide-file" size="xs" color="neutral" variant="ghost" data-editor-open-file @click="openFileOfDiff" />
          </template>
          <UBadge v-if="view?.header?.kind === 'file' && view.state !== 'text'" :label="view.dims || fmtSize(view.header.size)" color="neutral" variant="subtle" size="sm" />
          <UBadge v-if="view?.header?.truncated" label="truncated" color="warning" variant="subtle" size="sm" />
          <span v-if="view?.state === 'text'" class="flex-none font-mono text-[11px] text-muted" data-editor-pos>Ln {{ pos.line }}, Col {{ pos.col }}</span>
          <UBadge v-if="readOnlyBadge" label="Read only" icon="i-lucide-lock" color="neutral" variant="subtle" size="sm" data-editor-readonly />
          <UButton icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" aria-label="Copy path" data-editor-copy @click="copy(active.path, 'Path copied')" />
          <UButton v-if="rawHref" icon="i-lucide-file-output" size="xs" color="neutral" variant="ghost" aria-label="Open raw" :to="rawHref" target="_blank" rel="noopener noreferrer" data-editor-raw />
        </template>
      </div>
      <!-- Neovim's status line (design round 12, F8): the mode, the command line as typed, the last message. -->
      <div v-if="keymap === 'nvim' && active?.kind === 'file' && view?.state === 'text'" class="flex h-7 flex-none items-center gap-3 overflow-hidden border-b border-default bg-elevated/40 px-3 font-mono text-[11px]" data-nvim-status :data-nvim-mode="nvimOn ? (nvimState?.mode ?? '') : 'off'">
        <template v-if="nvimOn">
          <span class="flex-none font-semibold text-highlighted" data-nvim-mode-words>{{ modeWords(nvimState?.mode ?? '') }}</span>
          <span v-if="nvimState?.cmdline" class="flex-none text-highlighted" data-nvim-cmdline>{{ nvimState.cmdline }}</span>
          <span v-if="nvimState?.message" class="min-w-0 flex-1 truncate" :class="nvimState.messageKind === 'emsg' ? 'text-error' : 'text-muted'" data-nvim-message>{{ nvimState.message }}</span>
          <span v-if="!nvimState?.cmdline && !nvimState?.message && !modeWords(nvimState?.mode ?? '')" class="text-muted">Neovim · :w writes on {{ nvim?.offer.machine || 'the machine' }} · :q closes the tab</span>
        </template>
        <span v-else class="flex items-center gap-1.5 text-warning" data-editor-keymap-note><UIcon name="i-lucide-info" class="size-3.5" />{{ nvimWhy }} · Monaco's keys meanwhile</span>
      </div>
      <!-- The body. -->
      <div class="relative min-h-0 flex-1" :data-editor-state="hostAway ? 'host-away' : (view?.state ?? 'loading')">
        <div v-if="hostAway" class="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm">
          <UIcon name="i-lucide-laptop" class="size-6 text-muted" />
          <span class="font-medium text-highlighted">{{ hostAway }} is away</span>
          <span class="max-w-md text-muted">Files, changes and saves come from that machine. They return when it does; the terminal's scrollback stays.</span>
        </div>
        <div v-else-if="!view || view.state === 'loading'" class="flex items-center gap-2 p-6 text-sm text-muted"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading…</div>
        <iframe v-else-if="view.state === 'url'" :src="active!.path" class="h-full w-full bg-white" sandbox="allow-scripts allow-same-origin allow-forms allow-popups" referrerpolicy="no-referrer" title="URL preview" />
        <CodeEditor v-else-if="view.state === 'text'" ref="editorRef" :key="`${active!.id}:${nvimOn ? 'nvim' : 'keys'}`" :path="active!.path" :text="view.text ?? ''" :line="active!.line" read-only :nvim="nvimOn ? nvim : undefined" @cursor="pos = $event" @ready="onReady" @nvim="nvimState = $event" @nvim-closed="closeActive" />
        <DiffEditor v-else-if="view.state === 'diff'" :key="active!.id" :path="active!.path" :original="view.original ?? ''" :modified="view.modified ?? ''" :inline="inlineDiff" />
        <div v-else-if="view.state === 'image'" class="flex h-full items-center justify-center overflow-auto p-4 [background-image:linear-gradient(45deg,var(--ui-bg-elevated)_25%,transparent_25%),linear-gradient(-45deg,var(--ui-bg-elevated)_25%,transparent_25%),linear-gradient(45deg,transparent_75%,var(--ui-bg-elevated)_75%),linear-gradient(-45deg,transparent_75%,var(--ui-bg-elevated)_75%)] [background-size:16px_16px] [background-position:0_0,0_8px,8px_-8px,-8px_0]">
          <img :src="view.imageSrc" alt="" class="max-h-full max-w-full" />
        </div>
        <div v-else-if="view.state === 'binary'" class="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm">
          <UIcon name="i-lucide-file-digit" class="size-6 text-muted" />
          <span class="font-medium text-highlighted">Binary file; nothing to show</span>
          <span class="text-muted">{{ fmtSize(view.header?.size) }}. Open raw downloads it.</span>
          <UButton v-if="rawHref" label="Open raw" icon="i-lucide-file-output" size="xs" color="neutral" variant="soft" :to="rawHref" target="_blank" rel="noopener noreferrer" />
        </div>
        <div v-else-if="view.state === 'refused'" class="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm" data-editor-refused>
          <UIcon :name="refused(view.error || '').icon" class="size-6 text-muted" />
          <span class="font-medium text-highlighted">{{ refused(view.error || '').title }}</span>
          <span class="max-w-md text-muted">{{ refused(view.error || '').words }}</span>
        </div>
        <UAlert v-else color="error" variant="subtle" icon="i-lucide-triangle-alert" class="m-3" :title="view.error || 'Cannot read this path'" />
      </div>
    </template>
  </div>
</template>
