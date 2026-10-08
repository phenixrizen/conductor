/**
 * The editor area's tabs (design 4b, 4c): one tab per file or URL open,
 * the active one shown, Ctrl+Tab and Ctrl+Shift+Tab to switch, × or
 * Ctrl+W to close, the strip's × for all; closing the last tab takes the
 * area away. Folded (T; Alt+T in the terminal) the strip stays as one line
 * above the terminal. The split with the terminal is a fraction of the
 * column, kept per browser. Pure: the page keeps the state, this moves it.
 */
export type TabKind = 'file' | 'url' | 'diff'

export interface EditorTab {
  /** `file:<path>` or `url:<url>`: one tab per thing open. */
  id: string
  kind: TabKind
  /** The absolute path, or the URL. */
  path: string
  /** The file's name, or the URL's host. */
  title: string
  /** The line to show when the tab was opened at one; cleared once shown. */
  line?: number
  /** A diff tab: the file's status and lines, and the revision it is against (HEAD when empty) with its words. */
  meta?: { status?: string; added?: number; removed?: number; base?: string; against?: string }
}

export interface TabsState {
  tabs: EditorTab[]
  active: string | null
  folded: boolean
}

export const SPLIT_KEY = 'conductor.editor.split'
/** The editor's share of the column, by default and at its ends. */
export const SPLIT_DEFAULT = 0.6
export const SPLIT_MIN = 0.2
export const SPLIT_MAX = 0.85
/** The terminal keeps at least this many lines under the editor (design 4b). */
export const MIN_TERMINAL_LINES = 6

export function emptyTabs(): TabsState {
  return { tabs: [], active: null, folded: false }
}

export function tabId(kind: TabKind, path: string): string {
  return `${kind}:${path}`
}

export function tabTitle(kind: TabKind, path: string): string {
  if (kind === 'url') {
    try {
      return new URL(path).host || path
    } catch {
      return path
    }
  }
  return path.split('/').pop() || path
}

/** Opens a file, a URL or a diff: a tab already open is brought to the front (at the line asked for, its meta refreshed); the area unfolds. */
export function openTab(state: TabsState, kind: TabKind, path: string, line?: number, meta?: EditorTab['meta']): TabsState {
  const id = tabId(kind, path)
  const had = state.tabs.find((t) => t.id === id)
  const tabs = had ? state.tabs.map((t) => (t.id === id ? { ...t, line, meta: meta ?? t.meta } : t)) : [...state.tabs, { id, kind, path, title: tabTitle(kind, path), line, meta }]
  return { tabs, active: id, folded: false }
}

export function activateTab(state: TabsState, id: string): TabsState {
  if (!state.tabs.some((t) => t.id === id)) return state
  return { ...state, active: id, folded: false }
}

/** Closes a tab; the one after it (else before it) takes its place. */
export function closeTab(state: TabsState, id: string): TabsState {
  const i = state.tabs.findIndex((t) => t.id === id)
  if (i < 0) return state
  const tabs = state.tabs.filter((t) => t.id !== id)
  let active = state.active
  if (active === id) active = tabs[Math.min(i, tabs.length - 1)]?.id ?? null
  return { tabs, active, folded: tabs.length ? state.folded : false }
}

export function closeAllTabs(): TabsState {
  return emptyTabs()
}

/** The tab after the active one (Ctrl+Tab), round the end; `-1` the one before. */
export function cycleTab(state: TabsState, step: 1 | -1 = 1): TabsState {
  if (state.tabs.length < 2) return state
  const i = state.tabs.findIndex((t) => t.id === state.active)
  const next = state.tabs[(i + step + state.tabs.length) % state.tabs.length]!
  return { ...state, active: next.id, folded: false }
}

export function toggleFold(state: TabsState): TabsState {
  if (!state.tabs.length) return state
  return { ...state, folded: !state.folded }
}

/** The line a tab was opened at, taken once. */
export function takeLine(state: TabsState, id: string): TabsState {
  return { ...state, tabs: state.tabs.map((t) => (t.id === id && t.line !== undefined ? { ...t, line: undefined } : t)) }
}

export function readSplit(storage: Pick<Storage, 'getItem'> | null): number {
  try {
    const v = Number(storage?.getItem(SPLIT_KEY))
    return Number.isFinite(v) && v > 0 ? clampSplit(v) : SPLIT_DEFAULT
  } catch {
    return SPLIT_DEFAULT
  }
}

export function writeSplit(storage: Pick<Storage, 'setItem'> | null, v: number): void {
  try {
    storage?.setItem(SPLIT_KEY, String(clampSplit(v)))
  } catch {
    // The browser may refuse storage; the split then lasts the page.
  }
}

export function clampSplit(v: number): number {
  return Math.min(SPLIT_MAX, Math.max(SPLIT_MIN, v))
}

/**
 * The editor's height in pixels for a column of `total` pixels at `split`,
 * the terminal keeping at least MIN_TERMINAL_LINES of `lineHeight` plus the
 * `reserved` pixels around it (the divider, the reply bar).
 */
export function editorHeight(total: number, split: number, lineHeight: number, reserved: number): number {
  const minTerminal = MIN_TERMINAL_LINES * lineHeight
  const most = Math.max(0, total - reserved - minTerminal)
  return Math.max(0, Math.min(most, Math.round(total * clampSplit(split))))
}
