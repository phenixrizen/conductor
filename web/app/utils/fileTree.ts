import type { FileEntry } from '~/utils/protocol'

/**
 * The Explorer's tree (design 4a): the session's working directory as the
 * root, folders expanding in place with their own listing fetched on the
 * first expansion, files with their size. Pure: the component fetches, the
 * tree only keeps what it was given.
 */
export interface TreeNode {
  /** Absolute path. */
  path: string
  name: string
  dir: boolean
  size?: number
  /** 0 for the root's children. */
  depth: number
  expanded: boolean
  /** Whether the folder's listing has been fetched (its children are known). */
  loaded: boolean
  loading: boolean
  error?: string
  children: TreeNode[]
}

export function rootNode(cwd: string): TreeNode {
  const path = cwd.replace(/\/+$/, '') || '/'
  return { path, name: path.split('/').pop() || '/', dir: true, depth: -1, expanded: true, loaded: false, loading: false, children: [] }
}

/** The listing of a folder becomes its children: folders first, then files, each by name without regard to case; a `.git` folder is left out (the repository's own store, never worked in). */
export function setChildren(node: TreeNode, entries: readonly FileEntry[]): void {
  const base = node.path === '/' ? '' : node.path
  entries = entries.filter((e) => !(e.dir && e.name === '.git'))
  // By code point of the lowercased name, not the locale's collation: the same order in every browser, and the order a listing shows.
  const byName = (a: FileEntry, b: FileEntry) => {
    const x = a.name.toLowerCase()
    const y = b.name.toLowerCase()
    return x < y ? -1 : x > y ? 1 : a.name < b.name ? -1 : a.name > b.name ? 1 : 0
  }
  const sorted = [...entries].sort((a, b) => (a.dir === b.dir ? byName(a, b) : a.dir ? -1 : 1))
  const old = new Map(node.children.map((c) => [c.name, c]))
  node.children = sorted.map((e) => {
    const prev = old.get(e.name)
    if (prev && prev.dir === e.dir) return { ...prev, size: e.size }
    return { path: `${base}/${e.name}`, name: e.name, dir: e.dir, size: e.size, depth: node.depth + 1, expanded: false, loaded: false, loading: false, children: [] }
  })
  node.loaded = true
  node.loading = false
  node.error = undefined
}

/** The node at `path` in the tree, or null. */
export function findNode(root: TreeNode, path: string): TreeNode | null {
  const want = path.replace(/\/+$/, '') || '/'
  if (root.path === want) return root
  for (const c of root.children) {
    if (c.path === want) return c
    if (c.dir && (want.startsWith(c.path + '/') || c.path === '/')) {
      const hit = findNode(c, want)
      if (hit) return hit
    }
  }
  return null
}

/** A name matches the filter when it contains it, case-insensitive; an empty filter matches everything. */
export function matches(name: string, query: string): boolean {
  const q = query.trim().toLowerCase()
  return !q || name.toLowerCase().includes(q)
}

/**
 * The filter as a path, when it holds a slash (#138): `go/src` narrows the tree to that folder and what is below it. Trimmed, a
 * leading `./` or `/` dropped, a trailing slash dropped, lower-cased; null for a filter that is a name.
 */
