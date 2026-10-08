<script setup lang="ts">
import type { FileEntry, FileHeader, FileResponse } from '~/utils/protocol'
import { crumbsOf, findNode, looksLikePath, resolveTyped, rootNode, setChildren, visibleRows, type TreeNode } from '~/utils/fileTree'
import { parseLocation } from '~/utils/links'

export interface FileTarget {
  path: string
  line?: number
}

/**
 * The Files pane (design 4a): the working directory as a tree as soon as
 * the pane shows, folders expanding in place with a listing fetched on the
 * first expansion, the breadcrumb from the top, a box that filters the tree
 * or opens a typed `path:line`, and the hint that paths in the terminal are
 * clickable. A file opens in the pane (until the editor area of design 4b
 * takes it): highlighted, at its line, with its own crumbs, size, copy and
 * raw buttons. A URL the agent printed previews in a sandboxed frame.
 */
const props = defineProps<{
  request: (path: string, stat?: boolean) => Promise<FileResponse>
  /** The session's working directory: the tree's root and the breadcrumb's. */
  cwd?: string
  /** Builds a raw download URL for server sessions; null when unavailable. */
  rawUrl?: (path: string) => string | null
  /** Files open elsewhere (the editor area, design 4b): a file chosen is emitted as `open`, and the pane stays the tree. */
  external?: boolean
}>()
const emit = defineEmits<{ open: [target: FileTarget] }>()

const target = defineModel<FileTarget | null>('target', { default: null })

/** A file to show: in the pane, or wherever the page puts it. */
function openFile(t: FileTarget) {
  if (props.external) emit('open', t)
  else target.value = t
}
const url = defineModel<string | null>('url', { default: null })

const copy = useCopy()
const loading = ref(false)
const header = ref<FileHeader | null>(null)
const text = ref('')
const imageSrc = ref('')
const html = ref('')
const errorText = ref('')
const targetLine = ref<number | undefined>()
const content = ref<HTMLElement>()

// The tree.
const root = ref<TreeNode | null>(null)
const query = ref('')
const treeError = ref('')
const treeLoading = ref(false)
let retries = 0

const HIGHLIGHT_LIMIT = 200 * 1024

const langByExt: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'tsx', js: 'javascript', jsx: 'jsx', mjs: 'javascript', cjs: 'javascript', vue: 'vue', py: 'python',
  rs: 'rust', java: 'java', kt: 'kotlin', rb: 'ruby', php: 'php', c: 'c', h: 'c', cc: 'cpp', cpp: 'cpp', hpp: 'cpp', cs: 'csharp',
  swift: 'swift', json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'toml', md: 'markdown', sh: 'bash', bash: 'bash', zsh: 'bash',
  sql: 'sql', css: 'css', scss: 'scss', html: 'html', xml: 'xml', dockerfile: 'dockerfile', makefile: 'makefile', proto: 'proto',
  graphql: 'graphql', tf: 'hcl', svelte: 'svelte', lock: 'yaml', mod: 'go-mod', sum: 'text', ini: 'ini', cfg: 'ini', conf: 'ini', env: 'dotenv',
}

const mode = computed<'url' | 'file' | 'error' | 'tree'>(() => {
  if (url.value) return 'url'
  if (errorText.value) return 'error'
  if (header.value?.kind === 'file') return 'file'
  return 'tree'
})

const title = computed(() => {
  if (url.value) return url.value
  return header.value?.path || target.value?.path || root.value?.name || 'Files'
})

const rows = computed(() => (root.value ? visibleRows(root.value, query.value) : []))

