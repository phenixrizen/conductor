<script setup lang="ts">
import type { ChatQuote, FileHeader, FileRequester, FileWriter, NvimBridge } from '~/utils/protocol'
import { copyText, makeQuote, quoteLocation, quotePath, rangeWords } from '~/utils/quote'
import { closeWords, conflictWords, forgetModel } from '~/utils/editorModels'
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
  /** Saves a file (design round 12, F6), and whether this connection may edit: Save, Ctrl+S and an editable editor then. */
  write?: FileWriter
  canEdit?: boolean
  /** Comments on lines (design round 12, F7): the page has a chat to post them to; `canAsk`, and the person may ask the agent. */
  commenting?: boolean
  canAsk?: boolean
}>()
const emit = defineEmits<{ comment: [c: { quote: ChatQuote; text: string; toAgent: boolean }] }>()
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
const editorRef = ref<{
  showLine: (n: number) => void
  showRange: (from: number, to: number) => void
  find: () => void
  gotoLine: () => void
  focus: () => void
  getText: () => string
  markClean: () => void
  setText: (t: string) => void
  getLines: () => string[]
} | null>(null)
const copy = useCopy()

const active = computed<EditorTab | null>(() => tabs.value.tabs.find((t) => t.id === tabs.value.active) ?? null)
const view = computed<Loaded | null>(() => (active.value ? (loaded.get(active.value.id) ?? null) : null))

