<script setup lang="ts">
import type { FileEntry, FileHeader, FileResponse } from '~/utils/protocol'

export interface FileTarget {
  path: string
  line?: number
}

const props = defineProps<{
  request: (path: string, stat?: boolean) => Promise<FileResponse>
  /** Session working directory, used for breadcrumbs. */
  cwd?: string
  /** Builds a raw download URL for server sessions; null when unavailable. */
  rawUrl?: (path: string) => string | null
}>()

const open = defineModel<boolean>('open', { default: false })
const target = defineModel<FileTarget | null>('target', { default: null })
const url = defineModel<string | null>('url', { default: null })

const toast = useToast()
const loading = ref(false)
const header = ref<FileHeader | null>(null)
const text = ref('')
const imageSrc = ref('')
const html = ref('')
const errorText = ref('')
const targetLine = ref<number | undefined>()
const content = ref<HTMLElement>()

const HIGHLIGHT_LIMIT = 200 * 1024

const langByExt: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'tsx', js: 'javascript', jsx: 'jsx', mjs: 'javascript', cjs: 'javascript', vue: 'vue', py: 'python',
  rs: 'rust', java: 'java', kt: 'kotlin', rb: 'ruby', php: 'php', c: 'c', h: 'c', cc: 'cpp', cpp: 'cpp', hpp: 'cpp', cs: 'csharp',
  swift: 'swift', json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'toml', md: 'markdown', sh: 'bash', bash: 'bash', zsh: 'bash',
  sql: 'sql', css: 'css', scss: 'scss', html: 'html', xml: 'xml', dockerfile: 'dockerfile', makefile: 'makefile', proto: 'proto',
  graphql: 'graphql', tf: 'hcl', svelte: 'svelte', lock: 'yaml', mod: 'go-mod', sum: 'text', ini: 'ini', cfg: 'ini', conf: 'ini', env: 'dotenv',
}

const mode = computed<'url' | 'file' | 'dir' | 'error' | 'empty'>(() => {
  if (url.value) return 'url'
  if (errorText.value) return 'error'
  if (!header.value) return 'empty'
  return header.value.kind === 'dir' ? 'dir' : header.value.kind === 'file' ? 'file' : 'error'
})

const title = computed(() => {
  if (url.value) return url.value
  return header.value?.path || target.value?.path || 'File'
})

const crumbs = computed(() => {
  const p = header.value?.path
  if (!p) return []
  const root = props.cwd?.replace(/\/$/, '') || ''
  const rel = root && p.startsWith(root) ? p.slice(root.length) : p
  const parts = rel.split('/').filter(Boolean)
  const out: Array<{ label: string; path: string }> = [{ label: root ? root.split('/').pop() || '/' : '/', path: root || '/' }]
  let acc = root
  for (const part of parts) {
    acc = `${acc}/${part}`
    out.push({ label: part, path: acc })
  }
  return out
})

const lines = computed(() => (html.value ? [] : text.value.split('\n')))

