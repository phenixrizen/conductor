<script setup lang="ts">
import type { editor as MonacoEditor } from 'monaco-editor'
import type { NvimBridge, NvimEvent } from '~/utils/protocol'
import { cursorStyleFor, isVisual, keptByConductor, keyToNvim } from '~/utils/nvimKeys'
import { linesEdit } from '~/utils/nvimLines'

/**
 * A file in Monaco (design 4b): the gutter with folding, the minimap, find
 * and replace (Ctrl+F, Ctrl+H), go to line (Ctrl+G), the line asked for
 * shown and marked for a moment. Monaco loads with the first editor on the
 * page (`utils/monaco.ts`, a chunk of its own). Read-only until F6.
 */
const props = withDefaults(defineProps<{ path: string; text: string; line?: number; readOnly?: boolean; nvim?: NvimBridge }>(), { line: undefined, readOnly: true, nvim: undefined })
const emit = defineEmits<{ cursor: [pos: { line: number; col: number }]; ready: []; nvim: [state: { mode: string; cmdline: string; message: string; messageKind: string }]; nvimClosed: [] }>()

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
  model = monaco.editor.getModel(uri) ?? monaco.editor.createModel(props.text, languageFor(props.path), uri)
  if (model.getValue() !== props.text) model.setValue(props.text)
  editor = monaco.editor.create(host.value, {
    model,
    theme: theme.value,
    readOnly: props.readOnly,
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
  emit('cursor', { line: 1, col: 1 })
  if (props.line) showLine(props.line)
  emit('ready')
  if (props.nvim) void startNvim()
}

// The Neovim keymap (design round 12, F8): the real Neovim on the session's
// machine holds the file; every key goes to it, its changes come back into
// the model, its cursor, mode, command line and messages show. Monaco stays
// read-only, so nothing is typed into the text except by Neovim.
let nvimId: string | null = null
let unsubscribe: (() => void) | null = null
const nvimState = { mode: 'n', cmdline: '', message: '', messageKind: '' }
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
  unsubscribe = bridge.subscribe(onNvimEvent)
  editor.updateOptions({ cursorStyle: 'block', cursorBlinking: 'solid' })
  editor.onKeyDown((e) => {
    if (!nvimId || !bridge) return
    const be = e.browserEvent
    if (keptByConductor(be)) return
    const keys = keyToNvim(be)
    if (keys === null) return
    e.preventDefault()
    e.stopPropagation()
    bridge.input(nvimId, keys)
  })
  tellNvim()
}
function onNvimEvent(ev: NvimEvent) {
  if (!nvimId || ev.id !== nvimId || !editor || !model || !monacoRef) return
  const monaco = monacoRef
  switch (ev.kind) {
    case 'lines': {
      const edit = linesEdit({ first: ev.first ?? 0, last: ev.last ?? 0, lines: ev.lines ?? [] }, model.getLineCount(), (n) => model!.getLineLength(n))
      model.applyEdits([{ range: new monaco.Range(edit.range.startLineNumber, edit.range.startColumn, edit.range.endLineNumber, edit.range.endColumn), text: edit.text }])
      break
    }
    case 'cursor': {
      const line = Math.max(1, Math.min(ev.line ?? 1, model.getLineCount()))
      const col = Math.max(1, ev.col ?? 1)
      if (ev.mode && isVisual(ev.mode) && ev.visualLine) {
        const vl = Math.max(1, Math.min(ev.visualLine, model.getLineCount()))
        const vc = Math.max(1, ev.visualCol ?? 1)
        const forward = vl < line || (vl === line && vc <= col)
        editor.setSelection(ev.mode === 'V' ? new monaco.Selection(Math.min(vl, line), 1, Math.max(vl, line), model.getLineMaxColumn(Math.max(vl, line))) : forward ? new monaco.Selection(vl, vc, line, col + 1) : new monaco.Selection(vl, vc + 1, line, col))
      } else {
        editor.setPosition({ lineNumber: line, column: col })
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
    case 'closed':
      nvimId = null
      unsubscribe?.()
      unsubscribe = null
      emit('nvimClosed')
      break
    case 'error':
      nvimState.message = ev.message ?? ev.code ?? 'error'
      nvimState.messageKind = 'emsg'
      tellNvim()
      break
  }
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
    if (nvimId) return // Neovim owns the text now
    if (model && model.getValue() !== t) model.setValue(t)
  },
)

onMounted(mount)
onBeforeUnmount(() => {
  if (nvimId && props.nvim) props.nvim.close(nvimId)
  nvimId = null
  unsubscribe?.()
  unsubscribe = null
  editor?.dispose()
  editor = null
  // The model stays for the tab's next showing; a closed tab's model is dropped by the area.
})

defineExpose({ showLine, find, gotoLine, focus: () => editor?.focus() })
</script>

<template>
  <div ref="host" class="h-full w-full min-h-0" data-code-editor />
</template>

<style>
.conductor-line-target {
  background: color-mix(in oklab, var(--ui-primary) 22%, transparent);
}
</style>
