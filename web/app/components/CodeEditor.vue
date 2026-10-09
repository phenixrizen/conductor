<script setup lang="ts">
import type { editor as MonacoEditor } from 'monaco-editor'
import type { NvimBridge, NvimEvent, NvimSwapChoice } from '~/utils/protocol'
import type { NvimViewState } from '~/utils/nvimSwap'
import { cursorStyleFor, isVisual, keptByConductor, keyToNvim, textToNvim } from '~/utils/nvimKeys'
import { byteColToUtf16, linesEdit } from '~/utils/nvimLines'
import { ECHO_HOLD_MS, NvimEcho, type EchoLines } from '~/utils/nvimEcho'
import { hasModel, isDirty, markSaved } from '~/utils/editorModels'

/**
 * A file in Monaco (design 4b): the gutter with folding, the minimap, find
 * and replace (Ctrl+F, Ctrl+H), go to line (Ctrl+G), the line asked for
 * shown and marked for a moment. Monaco loads with the first editor on the
 * page (`utils/monaco.ts`, a chunk of its own). Read-only until F6.
 */
const props = withDefaults(defineProps<{ path: string; text: string; line?: number; readOnly?: boolean; nvim?: NvimBridge }>(), { line: undefined, readOnly: true, nvim: undefined })
const emit = defineEmits<{
  cursor: [pos: { line: number; col: number }]
  ready: []
  nvim: [state: NvimViewState]
  nvimClosed: []
  dirty: [dirty: boolean]
  /** Lines selected (F7): the range and where below it, in the editor's pixels, a bar can sit; null when nothing is. */
  selection: [sel: { from: number; to: number; top: number; left: number } | null]
}>()

const host = ref<HTMLElement>()
const colorMode = useColorMode()
let editor: MonacoEditor.IStandaloneCodeEditor | null = null
let model: MonacoEditor.ITextModel | null = null
let monacoRef: typeof import('monaco-editor') | null = null
let marks: MonacoEditor.IEditorDecorationsCollection | null = null

const theme = computed(() => (colorMode.value === 'dark' ? 'conductor-dark' : 'conductor-light'))

async function mount() {
  const { loadMonaco, languageFor } = await import('~/utils/monaco')
  const monaco = loadMonaco()
  monacoRef = monaco
  if (!host.value) return
  const uri = monaco.Uri.parse(`conductor://file${props.path}`)
  // A model lives for its tab: unsaved edits (F6) stay across the tab being switched away from and back.
  const existing = monaco.editor.getModel(uri)
  model = existing ?? monaco.editor.createModel(props.text, languageFor(props.path), uri)
  if (!existing || !hasModel(props.path)) {
    if (model.getValue() !== props.text) model.setValue(props.text)
    markSaved(props.path, model.getAlternativeVersionId())
  } else if (!isDirty(props.path, model.getAlternativeVersionId()) && model.getValue() !== props.text) {
    model.setValue(props.text)
    markSaved(props.path, model.getAlternativeVersionId())
  }
  contentSub = model.onDidChangeContent(() => {
    if (nvimId && model) markSaved(props.path, model.getAlternativeVersionId()) // Neovim's buffer, mirrored: Neovim keeps its own
    tellDirty()
  })
  editor = monaco.editor.create(host.value, {
    model,
    theme: theme.value,
    readOnly: props.readOnly,
    // The textarea, not Chromium's EditContext div: the page's single-key shortcuts know a textarea is for typing and pause.
    editContext: false,
    automaticLayout: true,
    minimap: { enabled: true, renderCharacters: false },
    folding: true,
    lineNumbers: 'on',
    glyphMargin: false,
    fontFamily: '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: 13,
    lineHeight: 20,
    scrollBeyondLastLine: false,
    renderLineHighlight: 'line',
    smoothScrolling: true,
    cursorBlinking: 'smooth',
    padding: { top: 8 },
    fixedOverflowWidgets: true,
  })
  editor.onDidChangeCursorPosition((e) => emit('cursor', { line: e.position.lineNumber, col: e.position.column }))
  editor.onDidChangeCursorSelection(() => tellSelection())
  editor.onDidScrollChange(() => tellSelection())
  emit('cursor', { line: 1, col: 1 })
  if (props.line) showLine(props.line)
  tellDirty()
  emit('ready')
  if (props.nvim) void startNvim()
}