function fmtSize(n?: number) {
  if (n === undefined) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(1)} MiB`
}

function escapeHtml(s: string) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

async function highlight(source: string, path: string, line?: number) {
  html.value = ''
  const ext = (path.split('.').pop() || '').toLowerCase()
  const base = (path.split('/').pop() || '').toLowerCase()
  const lang = langByExt[base] || langByExt[ext]
  if (!lang || source.length > HIGHLIGHT_LIMIT) return
  try {
    const shiki = await import('shiki')
    const dark = document.documentElement.classList.contains('dark')
    html.value = await shiki.codeToHtml(source, {
      lang,
      theme: dark ? 'github-dark' : 'github-light',
      transformers: [
        {
          pre(node) {
            node.properties.style = ''
            node.properties.class = 'shiki'
          },
          line(node, n) {
            node.properties['data-line'] = n
            node.children.unshift({ type: 'element', tagName: 'span', properties: { class: 'line-no' }, children: [{ type: 'text', value: String(n) }] })
            if (line === n) node.properties.class = `${node.properties.class ?? ''} target`.trim()
            const cls = String(node.properties.class ?? '')
            node.properties.class = cls.includes('line') ? cls : `line ${cls}`.trim()
          },
        },
      ],
    })
  } catch {
    html.value = ''
  }
}

async function load(path: string, line?: number) {
  loading.value = true
  errorText.value = ''
  imageSrc.value = ''
  html.value = ''
  text.value = ''
  targetLine.value = line
  try {
    const res = await props.request(path, false)
    header.value = res.header
    if (res.header.kind === 'error') {
      errorText.value = res.header.error?.message || 'cannot read'
    } else if (res.header.kind === 'file') {
      if (res.header.mime?.startsWith('image/') && res.body.length) {
        let bin = ''
        for (const b of res.body) bin += String.fromCharCode(b)
        imageSrc.value = `data:${res.header.mime};base64,${btoa(bin)}`
      } else if (!res.header.binary) {
        text.value = new TextDecoder().decode(res.body)
        await highlight(text.value, res.header.path, line)
      }
    }
  } catch (e) {
    errorText.value = (e as Error).message
  } finally {
    loading.value = false
    await nextTick()
    scrollToTarget()
  }
}

function scrollToTarget() {
  const el = content.value?.querySelector('.line.target') as HTMLElement | null
  el?.scrollIntoView({ block: 'center' })
}

function openEntry(entry: FileEntry) {
  const base = header.value?.path?.replace(/\/$/, '') || ''
  target.value = { path: `${base}/${entry.name}` }
}

function openCrumb(path: string) {
  target.value = { path }
}

async function copyPath() {
  try {
    await navigator.clipboard.writeText(header.value?.path || target.value?.path || '')
    toast.add({ title: 'Path copied', icon: 'i-lucide-clipboard-check', color: 'success' })
  } catch {
    toast.add({ title: 'Copy failed', color: 'warning' })
  }
}

function refresh() {
  if (url.value) {
    const u = url.value
    url.value = null
    nextTick(() => (url.value = u))
  } else if (target.value) load(target.value.path, target.value.line)
}

watch(
  () => target.value,
  (t) => {
    if (t) {
      url.value = null
      open.value = true
      load(t.path, t.line)
    }
  },
  { deep: true },
)

watch(
  () => url.value,
  (u) => {
    if (u) {
      header.value = null
      errorText.value = ''
      open.value = true
    }
  },
)

const rawHref = computed(() => (header.value?.kind === 'file' && props.rawUrl ? props.rawUrl(header.value.path) : null))
</script>

<template>
  <USlideover v-model:open="open" side="right" :ui="{ content: 'w-full max-w-4xl', body: 'p-0 flex flex-col min-h-0' }" :title="title" :description="mode === 'url' ? 'Sandboxed preview. Sites that forbid embedding stay blank; use the new-tab button.' : undefined">
    <template #body>
      <div class="flex items-center gap-2 border-b border-default px-3 py-2 text-xs">
        <template v-if="mode === 'url'">
          <UIcon name="i-lucide-globe" class="size-4 text-muted" />
          <span class="truncate flex-1 font-mono">{{ url }}</span>
          <UButton label="New tab" icon="i-lucide-external-link" size="xs" color="neutral" variant="soft" :to="url!" target="_blank" rel="noopener noreferrer" />
        </template>
        <template v-else>
          <nav class="flex items-center gap-1 flex-1 min-w-0 overflow-x-auto" aria-label="path">
            <template v-for="(c, i) in crumbs" :key="c.path">
              <UIcon v-if="i > 0" name="i-lucide-chevron-right" class="size-3 text-muted flex-none" />
              <button type="button" class="font-mono hover:underline truncate" :class="i === crumbs.length - 1 ? 'text-highlighted' : 'text-muted'" @click="openCrumb(c.path)">
                {{ c.label }}
              </button>
            </template>
          </nav>
          <UBadge v-if="header?.kind === 'file'" :label="fmtSize(header.size)" color="neutral" variant="subtle" size="sm" />
          <UBadge v-if="header?.truncated" label="truncated" color="warning" variant="subtle" size="sm" />
          <UBadge v-if="header?.binary" label="binary" color="neutral" variant="subtle" size="sm" />
          <UButton icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" aria-label="Copy path" @click="copyPath" />
          <UButton v-if="rawHref" icon="i-lucide-file-output" size="xs" color="neutral" variant="ghost" aria-label="Open raw" :to="rawHref" target="_blank" rel="noopener noreferrer" />
        </template>
        <UButton icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" aria-label="Refresh" :loading="loading" @click="refresh" />
      </div>

      <div ref="content" class="file-view flex-1 min-h-0 overflow-auto">
        <div v-if="loading" class="p-6 text-sm text-muted flex items-center gap-2">
          <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading…
        </div>

        <iframe
          v-else-if="mode === 'url'"
          :src="url!"
          class="w-full h-full min-h-[70vh] bg-white"
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
          referrerpolicy="no-referrer"
          title="URL preview"
        />

        <UAlert v-else-if="mode === 'error'" color="error" variant="subtle" icon="i-lucide-triangle-alert" class="m-3" :title="errorText || header?.error?.message || 'Cannot read this path'" />

        <ul v-else-if="mode === 'dir'" class="divide-y divide-default">
          <li v-if="!header?.entries?.length" class="px-4 py-3 text-sm text-muted">Empty directory</li>
          <li v-for="entry in header?.entries" :key="entry.name">
            <button type="button" class="flex w-full items-center gap-2 px-4 py-1.5 text-left text-sm hover:bg-elevated" @click="openEntry(entry)">
              <UIcon :name="entry.dir ? 'i-lucide-folder' : 'i-lucide-file'" class="size-4" :class="entry.dir ? 'text-primary' : 'text-muted'" />
              <span class="font-mono flex-1 truncate">{{ entry.name }}</span>
              <span v-if="!entry.dir" class="text-xs text-muted">{{ fmtSize(entry.size) }}</span>
            </button>
          </li>
        </ul>

        <template v-else-if="mode === 'file'">
          <img v-if="imageSrc" :src="imageSrc" alt="" class="max-w-full p-3" />
          <p v-else-if="header?.binary" class="p-6 text-sm text-muted">Binary file; nothing to show.</p>
          <div v-else-if="html" class="p-3" v-html="html" />
          <pre v-else class="p-3 font-mono"><div v-for="(l, i) in lines" :key="i" class="line" :class="{ target: targetLine === i + 1 }"><span class="line-no">{{ i + 1 }}</span><span class="line-code" v-html="escapeHtml(l) || ' '" /></div></pre>
        </template>

        <p v-else class="p-6 text-sm text-muted">Click a file path or URL in the terminal to open it here.</p>
      </div>
    </template>
  </USlideover>
</template>

<style scoped>
.file-view :deep(.shiki) {
  background: transparent !important;
  padding: 0;
  font-size: 0.8125rem;
  line-height: 1.45;
}
.file-view :deep(.shiki code) {
  display: block;
}
.file-view :deep(.shiki .line) {
  display: flex;
  white-space: pre;
}
.file-view :deep(.shiki .line.target) {
  background: color-mix(in oklab, var(--ui-primary) 18%, transparent);
}
.file-view :deep(.shiki .line-no) {
  user-select: none;
  width: 3.5rem;
  flex: none;
  text-align: right;
  padding-right: 0.75rem;
  opacity: 0.45;
}
</style>
