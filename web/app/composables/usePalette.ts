import type { AgentInfo, CrewSummary, SessionInfo } from '~/composables/useSessions'
import type { Action } from '~/utils/actions'
import { MODE_PREFIX, type PaletteMode, type PaletteRow } from '~/utils/palette'
import type { RowPrompt } from '~/utils/sidebarActions'

/**
 * A row of the command palette (#302): what the pure ranking sees (key,
 * label, detail, words) and what the component renders and runs. Nothing
 * runs before Enter; a row with `ask` asks in the row first; a row with
 * `field` takes a line in the row (a broadcast, a run's name, a reply).
 */
export interface PaletteItem extends PaletteRow {
  icon?: string
  /** An agent chip with the status dot, in the sidebar's grammar. */
  agentId?: string
  state?: 'needs' | 'running' | 'idle' | 'exited'
  /** The keys that run it, shown as a hint. */
  kbds?: string[]
  /** A Needs-you row: the session and its prompt, answered in the row. */
  session?: SessionInfo
  prompt?: RowPrompt | null
  /** Enter. What it returns is not read. */
  run: () => unknown
  /** → reveals these; their keys run them while the row has the focus. */
  actions?: Action[]
  /** The row asks before it runs. */
  ask?: { title: string; detail: string }
  /** The row takes a line (a broadcast, a run's name): the action `field` among its actions opens it, or Enter when `fieldOnEnter`; Enter in it submits. */
  field?: { placeholder: string; maxLength?: number; button?: string; note?: string; submit: (text: string) => unknown }
  fieldOnEnter?: boolean
  /** ⌘↵ opens it in a new tab. */
  href?: string
  /** ⇧↵ shows it in the Yard. */
  yardId?: string
  disabled?: boolean
  /** A switch: shown on or off; Enter flips it. */
  on?: boolean
  /** Enter runs it and the palette stays open (a switch does too). */
  keepOpen?: boolean
  /** Answer all (#303): choice `i` answers every session of the row's group, one send each. */
  answerAll?: (i: number) => void
  /** A line under the detail saying why the row will not act (a crew on another machine). */
  note?: string
  /** A run's member: `R` opens its run. */
  runId?: string
}

/** What a page registers (`usePalette().registerPage`): its own group, first after Needs you and first under `>`. */
export interface PageGroup {
  label: string
  rows: PaletteItem[]
  /** `:` mode: the page's open tabs, changes and commits, in that order. */
  files?: PaletteItem[]
  /** `@` mode: the people on the page's session and its links. */
  people?: PaletteItem[]
  /** The session the page shows: what `:path` opens into, and the directory a relative path is read against. */
  sessionId?: string
  cwd?: string
  /** The page's editor (its own useEditorTabs: one per page): what `:path` and the `open` verb open a file with. */
  openFile?: (loc: { path: string; line?: number }) => void
  /** The session page's inspector: a session row's Open Files / Chat / Activity / People on that page's own session. */
  showView?: (v: 'explorer' | 'chat' | 'activity' | 'people') => void
}

const pageGetter = shallowRef<(() => PageGroup | null) | null>(null)

/** How old the cached crews may be when the palette opens before they are read again. */
const LISTS_STALE_MS = 60_000

export function usePalette() {
  const open = useState<boolean>('paletteOpen', () => false)
  const query = useState<string>('paletteQuery', () => '')
  const crews = useState<CrewSummary[]>('paletteCrews', () => [])
  const catalog = useState<AgentInfo[]>('paletteCatalog', () => [])
  const listsAt = useState<number>('paletteListsAt', () => 0)
  /** A row to open on and ask (X on a session page: its Stop row), taken as the palette opens. */
  const pendingAsk = useState<string | null>('paletteAsk', () => null)
  const api = useSessions()
  const admin = useWorkbenchToken()

  /** A page's group, registered in its setup and gone with it, as useShortcutsModal().registerPage does. */
  function registerPage(get: () => PageGroup | null) {
    pageGetter.value = get
    onBeforeUnmount(() => {
      if (pageGetter.value === get) pageGetter.value = null
    })
  }
  const page = computed<PageGroup | null>(() => pageGetter.value?.() ?? null)

  /**
   * The two lists that need a request (principle 1): read after the first
   * paint of an open palette and cached for the session, read again when
   * they are a minute old or a run started since.
   */
  let loading: Promise<void> | null = null
  function loadLists(force = false): Promise<void> {
    if (!admin.hasToken.value) return Promise.resolve()
    if (loading) return loading
    if (!force && listsAt.value && Date.now() - listsAt.value < LISTS_STALE_MS) return Promise.resolve()
    loading = (async () => {
      try {
        const [c, a] = await Promise.all([api.listCrews().catch(() => ({ crews: crews.value, total: 0 })), api.catalog().catch(() => catalog.value)])
        crews.value = c.crews
        catalog.value = a
        listsAt.value = Date.now()
      } finally {
        loading = null
      }
    })()
    return loading
  }

  function show(mode: PaletteMode = 'search') {
    query.value = mode === 'search' ? '' : MODE_PREFIX[mode]
    open.value = true
  }
  function close() {
    open.value = false
  }
  function toggle() {
    if (open.value) close()
    else show()
  }
  /** Opens on the row `key`, asking its question (a Stop): Enter stops, Escape cancels. */
  function askRow(key: string) {
    pendingAsk.value = key
    query.value = ''
    open.value = true
  }

  return { open, query, show, close, toggle, askRow, pendingAsk, registerPage, page, crews, catalog, loadLists }
}