// The Neovim keymap (design round 12, F8): the real Neovim on the session's
// machine holds the file; every key goes to it, its changes come back into
// the model, its cursor, mode, command line and messages show. Monaco stays
// read-only, so nothing is typed into the text except by Neovim.
let contentSub: { dispose: () => void } | null = null
function tellDirty() {
  if (model) emit('dirty', isDirty(props.path, model.getAlternativeVersionId()))
}
/** The text now, for a save. */
function getText(): string {
  return model?.getValue() ?? ''
}
/** The text was saved: what the model holds now is the saved version. */
function markClean() {
  if (model) markSaved(props.path, model.getAlternativeVersionId())
  tellDirty()
}
/** The file as read again (a reload): the model takes it, unsaved edits dropped. */
function setText(text: string) {
  if (!model) return
  if (model.getValue() !== text) model.setValue(text)
  markSaved(props.path, model.getAlternativeVersionId())
  tellDirty()
}
let nvimId: string | null = null
let unsubscribe: (() => void) | null = null
// The local echo (round 13, G5): plain characters typed in insert mode show at once, Neovim's acknowledgement settles them.
let echo: NvimEcho | null = null
let holdTimer: ReturnType<typeof setTimeout> | undefined
let guessMarks: MonacoEditor.IEditorDecorationsCollection | null = null
function applyLinesEvent(ev: EchoLines) {
  if (!model || !monacoRef) return
  const edit = linesEdit(ev, model.getLineCount(), (n) => model!.getLineLength(n))
  model.applyEdits([{ range: new monacoRef.Range(edit.range.startLineNumber, edit.range.startColumn, edit.range.endLineNumber, edit.range.endColumn), text: edit.text }])
}
function markGuesses() {
  if (!guessMarks || !monacoRef) return
  const r = echo?.range()
  guessMarks.set(r ? [{ range: new monacoRef.Range(r.line, r.from, r.line, r.to), options: { inlineClassName: 'nvim-guess' } }] : [])
}
function stopEcho() {
  clearTimeout(holdTimer)
  holdTimer = undefined
  echo = null
  guessMarks?.clear()
  guessMarks = null
}
const nvimState: NvimViewState = { mode: 'n', cmdline: '', message: '', messageKind: '', swap: null, recovered: false }
function tellNvim() {
  emit('nvim', { ...nvimState })
}
async function startNvim() {
  const bridge = props.nvim
  if (!bridge || !editor || !model || !monacoRef) return
  try {
    const opened = await bridge.open(props.path)
    if (!editor) return
    nvimId = opened.id ?? null
  } catch (err) {
    nvimState.message = err instanceof Error ? err.message : String(err)
    nvimState.messageKind = 'emsg'
    tellNvim()
    return
  }
  if (!nvimId) return
  const m = model
  echo = new NvimEcho({
    line: (n) => (n >= 1 && n <= m.getLineCount() ? m.getLineContent(n) : ''),
    setLine: (n, text) => {
      if (n >= 1 && n <= m.getLineCount() && monacoRef) m.applyEdits([{ range: new monacoRef.Range(n, 1, n, m.getLineMaxColumn(n)), text }])
    },
    applyLines: applyLinesEvent,
  })
  guessMarks = editor.createDecorationsCollection()
  unsubscribe = bridge.subscribe(onNvimEvent)
  editor.updateOptions({ cursorStyle: 'block', cursorBlinking: 'solid' })
  editor.onKeyDown((e) => {
    if (!nvimId || !bridge) return
    const be = e.browserEvent
    // A key an input method or a dead key is composing with: theirs, not Neovim's (round 13, G4).
    if (be.isComposing || be.keyCode === 229) return
    if (keptByConductor(be)) return
    const keys = keyToNvim(be)
    if (keys === null) return
    e.preventDefault()
    e.stopPropagation()
    sendKeys(bridge, keys)
  })
  listenForText(bridge)
  tellNvim()
}

