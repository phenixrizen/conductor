<script setup lang="ts">
import type { editor as MonacoEditor } from 'monaco-editor'

/**
 * A file in Monaco (design 4b): the gutter with folding, the minimap, find
 * and replace (Ctrl+F, Ctrl+H), go to line (Ctrl+G), the line asked for
 * shown and marked for a moment. Monaco loads with the first editor on the
 * page (`utils/monaco.ts`, a chunk of its own). Read-only until F6.
 */
const props = withDefaults(defineProps<{ path: string; text: string; line?: number; readOnly?: boolean }>(), { line: undefined, readOnly: true })
const emit = defineEmits<{ cursor: [pos: { line: number; col: number }]; ready: [] }>()

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
    if (model && model.getValue() !== t) model.setValue(t)
  },
)

onMounted(mount)
onBeforeUnmount(() => {
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