export function pathQuery(query: string): string | null {
  const q = query.trim().toLowerCase()
  if (!q.includes('/')) return null
  return q.replace(/^\.\//, '').replace(/^\/+/, '').replace(/\/+$/, '')
}

/** A node's path from the root (`internal/api`), lower-cased for matching. */
function relPath(root: TreeNode, node: TreeNode): string {
  const base = root.path === '/' ? '' : root.path
  return (node.path.startsWith(base + '/') ? node.path.slice(base.length + 1) : node.path).toLowerCase()
}

/**
 * Whether a node is on the way to a path query, is that path, or lies below it: `go/src` shows `go`, `go/src` and everything
 * loaded under `go/src`, nothing beside them.
 */
export function onPath(rel: string, q: string): boolean {
  return rel === q || q.startsWith(rel + '/') || rel.startsWith(q + '/')
}

/**
 * The rows to draw, top to bottom: the root's children, an expanded folder's
 * children after it. With a filter, only the loaded nodes whose name matches
 * or that hold a match somewhere below, their folders opened so the match
 * shows; folders not yet fetched stay as they are (the filter is over what
 * is loaded).
 */
export function visibleRows(root: TreeNode, query = ''): TreeNode[] {
  const q = query.trim()
  const path = pathQuery(q)
  const out: TreeNode[] = []
  const walk = (node: TreeNode) => {
    for (const c of node.children) {
      if (!q) {
        out.push(c)
        if (c.dir && c.expanded) walk(c)
        continue
      }
      if (path !== null) {
        // A path: the folders on the way to it, itself, and what is loaded below it (#138).
        const rel = relPath(root, c)
        if (!onPath(rel, path)) continue
        out.push(c)
        if (c.dir && c.loaded && (rel === path || path.startsWith(rel + '/') || c.expanded)) walk(c)
        continue
      }
      if (holdsMatch(c, q)) {
        out.push(c)
        if (c.dir && c.loaded) walk(c)
      }
    }
  }
  walk(root)
  return out
}

/** Whether the node's name matches, or a loaded descendant's does. */
export function holdsMatch(node: TreeNode, query: string): boolean {
  if (matches(node.name, query)) return true
  return node.dir && node.loaded && node.children.some((c) => holdsMatch(c, query))
}

/**
 * What the box's Enter takes as a path to open rather than a name to find: a slash in it, a leading dot or tilde, or a `:line` at
 * its end. As a filter, a path narrows the tree to it (pathQuery) rather than matching names.
 */
export function looksLikePath(input: string): boolean {
  const s = input.trim()
  return s.includes('/') || /^[.~]/.test(s) || /:\d+(?::\d+)?$/.test(s)
}

/** A typed path made absolute against the working directory. */
export function resolveTyped(cwd: string, input: string): string {
  const s = input.trim()
  if (s.startsWith('/')) return s
  const base = cwd.replace(/\/+$/, '')
  return `${base}/${s.replace(/^\.\//, '')}`
}

/**
 * The breadcrumb folded from the left (#169): the last `keep` crumbs show whole (the working directory's own folder, and in file
 * mode the folders down to the file and the file), the folders above them fold into one `…` crumb that lists them; a path short
 * enough shows whole. Nothing is cut to a letter: what shows, shows whole, and the row scrolls when even that does not fit.
 */
export function foldCrumbs<T>(crumbs: readonly T[], keep: number): { hidden: T[]; shown: T[] } {
  const k = Math.max(1, keep)
  if (crumbs.length <= k + 1) return { hidden: [], shown: [...crumbs] }
  return { hidden: crumbs.slice(0, crumbs.length - k), shown: crumbs.slice(crumbs.length - k) }
}

/** The breadcrumb of the root: the working directory's folders from the top. */
export function crumbsOf(cwd: string): Array<{ label: string; path: string }> {
  const parts = cwd.replace(/\/+$/, '').split('/').filter(Boolean)
  const out: Array<{ label: string; path: string }> = []
  let acc = ''
  for (const part of parts) {
    acc = `${acc}/${part}`
    out.push({ label: part, path: acc })
  }
  return out.length ? out : [{ label: '/', path: '/' }]
}

/**
 * The Explorer's filter beyond what is loaded (design round 12): the files a `find` reply found, absolute, not already drawn in the
 * tree, with their folder and name for the row.
 */
export function foundRows(base: string, matches: readonly string[], drawn: ReadonlySet<string>): Array<{ abs: string; dir: string; name: string }> {
  const root = base.replace(/\/+$/, '')
  const out: Array<{ abs: string; dir: string; name: string }> = []
  for (const m of matches) {
    const abs = `${root}/${m}`
    if (drawn.has(abs)) continue
    const i = m.lastIndexOf('/')
    out.push({ abs, dir: i >= 0 ? m.slice(0, i + 1) : '', name: i >= 0 ? m.slice(i + 1) : m })
  }
  return out
}