/** Sends keys with their number; a plain character in insert mode shows at once (the local echo). */
function sendKeys(bridge: NvimBridge, keys: string) {
  if (!nvimId) return
  const at = editor?.getPosition()
  const r = echo ? echo.send(keys, { line: at?.lineNumber ?? 1, col: at?.column ?? 1 }) : { seq: 0 }
  bridge.input(nvimId, keys, r.seq || undefined)
  if (r.cursor && editor) {
    editor.setPosition({ lineNumber: r.cursor.line, column: r.cursor.col })
    markGuesses()
  }
}

// Text with no key press (round 13, G4): an input method's composed word, a dead key's character, dictation. Taken on the way down to
// Monaco's text area (the capture phase on the editor's own element), so Monaco, read-only here, never sees it, and sent to Neovim as keys.
let stopText: (() => void) | null = null
function listenForText(bridge: NvimBridge) {
  const dom = editor?.getDomNode()
  if (!dom) return
  let composing = false
  const send = (text: string) => {
    if (!nvimId) return
    for (const keys of textToNvim(text)) sendKeys(bridge, keys)
  }
  const clear = (t: EventTarget | null) => {
    if (t instanceof HTMLTextAreaElement) t.value = ''
  }
  const onStart = (e: Event) => {
    composing = true
    e.stopPropagation()
  }
  const onUpdate = (e: Event) => e.stopPropagation()
  const onEnd = (e: Event) => {
    composing = false
    e.stopPropagation()
    const data = (e as CompositionEvent).data
    if (data) send(data)
    clear(e.target)
  }
  const onBeforeInput = (e: Event) => {
    const ie = e as InputEvent
    if (composing || ie.isComposing || ie.inputType !== 'insertText' || !ie.data) return
    ie.preventDefault()
    ie.stopPropagation()
    send(ie.data)
  }
  const onInput = (e: Event) => {
    // Monaco is read-only here: what reaches its text area is not its to type (a composition's text waits for its end).
    e.stopPropagation()
    if (!composing) clear(e.target)
  }
  const pairs: Array<[string, (e: Event) => void]> = [
    ['compositionstart', onStart],
    ['compositionupdate', onUpdate],
    ['compositionend', onEnd],
    ['beforeinput', onBeforeInput],
    ['input', onInput],
  ]
  for (const [type, fn] of pairs) dom.addEventListener(type, fn, true)
  stopText = () => {
    for (const [type, fn] of pairs) dom.removeEventListener(type, fn, true)
    stopText = null
  }
}
function onNvimEvent(ev: NvimEvent) {
  if (!nvimId || ev.id !== nvimId || !editor || !model || !monacoRef) return
  const monaco = monacoRef
  switch (ev.kind) {
    case 'lines': {
      const lev = { first: ev.first ?? 0, last: ev.last ?? 0, lines: ev.lines ?? [] }
      if (echo?.lines(lev)) {
        // Guesses show: the change waits for its acknowledgement, which comes right after it, so the two settle at once.
        holdTimer ??= setTimeout(() => {
          holdTimer = undefined
          echo?.timeout()
          markGuesses()
        }, ECHO_HOLD_MS)
        break
      }
      applyLinesEvent(lev)
      break
    }
    case 'cursor': {
      // Neovim counts a line's bytes, Monaco its UTF-16 units.
      const toUtf16 = (l: number, c: number) => byteColToUtf16(model!.getLineContent(Math.max(1, Math.min(l, model!.getLineCount()))), Math.max(1, c))
      const shown = echo ? echo.cursor({ line: ev.line ?? 1, col: ev.col ?? 1, mode: ev.mode ?? nvimState.mode, ack: ev.ack }, toUtf16) : null
      if (echo && !echo.holding && holdTimer) {
        clearTimeout(holdTimer)
        holdTimer = undefined
      }
      markGuesses()
      const line = Math.max(1, Math.min(ev.line ?? 1, model.getLineCount()))
      const col = toUtf16(line, ev.col ?? 1)
      if (echo && !shown) {
        // A guess is ahead of Neovim's cursor: the cursor stays after it.
      } else if (ev.mode && isVisual(ev.mode) && ev.visualLine) {
        const vl = Math.max(1, Math.min(ev.visualLine, model.getLineCount()))
        const vc = byteColToUtf16(model.getLineContent(vl), Math.max(1, ev.visualCol ?? 1))
        const forward = vl < line || (vl === line && vc <= col)
        editor.setSelection(ev.mode === 'V' ? new monaco.Selection(Math.min(vl, line), 1, Math.max(vl, line), model.getLineMaxColumn(Math.max(vl, line))) : forward ? new monaco.Selection(vl, vc, line, col + 1) : new monaco.Selection(vl, vc + 1, line, col))
      } else {
        const at = shown ?? { line, col }
        editor.setPosition({ lineNumber: at.line, column: at.col })
      }
      editor.revealPositionInCenterIfOutsideViewport({ lineNumber: line, column: col })
      if (ev.mode && ev.mode !== nvimState.mode) {
        nvimState.mode = ev.mode
        editor.updateOptions({ cursorStyle: cursorStyleFor(ev.mode) })
        tellNvim()
      }
      break
    }
    case 'mode':
      if (ev.mode) {
        echo?.setMode(ev.mode)
        nvimState.mode = ev.mode
        editor.updateOptions({ cursorStyle: cursorStyleFor(ev.mode) })
        tellNvim()
      }
      break
    case 'cmdline':
      nvimState.cmdline = ev.show ? `${ev.prompt ?? ''}${ev.content ?? ''}` : ''
      tellNvim()
      break
    case 'message':
      nvimState.message = ev.messageKind === 'clear' ? '' : (ev.text ?? '')
      nvimState.messageKind = ev.messageKind ?? ''
      tellNvim()
      break
    case 'written':
      nvimState.message = nvimState.message || 'written'
      tellNvim()
      break
    case 'swap':
      // Another editor's swap file: the file opened read-only (round 13, G3).
      nvimState.swap = ev.swap ?? null
      nvimState.recovered = false
      tellNvim()
      break
    case 'closed':
      nvimId = null
      unsubscribe?.()
      unsubscribe = null
      stopText?.()
      stopEcho()
      emit('nvimClosed')
      break
    case 'error':
      nvimState.message = ev.message ?? ev.code ?? 'error'
      nvimState.messageKind = 'emsg'
      tellNvim()
      break
  }
}

