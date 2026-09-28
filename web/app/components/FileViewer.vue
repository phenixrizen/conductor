<script setup lang="ts">
import type { FileResponse } from '~/utils/protocol'
import type { FileTarget } from './FileBrowser.vue'

export type { FileTarget } from './FileBrowser.vue'

/** Slideover wrapper around FileBrowser for pages without an inspector (wall focus, join). */
defineProps<{
  request: (path: string, stat?: boolean) => Promise<FileResponse>
  cwd?: string
  rawUrl?: (path: string) => string | null
}>()

const open = defineModel<boolean>('open', { default: false })
const target = defineModel<FileTarget | null>('target', { default: null })
const url = defineModel<string | null>('url', { default: null })
const browser = useTemplateRef<{ title: string; mode: string }>('browser')

watch([target, url], ([t, u]) => {
  if (t || u) open.value = true
})
</script>

<template>
  <USlideover
    v-model:open="open"
    side="right"
    :ui="{ content: 'w-full max-w-4xl', body: 'p-0 flex flex-col min-h-0' }"
    :title="browser?.title || target?.path || url || 'File'"
    :description="url ? 'Sandboxed preview. Sites that forbid embedding stay blank; use the new-tab button.' : undefined"
  >
    <template #body>
      <FileBrowser ref="browser" v-model:target="target" v-model:url="url" :request="request" :cwd="cwd" :raw-url="rawUrl" />
    </template>
  </USlideover>
</template>
