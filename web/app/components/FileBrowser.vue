<script setup lang="ts">
import type { ActivityEntry, FileEntry, FileHeader, FileRequester } from '~/utils/protocol'
import { agoWords, changeMarks, changeRows, changesTitle, statusLetter, statusTone, type ChangeRow } from '~/utils/changes'
import { commitAgo, commitChangeRows, commitRows, commitsHead, type CommitChangeRow, type CommitRow } from '~/utils/commits'
import { opIcon, touchedPaths, touchedRows, touchedWords } from '~/utils/touched'
import { crumbsOf, findNode, foundRows, looksLikePath, resolveTyped, rootNode, setChildren, visibleRows, type TreeNode } from '~/utils/fileTree'
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
  request: FileRequester
  /** The session's working directory: the tree's root and the breadcrumb's. */
  cwd?: string
  /** Builds a raw download URL for server sessions; null when unavailable. */
  rawUrl?: (path: string) => string | null
  /** Files open elsewhere (the editor area, design 4b): a file chosen is emitted as `open`, and the pane stays the tree. */
  external?: boolean
  /** What the Changes section counts against (design 4d): HEAD when empty; a crew member's run base. */
  base?: string
  /** The session's activity, for the Touched section (design 4e): its `file` entries, newest first. Absent, the section is not offered. */
  activity?: ActivityEntry[]
}>()
const emit = defineEmits<{ open: [target: FileTarget]; openDiff: [change: ChangeRow, against: { top: string; branch?: string; base?: string; baseId?: string }]; openCommitDiff: [change: CommitChangeRow, commit: { sha: string; short: string; parent: string }] }>()

// The Changes section (design 4d): git status against the base, refreshed every few seconds while it shows.
const section = ref<'explorer' | 'changes' | 'touched' | 'commits'>('explorer')
const touched = computed(() => (props.activity && props.cwd ? touchedRows(props.activity, props.cwd) : []))
const dots = computed(() => touchedPaths(touched.value))
const status = ref<FileHeader | null>(null)
const statusError = ref('')
const statusLoading = ref(false)
const statusAt = ref(0)
const now = ref(Date.now())
let statusTimer: number | undefined
let clock: number | undefined
const notRepo = computed(() => status.value?.kind === 'error' && status.value.error?.code === 'not_repo')
const changes = computed(() => (status.value?.kind === 'status' ? changeRows(status.value.path, status.value.changes ?? []) : []))
const marks = computed(() => (status.value?.kind === 'status' ? changeMarks(status.value.path, status.value.changes ?? []) : new Map()))

async function loadStatus() {
  if (!props.cwd) return
  statusLoading.value = true
  try {
    const res = await props.request(props.cwd, false, { op: 'status', base: props.base })
    status.value = res.header
    statusError.value = res.header.kind === 'error' && res.header.error?.code !== 'not_repo' ? res.header.error?.message || 'cannot read the status' : ''
    statusAt.value = Date.now()
  } catch (e) {
    statusError.value = (e as Error).message
  } finally {
    statusLoading.value = false
  }
}
function pollStatus() {
  window.clearInterval(statusTimer)
  statusTimer = window.setInterval(() => {
    if (section.value === 'changes' && document.visibilityState !== 'hidden') loadStatus()
  }, 5000)
}
watch(section, (s) => {
  if (s === 'changes') {
    loadStatus()
    pollStatus()
  } else window.clearInterval(statusTimer)
  if (s === 'commits') {
    loadLog()
    pollLog()
  } else window.clearInterval(logTimer)
})