function fmtSize(n?: number) {
  if (n === undefined) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(1)} MiB`
}

/**
 * A diff tab: the base's version through git show (none for an added file), the working directory's through a read (none for a deleted
 * one). A commit's diff (design 4e) shows both sides at their revisions: the parent's (a rename's old path; none for a root commit) and
 * the commit's.
 */
async function loadDiff(tab: EditorTab) {
  loaded.set(tab.id, { state: 'loading' })
  const status = tab.meta?.status
  const rev = tab.meta?.rev
  try {
    let original = ''
    let modified = ''
    if (status !== 'A' && status !== '?' && !(rev && !tab.meta?.base)) {
      const res = await props.request(rev ? tab.meta?.from || tab.path : tab.path, false, { op: 'show', rev: tab.meta?.base || 'HEAD' })
      if (res.header.kind === 'error') {
        loaded.set(tab.id, { state: res.header.error?.code === 'file_denied' ? 'refused' : 'error', header: res.header, error: res.header.error?.message || 'cannot read' })
        return
      }
      original = new TextDecoder().decode(res.body)
    }
    if (status !== 'D') {
      const res = rev ? await props.request(tab.path, false, { op: 'show', rev }) : await props.request(tab.path, false)
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

/** The line (or a quote's range) a tab was opened at, once the editor is up. */
function onReady() {
  const t = active.value
  if (t?.line) {
    if (t.lineTo && t.lineTo > t.line) editorRef.value?.showRange(t.line, t.lineTo)
    else editorRef.value?.showLine(t.line)
    tabs.value = takeLine(tabs.value, t.id)
  }
}
watch(
  () => [active.value?.line, active.value?.lineTo],
  () => {
    const t = active.value
    if (t?.line && view.value?.state === 'text' && editorRef.value) {
      if (t.lineTo && t.lineTo > t.line) editorRef.value.showRange(t.line, t.lineTo)
      else editorRef.value.showLine(t.line)
      tabs.value = takeLine(tabs.value, t.id)
    }
  },
)

// Comments on lines (design round 12, F7): a selection in a file shows a bar (Comment, Ask the agent, Copy; Ctrl+Shift+M and
// Ctrl+Shift+A); the composer under it carries the quote and the words to the page's chat, and with Ask the agent into the agent.
const selection = ref<{ from: number; to: number; top: number; left: number } | null>(null)
const composer = ref<{ ask: boolean; quote: ChatQuote; text: string; top: number; left: number } | null>(null)
const commentable = computed(() => !!props.commenting && active.value?.kind === 'file' && view.value?.state === 'text')
watch(
  () => active.value?.id,
  () => {
    selection.value = null
    composer.value = null
  },
)
function onSelection(s: { from: number; to: number; top: number; left: number } | null) {
  selection.value = s
}
function quoteNow(): ChatQuote | null {
  const t = active.value
  if (!t || !editorRef.value) return null
  const s = selection.value
  const from = s?.from ?? pos.value.line
  const to = s?.to ?? pos.value.line
  return makeQuote(quotePath(t.path, props.cwd), editorRef.value.getLines(), from, to)
}
function openComposer(ask: boolean) {
  if (!commentable.value || (ask && !props.canAsk)) return
  const quote = quoteNow()
  if (!quote) return
  composer.value = { ask, quote, text: '', top: selection.value?.top ?? 40, left: Math.max(8, selection.value?.left ?? 8) }
  nextTick(() => (document.querySelector('[data-editor-composer] textarea') as HTMLTextAreaElement | null)?.focus())
}
function copyLines() {
  const q = quoteNow()
  if (q) copy(copyText(q), 'Lines copied')
}
function sendComment(toAgent: boolean) {
  const c = composer.value
  if (!c) return
  emit('comment', { quote: c.quote, text: c.text.trim(), toAgent })
  composer.value = null
  selection.value = null
  editorRef.value?.focus()
}
function onComposerKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    composer.value = null
    editorRef.value?.focus()
  } else if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    sendComment(!!composer.value?.ask)
  }
}

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
  // Neovim's :q: Neovim kept its own buffer, nothing to ask.
  if (active.value) closeNow([active.value.id])
}

// Editing (design round 12, F6): a controller on a session that allows it edits a text file read whole in Monaco (Neovim's keymap
// edits through Neovim instead); Save and Ctrl+S write it on the session's machine, refused when the file changed on disk since it
// was read (Compare, Reload, Save anyway); a tab with unsaved changes wears a dot and asks before it closes.
const dirty = reactive(new Set<string>())
const saving = ref(false)
const savedFlash = ref(false)
const saveError = ref('')
const conflict = ref<{ tabId: string; header: FileHeader } | null>(null)
const compare = ref<{ tabId: string; disk: string; mine: string } | null>(null)
const closing = ref<{ ids: string[] } | null>(null)
const editable = computed(
  () => !!props.write && !!props.canEdit && !nvimOn.value && active.value?.kind === 'file' && view.value?.state === 'text' && !view.value.header?.truncated && !!view.value.header?.sha256,
)
const activeConflict = computed(() => (conflict.value && conflict.value.tabId === active.value?.id ? conflict.value : null))
const activeCompare = computed(() => (compare.value && compare.value.tabId === active.value?.id ? compare.value : null))
function setDirty(id: string, d: boolean) {
  if (d) dirty.add(id)
  else dirty.delete(id)
}
async function save(force = false) {
  const t = active.value
  const v = view.value
  if (!t || !v || !editable.value || !props.write || saving.value) return
  // From the compare view the editor is not up: its text is the compare's right side, and its model takes the saved text when it returns.
  const fromCompare = !!activeCompare.value
  const text = fromCompare ? activeCompare.value!.mine : (editorRef.value?.getText() ?? v.text ?? '')
  saving.value = true
  saveError.value = ''
  try {
    const res = await props.write(t.path, new TextEncoder().encode(text), { baseSha256: v.header?.sha256, force })
    const h = res.header
    if (h.kind === 'written') {
      loaded.set(t.id, { ...v, text, header: { ...v.header!, sha256: h.sha256, mtime: h.mtime, size: h.size } })
      conflict.value = null
      compare.value = null
      if (fromCompare || !editorRef.value) forgetModel(t.path)
      else editorRef.value.markClean()
      dirty.delete(t.id)
      savedFlash.value = true
      setTimeout(() => (savedFlash.value = false), 2000)
    } else if (h.error?.code === 'changed_on_disk') {
      conflict.value = { tabId: t.id, header: h }
    } else {
      saveError.value = h.error?.message || 'cannot save'
    }
  } catch (e) {
    saveError.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
/** The file as it is on disk now, the unsaved edits dropped. */
async function reloadFromDisk() {
  const t = active.value
  if (!t) return
  const res = await props.request(t.path, false)
  if (res.header.kind !== 'file') {
    saveError.value = res.header.error?.message || 'cannot read the file'
    return
  }
  const text = new TextDecoder().decode(res.body)
  const fromCompare = !!activeCompare.value
  loaded.set(t.id, { state: 'text', header: res.header, text })
  conflict.value = null
  compare.value = null
  // The unsaved edits go: an editor that is up takes the text; one coming back from the compare view reads it fresh.
  if (fromCompare || !editorRef.value) forgetModel(t.path)
  else editorRef.value.setText(text)
  dirty.delete(t.id)
}
/** Disk and the unsaved edits side by side. */
async function openCompare() {
  const t = active.value
  if (!t) return
  const mine = editorRef.value?.getText() ?? view.value?.text ?? ''
  const res = await props.request(t.path, false)
  compare.value = { tabId: t.id, disk: res.header.kind === 'file' ? new TextDecoder().decode(res.body) : '', mine }
}
function closeNow(ids: string[]) {
  let st = tabs.value
  for (const id of ids) {
    const t = st.tabs.find((x) => x.id === id)
    if (t && dirty.has(id)) forgetModel(t.path)
    dirty.delete(id)
    if (conflict.value?.tabId === id) conflict.value = null
    if (compare.value?.tabId === id) compare.value = null
    st = closeTab(st, id)
  }
  tabs.value = st
}
/** Closes a tab, asking first when it has unsaved changes (it comes to the front to be saved). */
function requestClose(id: string) {
  if (!dirty.has(id)) return closeNow([id])
  tabs.value = activateTab(tabs.value, id)
  closing.value = { ids: [id] }
}
function requestCloseAll() {
  const ids = tabs.value.tabs.map((t) => t.id)
  const unsaved = ids.filter((id) => dirty.has(id))
  if (!unsaved.length) {
    tabs.value = closeAllTabs()
    return
  }
  if (unsaved.length === 1) tabs.value = activateTab(tabs.value, unsaved[0]!)
  closing.value = { ids }
}
const closingWords = computed(() => {
  const ids = closing.value?.ids.filter((id) => dirty.has(id)) ?? []
  return closeWords(ids.map((id) => tabs.value.tabs.find((t) => t.id === id)?.title ?? id))
})
const closingOne = computed(() => (closing.value?.ids.filter((id) => dirty.has(id)).length ?? 0) === 1)
async function closingSave() {
  const ids = closing.value?.ids ?? []
  await save()
  if (active.value && !dirty.has(active.value.id)) {
    closing.value = null
    closeNow(ids)
  }
}
function closingDiscard() {
  const ids = closing.value?.ids ?? []
  closing.value = null
  closeNow(ids)
}

function onKeydown(e: KeyboardEvent) {
  if (!(e.ctrlKey || e.metaKey)) return
  if (e.shiftKey && !e.altKey && (e.key.toLowerCase() === 'm' || e.key.toLowerCase() === 'a') && commentable.value) {
    e.preventDefault()
    openComposer(e.key.toLowerCase() === 'a')
    return
  }
  if (e.key.toLowerCase() === 's' && !e.altKey && !e.shiftKey) {
    // Ctrl+S saves (F6); never the page's own Save as.
    e.preventDefault()
    if (editable.value) void save()
    return
  }
  if (e.key === 'Tab') {
    e.preventDefault()
    tabs.value = cycleTab(tabs.value, e.shiftKey ? -1 : 1)
  } else if (e.key.toLowerCase() === 'w' && !e.shiftKey && !e.altKey) {
    if (!active.value) return
    e.preventDefault()
    requestClose(active.value.id)
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
        <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" aria-label="Close all files" data-editor-close-all @click="requestCloseAll()" />
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
              <span v-if="dirty.has(t.id)" class="size-1.5 flex-none rounded-full bg-primary" aria-label="unsaved" data-editor-dirty />
            </button>
            <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" class="opacity-0 group-hover:opacity-100 data-[active]:opacity-100" :aria-label="`Close ${t.title}`" :data-editor-close="t.id" @click="requestClose(t.id)" />
          </div>
        </div>
        <div class="flex flex-none items-center gap-0.5 px-1">
          <UTooltip v-if="nvim" :text="keymapTip">
            <UButton :disabled="!!active && dirty.has(active.id)" :label="keymap === 'nvim' ? 'Neovim' : 'Keys'" icon="i-lucide-keyboard" size="xs" :color="keymap === 'nvim' ? 'primary' : 'neutral'" :variant="keymap === 'nvim' ? 'soft' : 'ghost'" :aria-label="keymap === 'nvim' ? 'Keymap: Neovim' : 'Keymap: default'" :data-editor-keymap="keymap" @click="setKeymap(keymap === 'nvim' ? 'default' : 'nvim')" />
          </UTooltip>
          <UTooltip text="Terminal only (T · Alt+T in the terminal)">
            <UButton icon="i-lucide-panel-bottom" size="xs" color="neutral" variant="ghost" aria-label="Terminal only" data-editor-fold @click="tabs = toggleFold(tabs)" />
          </UTooltip>
          <UTooltip text="Close all">
            <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" aria-label="Close all files" data-editor-close-all @click="requestCloseAll()" />
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
          <UBadge v-if="readOnlyBadge || (view?.state === 'text' && active.kind === 'file' && !editable && !nvimOn)" label="Read only" icon="i-lucide-lock" color="neutral" variant="subtle" size="sm" data-editor-readonly />
          <span v-if="savedFlash" class="flex-none text-[11px] text-success" data-editor-saved>Saved</span>
          <UTooltip v-if="editable" text="Save (Ctrl+S)">
            <UButton label="Save" icon="i-lucide-save" size="xs" :color="active && dirty.has(active.id) ? 'primary' : 'neutral'" :variant="active && dirty.has(active.id) ? 'soft' : 'ghost'" :disabled="!active || !dirty.has(active.id)" :loading="saving" data-editor-save @click="save()" />
          </UTooltip>
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
      <!-- The file changed on disk since it was read (F6): what changed it, and the three ways on. -->
      <div v-if="activeConflict" class="flex flex-none flex-wrap items-center gap-2 border-b border-default bg-warning/10 px-3 py-1.5 text-xs" data-editor-conflict>
        <UIcon name="i-lucide-triangle-alert" class="size-3.5 flex-none text-warning" />
        <span class="min-w-0 flex-1 text-highlighted">{{ conflictWords(activeConflict.header) }}</span>
        <UButton label="Compare" size="xs" color="neutral" variant="soft" data-editor-compare @click="openCompare" />
        <UButton label="Reload" size="xs" color="neutral" variant="soft" data-editor-reload @click="reloadFromDisk" />
        <UButton label="Save anyway" size="xs" color="warning" variant="soft" :loading="saving" data-editor-save-anyway @click="save(true)" />
      </div>
      <div v-if="saveError && !activeConflict" class="flex flex-none items-center gap-2 border-b border-default bg-error/10 px-3 py-1.5 text-xs" data-editor-save-error>
        <UIcon name="i-lucide-circle-x" class="size-3.5 flex-none text-error" /><span class="min-w-0 flex-1">Not saved: {{ saveError }}</span>
        <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" aria-label="Dismiss" @click="saveError = ''" />
      </div>
      <div v-if="activeCompare" class="flex flex-none items-center gap-2 border-b border-default px-3 py-1.5 text-xs" data-editor-comparing>
        <span class="min-w-0 flex-1 text-muted">On disk, left · yours, right</span>
        <UButton label="Back to editing" size="xs" color="neutral" variant="ghost" @click="compare = null" />
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
        <DiffEditor v-else-if="activeCompare" :key="`${active!.id}:compare`" :path="active!.path" :original="activeCompare.disk" :modified="activeCompare.mine" :inline="false" data-editor-compare-view />
        <CodeEditor v-else-if="view.state === 'text'" ref="editorRef" :key="`${active!.id}:${nvimOn ? 'nvim' : 'keys'}`" :path="active!.path" :text="view.text ?? ''" :line="active!.line" :read-only="!editable" :nvim="nvimOn ? nvim : undefined" @cursor="pos = $event" @ready="onReady" @nvim="nvimState = $event" @nvim-closed="closeActive" @dirty="setDirty(active!.id, $event)" @selection="onSelection" />
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
        <!-- Over a selection (F7): Comment, Ask the agent, Copy; the composer under the lines. -->
        <div v-if="commentable && selection && !composer && !activeCompare" class="absolute z-10 flex items-center gap-0.5 rounded-md border border-default bg-default p-0.5 shadow-md" :style="{ top: `${selection.top + 4}px`, left: `${Math.max(8, selection.left)}px` }" data-editor-selection-bar>
          <UButton label="Comment" icon="i-lucide-message-square-plus" size="xs" color="neutral" variant="ghost" data-editor-comment @mousedown.prevent @click="openComposer(false)" />
          <UButton v-if="canAsk" label="Ask the agent" icon="i-lucide-bot" size="xs" color="neutral" variant="ghost" data-editor-ask @mousedown.prevent @click="openComposer(true)" />
          <UButton label="Copy" icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" data-editor-copy-lines @mousedown.prevent @click="copyLines" />
        </div>
        <div v-if="composer" class="absolute z-10 flex w-96 max-w-[calc(100%-16px)] flex-col gap-2 rounded-lg border border-default bg-default p-3 shadow-lg" :style="{ top: `${composer.top + 4}px`, left: `${Math.min(composer.left, 8)}px` }" data-editor-composer :data-editor-composer-mode="composer.ask ? 'ask' : 'comment'">
          <div class="flex items-center gap-2 text-xs">
            <UIcon :name="composer.ask ? 'i-lucide-bot' : 'i-lucide-message-square-plus'" class="size-3.5 flex-none text-muted" />
            <span class="min-w-0 flex-1 truncate font-medium text-highlighted">{{ composer.ask ? 'Ask the agent about' : 'Comment on' }} {{ rangeWords(composer.quote.from, composer.quote.to) }}</span>
            <span class="flex-none truncate font-mono text-[11px] text-muted">{{ quoteLocation(composer.quote) }}</span>
          </div>
          <pre class="max-h-24 overflow-auto rounded border border-default bg-elevated/40 px-2 py-1 font-mono text-[11px] leading-snug" data-editor-composer-quote><template v-for="(l, i) in composer.quote.lines" :key="i"><span class="select-none text-muted">{{ String(composer.quote.from + i).padStart(3, ' ') }}  </span>{{ l }}
</template></pre>
          <UTextarea v-model="composer.text" :rows="2" autoresize :maxrows="6" :placeholder="composer.ask ? 'What should the agent do with these lines?' : 'Say something about these lines'" aria-label="Comment" @keydown="onComposerKey" />
          <div class="flex items-center justify-end gap-1.5">
            <span class="mr-auto text-[11px] text-muted">Enter sends · Esc closes</span>
            <UButton label="Comment" size="xs" :color="composer.ask ? 'neutral' : 'primary'" :variant="composer.ask ? 'ghost' : 'solid'" data-editor-composer-send @click="sendComment(false)" />
            <UButton v-if="canAsk" label="Ask the agent" icon="i-lucide-bot" size="xs" :color="composer.ask ? 'primary' : 'neutral'" :variant="composer.ask ? 'solid' : 'ghost'" data-editor-composer-ask @click="sendComment(true)" />
          </div>
        </div>
      </div>
    </template>
    <UModal :open="!!closing" :title="closingWords.title" :description="closingWords.words" :dismissible="!saving" data-editor-close-prompt @update:open="(o) => !o && (closing = null)">
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton label="Cancel" color="neutral" variant="ghost" @click="closing = null" />
          <UButton label="Don't save" color="error" variant="soft" data-editor-discard @click="closingDiscard" />
          <UButton v-if="closingOne" label="Save" color="primary" :loading="saving" data-editor-close-save @click="closingSave" />
        </div>
      </template>
    </UModal>
  </div>
</template>
