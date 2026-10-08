<script setup lang="ts">
import type { editor as MonacoEditor } from 'monaco-editor'

/**
 * A file's changes in Monaco's diff editor (design 4d): the base's version
 * beside the working directory's, side by side or inline, read-only. Loads
 * Monaco with the first editor on the page (`utils/monaco.ts`).
 */
const props = withDefaults(defineProps<{ path: string; original: string; modified: string; inline?: boolean }>(), { inline: false })

const host = ref<HTMLElement>()
const colorMode = useColorMode()
let editor: MonacoEditor.IStandaloneDiffEditor | null = null
let monacoRef: typeof import('monaco-editor') | null = null
let models: { original: MonacoEditor.ITextModel; modified: MonacoEditor.ITextModel } | null = null

const theme = computed(() => (colorMode.value === 'dark' ? 'conductor-dark' : 'conductor-light'))

async function mount() {
  const { loadMonaco, languageFor } = await import('~/utils/monaco')
  const monaco = loadMonaco()
  monacoRef = monaco
  if (!host.value) return
  const lang = languageFor(props.path)
  models = { original: monaco.editor.createModel(props.original, lang), modified: monaco.editor.createModel(props.modified, lang) }
  editor = monaco.editor.createDiffEditor(host.value, {
    theme: theme.value,
    readOnly: true,
    originalEditable: false,
    renderSideBySide: !props.inline,
    automaticLayout: true,
    minimap: { enabled: false },
    fontFamily: '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: 13,
    lineHeight: 20,
    scrollBeyondLastLine: false,
    renderOverviewRuler: true,
    useInlineViewWhenSpaceIsLimited: false,
  })
  editor.setModel(models)
}

watch(theme, (t) => monacoRef?.editor.setTheme(t))
watch(() => props.inline, (v) => editor?.updateOptions({ renderSideBySide: !v }))
watch(
  () => [props.original, props.modified],
  ([o, m]) => {
    if (models && models.original.getValue() !== o) models.original.setValue(o!)
    if (models && models.modified.getValue() !== m) models.modified.setValue(m!)
  },
)

onMounted(mount)
onBeforeUnmount(() => {
  editor?.dispose()
  models?.original.dispose()
  models?.modified.dispose()
  editor = null
  models = null
})
</script>

<template>
  <div ref="host" class="h-full w-full min-h-0" data-diff-editor />
</template>