// The Commits section (design 4e): the commits on the branch since the session started (a crew member's: since the run's base),
// refreshed while it shows; a commit opens to its files, a file to its diff against the commit's parent.
const log = ref<FileHeader | null>(null)
const logError = ref('')
const logLoading = ref(false)
let logTimer: number | undefined
const commits = computed<CommitRow[]>(() => commitRows(log.value))
const logNotRepo = computed(() => log.value?.kind === 'error' && log.value.error?.code === 'not_repo')
const opened = reactive(new Map<string, { header: FileHeader | null; error: string }>())
async function loadLog() {
  if (!props.cwd) return
  logLoading.value = true
  try {
    const res = await props.request(props.cwd, false, { op: 'log', base: props.base })
    log.value = res.header
    logError.value = res.header.kind === 'error' && res.header.error?.code !== 'not_repo' ? res.header.error?.message || 'cannot read the commits' : ''
  } catch (e) {
    logError.value = (e as Error).message
  } finally {
    logLoading.value = false
  }
}
function pollLog() {
  window.clearInterval(logTimer)
  logTimer = window.setInterval(() => {
    if (section.value === 'commits' && document.visibilityState !== 'hidden') loadLog()
  }, 10000)
}
async function toggleCommit(c: CommitRow) {
  if (opened.has(c.sha)) {
    opened.delete(c.sha)
    return
  }
  opened.set(c.sha, { header: null, error: '' })
  try {
    const res = await props.request(props.cwd || '.', false, { op: 'commit', rev: c.sha })
    opened.set(c.sha, { header: res.header, error: res.header.kind === 'error' ? res.header.error?.message || 'cannot read the commit' : '' })
  } catch (e) {
    opened.set(c.sha, { header: null, error: (e as Error).message })
  }
}
function openCommitChange(c: CommitRow, ch: CommitChangeRow) {
  emit('openCommitDiff', ch, { sha: c.sha, short: c.short, parent: c.parent })
}
onMounted(() => {
  clock = window.setInterval(() => (now.value = Date.now()), 1000)
  // The marks beside changed files: one status read when the tree is up, then as the Changes section refreshes.
  setTimeout(() => loadStatus(), 1500)
})
onBeforeUnmount(() => {
  window.clearInterval(logTimer)
  window.clearInterval(statusTimer)
  window.clearInterval(clock)
})
function openChange(c: ChangeRow) {
  const h = status.value
  emit('openDiff', c, { top: h?.path || props.cwd || '/', branch: h?.branch, base: props.base, baseId: h?.base })
}

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
// The filter reaches folders not opened yet: a find on the session's machine, a moment after typing stops, lists the files below
// whose name holds the words and the tree does not draw.
const found = ref<FileHeader | null>(null)
let findTimer: number | undefined
let findSeq = 0
watch(query, (q) => {
  window.clearTimeout(findTimer)
  const words = q.trim()
  if (!words || looksLikePath(words) || !props.cwd) {
    found.value = null
    return
  }
  const seq = ++findSeq
  findTimer = window.setTimeout(async () => {
    try {
      const res = await props.request(words, false, { op: 'find' }) // the words ride as the path; the session's working directory is the root
      if (seq === findSeq) found.value = res.header.kind === 'find' ? res.header : null
    } catch {
      if (seq === findSeq) found.value = null
    }
  }, 300)
})
const foundBelow = computed(() => (found.value && query.value.trim() ? foundRows(found.value.path, found.value.matches ?? [], new Set(rows.value.map((n) => n.path))) : []))

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