/** The selection, as the bar over it needs it (F7): whole lines, from..to, and the spot below its last line. */
function tellSelection() {
  if (!editor || !model || nvimId) return
  const s = editor.getSelection()
  if (!s || s.isEmpty()) return emit('selection', null)
  let to = s.endLineNumber
  if (s.endColumn === 1 && to > s.startLineNumber) to-- // a selection ending at a line's start ends on the line before
  const at = editor.getScrolledVisiblePosition({ lineNumber: to, column: 1 })
  if (!at) return emit('selection', null)
  emit('selection', { from: s.startLineNumber, to, top: at.top + at.height, left: at.left })
}
/** The file's lines as the editor holds them. */
function getLines(): string[] {
  return model?.getLinesContent() ?? []
}
/** Shows lines from..to: in the middle, the cursor on the first, tinted for a moment (a quote opened, F7). */
function showRange(from: number, to: number) {
  if (!editor || !monacoRef) return
  const count = model?.getLineCount() ?? to
  const a = Math.max(1, Math.min(from, count))
  const b = Math.max(a, Math.min(to, count))
  editor.revealLinesInCenter(a, b)
  editor.setPosition({ lineNumber: a, column: 1 })
  marks?.clear()
  marks = editor.createDecorationsCollection([{ range: new monacoRef.Range(a, 1, b, 1), options: { isWholeLine: true, className: 'conductor-line-target' } }])
  setTimeout(() => marks?.clear(), 2500)
}
/** Goes to a line: shown in the middle, the cursor on it, tinted for a moment. */
function showLine(line: number) {
  if (!editor || !monacoRef) return
  const n = Math.max(1, Math.min(line, model?.getLineCount() ?? line))
  editor.revealLineInCenter(n)
  editor.setPosition({ lineNumber: n, column: 1 })
  marks?.clear()
  marks = editor.createDecorationsCollection([{ range: new monacoRef.Range(n, 1, n, 1), options: { isWholeLine: true, className: 'conductor-line-target' } }])
  setTimeout(() => marks?.clear(), 2500)
}