/** The breadcrumb: the working directory from the top; in file mode, on down to the file. */
const crumbs = computed(() => {
  const base = crumbsOf(props.cwd || '/')
  if (mode.value !== 'file' || !header.value?.path) return base.map((c) => ({ ...c, inside: false }))
  const rootPath = root.value?.path ?? (props.cwd || '').replace(/\/+$/, '')
  const p = header.value.path
  const rel = rootPath && p.startsWith(rootPath + '/') ? p.slice(rootPath.length + 1) : null
  if (rel === null) {
    const out: Array<{ label: string; path: string; inside: boolean }> = []
    let acc = ''
    for (const part of p.split('/').filter(Boolean)) {
      acc = `${acc}/${part}`
      out.push({ label: part, path: acc, inside: false })
    }
    return out
  }
  const out = base.map((c) => ({ ...c, inside: false }))
  let acc = rootPath
  for (const part of rel.split('/')) {
    acc = `${acc}/${part}`
    out.push({ label: part, path: acc, inside: true })
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

/** Lists a folder of the tree (the root, or one expanded), keeping what was known. */
async function listFolder(node: TreeNode): Promise<boolean> {
  node.loading = true
  node.error = undefined
  try {
    const res = await props.request(node.path, false)
    if (res.header.kind === 'dir') {
      setChildren(node, res.header.entries ?? [])
      return true
    }
    node.loading = false
    node.error = res.header.kind === 'error' ? res.header.error?.message || 'cannot read' : 'not a folder'
    return false
  } catch (e) {
    node.loading = false
    node.error = (e as Error).message
    return false
  }
}

/** The working directory's listing, the tree's first rows; a connection not open yet is tried again a few times. */
async function loadRoot() {
  if (!props.cwd) return
  const r = rootNode(props.cwd)
  root.value = r
  treeError.value = ''
  treeLoading.value = true
  const ok = await listFolder(r)
  treeLoading.value = false
  if (!ok) {
    treeError.value = r.error || 'cannot read'
    if (/not connected/i.test(treeError.value) && retries < 8) {
      retries += 1
      setTimeout(() => {
        if (root.value === r && !r.loaded) loadRoot()
      }, 1000)
    }
  } else retries = 0
}

async function toggle(node: TreeNode) {
  if (!node.dir) {
    openFile({ path: node.path })
    return
  }
  if (node.expanded) {
    node.expanded = false
    return
  }
  node.expanded = true
  if (!node.loaded) await listFolder(node)
}

/** Opens the tree down to `path` (a folder), fetching what is not loaded, and expands it. */
async function reveal(path: string) {
  const r = root.value
  if (!r) return
  const want = path.replace(/\/+$/, '')
  if (want === r.path) return
  if (!want.startsWith(r.path + '/')) return
  let node: TreeNode = r
  const parts = want.slice(r.path.length + 1).split('/')
  for (const part of parts) {
    if (!node.loaded && !(await listFolder(node))) return
    const next = node.children.find((c) => c.name === part && c.dir)
    if (!next) return
    next.expanded = true
    node = next
  }
  if (!node.loaded) await listFolder(node)
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
    if (res.header.kind === 'dir') {
      // A folder: shown in the tree, opened down to it.
      header.value = null
      loading.value = false
      if (root.value && findNode(root.value, path)) {
        const node = findNode(root.value, path)!
        setChildren(node, res.header.entries ?? [])
        node.expanded = true
        await reveal(path)
      } else await reveal(path)
      target.value = null
      return
    }
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

/** A crumb: the tree opened down to that folder, the file left. */
function openCrumb(path: string) {
  header.value = null
  errorText.value = ''
  target.value = null
  url.value = null
  reveal(path)
}

/** Back from a file or a preview to the tree. */
function backToTree() {
  header.value = null
  errorText.value = ''
  target.value = null
  url.value = null
}

/** The box's Enter: a typed path opens; a name opens its first match. */
function go() {
  const q = query.value.trim()
  if (!q) return
  if (looksLikePath(q)) {
    const loc = parseLocation(q)
    openFile({ path: resolveTyped(props.cwd || '/', loc.path), line: loc.line })
    query.value = ''
    return
  }
  const first = rows.value.find((n) => n.name.toLowerCase().includes(q.toLowerCase()))
  if (!first) return
  if (first.dir) toggle(first)
  else {
    openFile({ path: first.path })
    query.value = ''
  }
}

function copyPath() {
  return copy(header.value?.path || target.value?.path || root.value?.path || '', 'Path copied')
}

function refresh() {
  if (url.value) {
    const u = url.value
    url.value = null
    nextTick(() => (url.value = u))
  } else if (target.value) load(target.value.path, target.value.line)
  else loadRoot()
}

watch(
  () => target.value,
  (t) => {
    if (t) {
      url.value = null
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
    }
  },
)

watch(
  () => props.cwd,
  () => {
    retries = 0
    loadRoot()
  },
  { immediate: true },
)

defineExpose({ title, mode, reload: loadRoot })

const rawHref = computed(() => (header.value?.kind === 'file' && props.rawUrl ? props.rawUrl(header.value.path) : null))
</script>

<template>
  <div class="flex h-full min-h-0 flex-col" :data-files-mode="mode">
    <div v-if="mode === 'tree'" class="border-b border-default px-2 py-1.5">
      <UInput v-model="query" placeholder="Filter, or go to path:line" size="xs" class="w-full font-mono" icon="i-lucide-search" data-files-box @keydown.enter.prevent="go" @keydown.escape="query = ''" />
    </div>
    <div class="flex items-center gap-2 border-b border-default px-3 py-2 text-xs">
      <template v-if="mode === 'url'">
        <UIcon name="i-lucide-globe" class="size-4 text-muted" />
        <span class="truncate flex-1 font-mono">{{ url }}</span>
        <UButton label="New tab" icon="i-lucide-external-link" size="xs" color="neutral" variant="soft" :to="url!" target="_blank" rel="noopener noreferrer" />
      </template>
      <template v-else>
        <UButton v-if="mode !== 'tree'" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" aria-label="Back to the files" data-files-back @click="backToTree" />
        <nav class="flex items-center gap-1 flex-1 min-w-0 overflow-x-auto" aria-label="path" data-files-crumbs>
          <template v-for="(c, i) in crumbs" :key="c.path">
            <UIcon v-if="i > 0" name="i-lucide-chevron-right" class="size-3 text-muted flex-none" />
            <button type="button" class="font-mono truncate" :class="[i === crumbs.length - 1 ? 'text-highlighted' : 'text-muted', c.inside || i === crumbsOf(cwd || '/').length - 1 ? 'hover:underline' : 'cursor-default']" :data-files-crumb="c.path" @click="(c.inside || i === crumbsOf(cwd || '/').length - 1) && openCrumb(c.path)">
              {{ c.label }}
            </button>
          </template>
        </nav>
        <UBadge v-if="header?.kind === 'file'" :label="fmtSize(header.size)" color="neutral" variant="subtle" size="sm" />
        <UBadge v-if="header?.truncated" label="truncated" color="warning" variant="subtle" size="sm" />
        <UBadge v-if="header?.binary" label="binary" color="neutral" variant="subtle" size="sm" />
        <UButton v-if="mode === 'file'" icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" aria-label="Copy path" @click="copyPath" />
        <UButton v-if="rawHref" icon="i-lucide-file-output" size="xs" color="neutral" variant="ghost" aria-label="Open raw" :to="rawHref" target="_blank" rel="noopener noreferrer" />
      </template>
      <UButton icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" aria-label="Refresh" :loading="loading || treeLoading" @click="refresh" />
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

      <template v-else-if="mode === 'file'">
        <img v-if="imageSrc" :src="imageSrc" alt="" class="max-w-full p-3" />
        <p v-else-if="header?.binary" class="p-6 text-sm text-muted">Binary file; nothing to show.</p>
        <div v-else-if="html" class="p-3" v-html="html" />
        <pre v-else class="p-3 font-mono"><div v-for="(l, i) in lines" :key="i" class="line" :class="{ target: targetLine === i + 1 }"><span class="line-no">{{ i + 1 }}</span><span class="line-code" v-html="escapeHtml(l) || ' '" /></div></pre>
      </template>

      <template v-else>
        <p v-if="!cwd" class="p-6 text-sm text-muted">Click a file path or URL in the terminal to open it here.</p>
        <div v-else-if="treeLoading && !root?.loaded" class="p-6 text-sm text-muted flex items-center gap-2"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Listing {{ root?.name }}…</div>
        <div v-else-if="treeError" class="flex items-center gap-2 p-4 text-sm text-muted" data-files-error>
          <UIcon name="i-lucide-triangle-alert" class="size-4 flex-none text-warning" />
          <span class="min-w-0 flex-1">{{ treeError }}</span>
          <UButton label="Retry" size="xs" color="neutral" variant="soft" @click="loadRoot" />
        </div>
        <ul v-else class="py-1" data-files-tree>
          <li v-if="root && !rows.length" class="px-4 py-3 text-sm text-muted">{{ query ? 'Nothing matches' : 'Empty directory' }}</li>
          <template v-for="n in rows" :key="n.path">
            <li>
              <button
                type="button"
                class="flex w-full items-center gap-1.5 py-1 pr-3 text-left text-sm hover:bg-elevated"
                :style="{ paddingLeft: `${12 + n.depth * 14}px` }"
                :data-file-node="n.path"
                :data-file-dir="n.dir ? '' : undefined"
                :data-file-expanded="n.dir && n.expanded ? '' : undefined"
                @click="toggle(n)"
              >
                <UIcon v-if="n.dir" :name="n.expanded ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" class="size-3.5 flex-none text-muted" />
                <span v-else class="size-3.5 flex-none" />
                <UIcon :name="n.dir ? (n.expanded ? 'i-lucide-folder-open' : 'i-lucide-folder') : 'i-lucide-file'" class="size-4 flex-none" :class="n.dir ? 'text-primary' : 'text-muted'" />
                <span class="font-mono flex-1 truncate">{{ n.name }}</span>
                <UIcon v-if="n.loading" name="i-lucide-loader-circle" class="size-3.5 animate-spin text-muted" />
                <span v-else-if="!n.dir" class="text-[11px] text-muted">{{ fmtSize(n.size) }}</span>
              </button>
            </li>
            <li v-if="n.dir && n.expanded && n.loaded && !n.children.length && !query" class="py-1 text-xs text-muted" :style="{ paddingLeft: `${12 + (n.depth + 1) * 14 + 20}px` }" :data-file-empty="n.path">Empty directory</li>
            <li v-if="n.dir && n.expanded && n.error" class="py-1 text-xs text-warning" :style="{ paddingLeft: `${12 + (n.depth + 1) * 14 + 20}px` }">{{ n.error }}</li>
          </template>
        </ul>
      </template>
    </div>
    <p v-if="mode === 'tree' && cwd" class="flex items-center gap-1.5 border-t border-default px-3 py-1.5 text-[11px] text-muted" data-files-hint>
      <UIcon name="i-lucide-mouse-pointer-click" class="size-3.5 flex-none" /> Paths the agent prints in the terminal are clickable.
    </p>
  </div>
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