/** The working directory's listing, the tree's first rows; a connection not open yet is tried again, once a second, ten times. */
async function loadRoot() {
  if (!props.cwd) return
  // Reactive from the start: the listing mutates the root, and the retry below compares it with the ref's.
  const r = reactive(rootNode(props.cwd)) as TreeNode
  root.value = r
  treeError.value = ''
  treeLoading.value = true
  const ok = await listFolder(r)
  treeLoading.value = false
  if (!ok) {
    treeError.value = r.error || 'cannot read'
    // The connection is not open yet (the page's terminal mounts and connects after the pane): try again in a moment.
    if (/not connected|not ready/i.test(treeError.value) && retries < 10) {
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
  else if (section.value === 'changes') loadStatus()
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
    <div v-if="mode === 'tree' && cwd" class="flex items-center gap-1 border-b border-default px-2 pt-1.5 text-xs" data-files-sections>
      <button type="button" class="rounded-t px-2.5 py-1.5 font-medium" :class="section === 'explorer' ? 'border-b-2 border-primary text-highlighted' : 'text-muted hover:text-default'" data-files-section="explorer" @click="section = 'explorer'">Explorer</button>
      <button type="button" class="flex items-center gap-1.5 rounded-t px-2.5 py-1.5 font-medium" :class="section === 'changes' ? 'border-b-2 border-primary text-highlighted' : 'text-muted hover:text-default'" data-files-section="changes" @click="section = 'changes'">
        Changes<UBadge v-if="changes.length" :label="String(changes.length)" color="neutral" variant="subtle" size="xs" data-files-changes-count />
      </button>
      <button type="button" class="flex items-center gap-1.5 rounded-t px-2.5 py-1.5 font-medium" :class="section === 'commits' ? 'border-b-2 border-primary text-highlighted' : 'text-muted hover:text-default'" data-files-section="commits" @click="section = 'commits'">
        Commits<UBadge v-if="commits.length" :label="String(commits.length)" color="neutral" variant="subtle" size="xs" data-files-commits-count />
      </button>
      <button v-if="activity" type="button" class="flex items-center gap-1.5 rounded-t px-2.5 py-1.5 font-medium" :class="section === 'touched' ? 'border-b-2 border-primary text-highlighted' : 'text-muted hover:text-default'" data-files-section="touched" @click="section = 'touched'">
        Touched<UBadge v-if="touched.length" :label="String(touched.length)" color="neutral" variant="subtle" size="xs" data-files-touched-count />
      </button>
    </div>
    <div v-if="mode === 'tree' && section === 'commits'" class="flex items-center gap-2 border-b border-default px-3 py-2 text-xs" data-files-commits-head>
      <UIcon name="i-lucide-git-commit-horizontal" class="size-3.5 flex-none text-muted" />
      <span class="min-w-0 flex-1 truncate text-muted">{{ logNotRepo ? 'not a repository' : commitsHead(commits.length, log, base) }}</span>
      <UButton icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" aria-label="Refresh" :loading="logLoading" @click="loadLog" />
    </div>
    <div v-if="mode === 'tree' && section === 'touched'" class="flex items-center gap-2 border-b border-default px-3 py-2 text-xs" data-files-touched-head>
      <UIcon name="i-lucide-history" class="size-3.5 flex-none text-muted" />
      <span class="min-w-0 flex-1 truncate text-muted">newest first · {{ touched.length }} since the session started</span>
    </div>
    <div v-if="mode === 'tree' && section === 'explorer'" class="border-b border-default px-2 py-1.5">
      <UInput v-model="query" placeholder="Filter, or go to path:line" size="xs" class="w-full font-mono" icon="i-lucide-search" data-files-box @keydown.enter.prevent="go" @keydown.escape="query = ''" />
    </div>
    <div v-if="mode === 'tree' && section === 'changes'" class="flex items-center gap-2 border-b border-default px-3 py-2 text-xs" data-files-changes-head>
      <UIcon name="i-lucide-git-branch" class="size-3.5 flex-none text-muted" />
      <span class="min-w-0 flex-1 truncate font-mono text-muted">{{ notRepo ? 'not a repository' : `${status?.branch || '…'} · ${base && base !== 'HEAD' ? `vs ${base}` : 'working directory'}` }}</span>
      <span v-if="!notRepo" class="flex-none text-muted">{{ changesTitle(changes.length) }}</span>
      <UButton icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" aria-label="Refresh" :loading="statusLoading" @click="loadStatus" />
    </div>
    <div v-if="mode !== 'tree' || section === 'explorer'" class="flex items-center gap-2 border-b border-default px-3 py-2 text-xs">
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

      <template v-else-if="section === 'touched'">
        <p v-if="!touched.length" class="p-6 text-sm text-muted">Nothing yet. The files the agent reads, edits, writes and deletes list here as its hooks report them.</p>
        <ul v-else class="py-1" data-files-touched>
          <li v-for="(r, i) in touched" :key="`${r.abs}-${r.at}-${i}`">
            <button type="button" class="flex w-full items-center gap-2 px-3 py-1 text-left text-sm hover:bg-elevated" :data-touched="r.abs" :data-touched-op="r.op" @click="openFile({ path: r.abs })">
              <UIcon :name="opIcon(r.op)" class="size-3.5 flex-none text-muted" />
              <span class="min-w-0 flex-1 truncate font-mono"><span class="text-muted">{{ r.dir }}</span>{{ r.name }}</span>
              <span class="flex-none text-[11px] text-muted">{{ touchedWords(r) }}</span>
            </button>
          </li>
        </ul>
      </template>
      <template v-else-if="section === 'commits'">
        <div v-if="logNotRepo" class="flex flex-col items-center gap-2 p-6 text-center text-sm" data-files-not-repo>
          <UIcon name="i-lucide-git-branch" class="size-6 text-muted" />
          <span class="font-medium text-highlighted">Not a git repository</span>
          <span class="text-muted">{{ cwd }} has no .git. Touched still lists what the agent did.</span>
        </div>
        <div v-else-if="logError" class="flex items-center gap-2 p-4 text-sm text-muted">
          <UIcon name="i-lucide-triangle-alert" class="size-4 flex-none text-warning" /><span class="min-w-0 flex-1">{{ logError }}</span>
          <UButton label="Retry" size="xs" color="neutral" variant="soft" @click="loadLog" />
        </div>
        <div v-else-if="!log" class="flex items-center gap-2 p-6 text-sm text-muted"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Reading the commits…</div>
        <p v-else-if="!commits.length" class="p-6 text-sm text-muted">{{ base && base !== 'HEAD' ? `No commits since ${base}.` : 'No commits since the session started.' }}</p>
        <ul v-else class="py-1" data-files-commits>
          <li v-for="c in commits" :key="c.sha">
            <button type="button" class="flex w-full items-start gap-2 px-3 py-1.5 text-left text-sm hover:bg-elevated" :data-commit="c.sha" :aria-expanded="opened.has(c.sha)" @click="toggleCommit(c)">
              <UIcon :name="opened.has(c.sha) ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" class="mt-0.5 size-3.5 flex-none text-muted" />
              <span class="min-w-0 flex-1">
                <span class="block truncate text-highlighted" data-commit-subject>{{ c.subject }}</span>
                <span class="block truncate text-[11px] text-muted"><span class="font-mono">{{ c.short }}</span>{{ c.author ? ` · ${c.author}` : '' }} · {{ commitAgo(c.atMs, now) }}</span>
              </span>
            </button>
            <div v-if="opened.has(c.sha)" class="pb-1 pl-6" :data-commit-files="c.sha">
              <div v-if="!opened.get(c.sha)!.header && !opened.get(c.sha)!.error" class="flex items-center gap-2 px-3 py-1 text-xs text-muted"><UIcon name="i-lucide-loader-circle" class="size-3.5 animate-spin" /> Reading…</div>
              <p v-else-if="opened.get(c.sha)!.error" class="px-3 py-1 text-xs text-warning">{{ opened.get(c.sha)!.error }}</p>
              <template v-else>
                <p v-if="opened.get(c.sha)!.header?.commit?.body" class="whitespace-pre-wrap px-3 py-1 text-xs text-muted" data-commit-body>{{ opened.get(c.sha)!.header?.commit?.body }}</p>
                <button v-for="ch in commitChangeRows(opened.get(c.sha)!.header)" :key="ch.abs" type="button" class="flex w-full items-center gap-2 px-3 py-0.5 text-left text-sm hover:bg-elevated" :data-commit-change="ch.abs" :data-change-status="ch.status" @click="openCommitChange(c, ch)">
                  <UBadge :label="statusLetter(ch.status)" :color="statusTone(ch.status)" variant="subtle" size="xs" class="w-5 flex-none justify-center font-mono" />
                  <span class="min-w-0 flex-1 truncate font-mono"><span class="text-muted">{{ ch.dir }}</span>{{ ch.name }}</span>
                  <span v-if="ch.binary" class="flex-none text-[11px] text-muted">binary</span>
                  <template v-else>
                    <span class="flex-none font-mono text-[11px] text-success">+{{ ch.added }}</span>
                    <span class="flex-none font-mono text-[11px] text-error">−{{ ch.removed }}</span>
                  </template>
                </button>
                <p v-if="opened.get(c.sha)!.header?.truncated" class="px-3 py-0.5 text-xs text-muted">The list stops here.</p>
              </template>
            </div>
          </li>
          <li v-if="log?.truncated" class="px-3 py-1 text-xs text-muted">The list stops at {{ commits.length }} commits.</li>
        </ul>
      </template>
      <template v-else-if="section === 'changes'">
        <div v-if="notRepo" class="flex flex-col items-center gap-2 p-6 text-center text-sm" data-files-not-repo>
          <UIcon name="i-lucide-git-branch" class="size-6 text-muted" />
          <span class="font-medium text-highlighted">Not a git repository</span>
          <span class="text-muted">{{ cwd }} has no .git. Touched still lists what the agent did.</span>
        </div>
        <div v-else-if="statusError" class="flex items-center gap-2 p-4 text-sm text-muted">
          <UIcon name="i-lucide-triangle-alert" class="size-4 flex-none text-warning" /><span class="min-w-0 flex-1">{{ statusError }}</span>
          <UButton label="Retry" size="xs" color="neutral" variant="soft" @click="loadStatus" />
        </div>
        <p v-else-if="status && !changes.length" class="p-6 text-sm text-muted">Nothing changed since the last commit.</p>
        <ul v-else class="py-1" data-files-changes>
          <li v-for="c in changes" :key="c.abs">
            <button type="button" class="flex w-full items-center gap-2 px-3 py-1 text-left text-sm hover:bg-elevated" :data-change="c.abs" :data-change-status="c.status" @click="openChange(c)">
              <UBadge :label="statusLetter(c.status)" :color="statusTone(c.status)" variant="subtle" size="xs" class="w-5 flex-none justify-center font-mono" />
              <span class="min-w-0 flex-1 truncate font-mono"><span class="text-muted">{{ c.dir }}</span>{{ c.name }}</span>
              <span v-if="c.binary" class="flex-none text-[11px] text-muted">binary</span>
              <template v-else>
                <span class="flex-none font-mono text-[11px] text-success">+{{ c.added }}</span>
                <span class="flex-none font-mono text-[11px] text-error">−{{ c.removed }}</span>
              </template>
            </button>
          </li>
          <li v-if="status?.truncated" class="px-3 py-1 text-xs text-muted">The list stops at {{ changes.length }} files.</li>
        </ul>
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
          <li v-if="root && !rows.length && !foundBelow.length" class="px-4 py-3 text-sm text-muted">{{ query ? 'Nothing matches' : 'Empty directory' }}</li>
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
                <span class="font-mono flex-1 truncate">{{ n.name }}<span v-if="!n.dir && dots.has(n.path)" class="ml-1.5 inline-block size-1.5 rounded-full bg-muted align-middle" :data-file-touched="n.path" /></span>
                <span v-if="!n.dir && marks.get(n.path)" class="flex-none font-mono text-[10px] font-semibold" :class="statusTone(marks.get(n.path)!) === 'success' ? 'text-success' : statusTone(marks.get(n.path)!) === 'error' ? 'text-error' : 'text-warning'" :data-file-mark="statusLetter(marks.get(n.path)!)">{{ statusLetter(marks.get(n.path)!) }}</span>
                <UIcon v-if="n.loading" name="i-lucide-loader-circle" class="size-3.5 animate-spin text-muted" />
                <span v-else-if="!n.dir" class="text-[11px] text-muted">{{ fmtSize(n.size) }}</span>
              </button>
            </li>
            <li v-if="n.dir && n.expanded && n.loaded && !n.children.length && !query" class="py-1 text-xs text-muted" :style="{ paddingLeft: `${12 + (n.depth + 1) * 14 + 20}px` }" :data-file-empty="n.path">Empty directory</li>
            <li v-if="n.dir && n.expanded && n.error" class="py-1 text-xs text-warning" :style="{ paddingLeft: `${12 + (n.depth + 1) * 14 + 20}px` }">{{ n.error }}</li>
          </template>
          <template v-if="foundBelow.length">
            <li class="mt-1 border-t border-default px-3 pb-0.5 pt-2 text-[11px] font-medium uppercase tracking-wide text-muted" data-files-found-head>In folders not opened yet</li>
            <li v-for="f in foundBelow" :key="f.abs">
              <button type="button" class="flex w-full items-center gap-1.5 px-3 py-1 text-left text-sm hover:bg-elevated" :data-file-found="f.abs" @click="openFile({ path: f.abs })">
                <UIcon name="i-lucide-file" class="size-4 flex-none text-muted" />
                <span class="min-w-0 flex-1 truncate font-mono"><span class="text-muted">{{ f.dir }}</span>{{ f.name }}</span>
              </button>
            </li>
            <li v-if="found?.truncated" class="px-3 py-1 text-[11px] text-muted">The search stopped early; type more to narrow it.</li>
            <li class="px-3 py-1 text-[11px] text-muted">.git and node_modules are not searched.</li>
          </template>
        </ul>
      </template>
    </div>
    <p v-if="mode === 'tree' && cwd && section === 'explorer'" class="flex items-center gap-1.5 border-t border-default px-3 py-1.5 text-[11px] text-muted" data-files-hint>
      <UIcon name="i-lucide-mouse-pointer-click" class="size-3.5 flex-none" /> Paths the agent prints in the terminal are clickable.
    </p>
    <p v-if="mode === 'tree' && cwd && section === 'commits' && log?.kind === 'log'" class="flex items-center gap-1.5 border-t border-default px-3 py-1.5 text-[11px] text-muted" data-files-commits-foot><UIcon name="i-lucide-info" class="size-3 flex-none" />Commits made here. Nothing is pushed from Conductor.</p>
    <p v-if="mode === 'tree' && cwd && section === 'touched'" class="border-t border-default px-3 py-1.5 text-[11px] text-muted" data-files-touched-foot>The same events are in Activity and on the Events page, quiet there by default.</p>
    <p v-if="mode === 'tree' && cwd && section === 'changes' && status?.kind === 'status'" class="flex items-center gap-1.5 border-t border-default px-3 py-1.5 text-[11px] text-muted" data-files-changes-foot>
      <UIcon name="i-lucide-refresh-cw" class="size-3.5 flex-none" /> Refreshed as the agent works · {{ agoWords(now - statusAt) }}
      <span class="ml-auto font-mono"><span class="text-success">+{{ status.added ?? 0 }}</span> <span class="text-error">−{{ status.removed ?? 0 }}</span></span>
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