function find() {
  editor?.focus()
  editor?.trigger('conductor', 'actions.find', null)
}

function gotoLine() {
  editor?.focus()
  editor?.trigger('conductor', 'editor.action.gotoLine', null)
}

watch(theme, (t) => monacoRef?.editor.setTheme(t))
watch(() => props.line, (l) => l && showLine(l))
watch(
  () => props.text,
  (t) => {
    if (nvimId || !model) return // Neovim owns the text now
    if (isDirty(props.path, model.getAlternativeVersionId())) return // the person's unsaved edits stay
    if (model.getValue() !== t) {
      model.setValue(t)
      markSaved(props.path, model.getAlternativeVersionId())
    }
  },
)

onMounted(mount)
onBeforeUnmount(() => {
  if (nvimId && props.nvim) props.nvim.close(nvimId)
  nvimId = null
  unsubscribe?.()
  unsubscribe = null
  stopText?.()
  stopEcho()
  contentSub?.dispose()
  contentSub = null
  editor?.dispose()
  editor = null
  // The model stays for the tab's next showing; a closed tab's model is dropped by the area.
})

watch(
  () => props.readOnly,
  (ro) => editor?.updateOptions({ readOnly: ro }),
)
/** Answers the swap file the editor found: recover keeps the banner for deleting it after the write; the others end it. */
function answerSwap(choice: NvimSwapChoice) {
  if (!nvimId || !props.nvim || !nvimState.swap) return
  props.nvim.swap(nvimId, choice)
  if (choice === 'recover') nvimState.recovered = true
  else nvimState.swap = null
  tellNvim()
  editor?.focus()
}
/** Answers Neovim's confirm question with its choice's key; the question goes. */
function answerConfirm(key: string) {
  if (!nvimId || !props.nvim || nvimState.messageKind !== 'confirm') return
  props.nvim.input(nvimId, key)
  nvimState.message = ''
  nvimState.messageKind = ''
  tellNvim()
  editor?.focus()
}
defineExpose({ showLine, showRange, find, gotoLine, focus: () => editor?.focus(), getText, markClean, setText, getLines, answerSwap, answerConfirm })
</script>

<template>
  <div ref="host" class="h-full w-full min-h-0" data-code-editor />
</template>

<style>
/* A local echo's guess (round 13, G5): underlined only once it has waited 100 ms for Neovim, so a fast link shows nothing. */
.nvim-guess {
  text-decoration: underline dotted transparent;
  text-underline-offset: 3px;
  animation: nvim-guess-waits 0s linear 100ms forwards;
}
@keyframes nvim-guess-waits {
  to {
    text-decoration-color: var(--ui-text-muted);
  }
}
.conductor-line-target {
  background: color-mix(in oklab, var(--ui-primary) 22%, transparent);
}
</style>
