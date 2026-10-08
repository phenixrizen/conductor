import { diffAgainst, type ChangeRow } from '~/utils/changes'
import { emptyTabs, openTab, toggleFold, type TabsState } from '~/utils/editorTabs'

/**
 * The files a page has open in its editor area (design 4b): the tabs, how a
 * path or a URL the agent printed opens, and the fold (T; Alt+T while
 * typing in a terminal). One per page: the session page, the Yard's
 * focused tile, a guest's join page.
 */
export function useEditorTabs() {
  const toast = useToast()
  const tabs = ref<TabsState>(emptyTabs())
  const editorOpen = computed(() => tabs.value.tabs.length > 0 && !tabs.value.folded)

  /** A file (a path the agent printed, one chosen in the Files pane, one typed there) opens in the editor area. */
  function openFile(loc: { path: string; line?: number }) {
    tabs.value = openTab(tabs.value, 'file', loc.path, loc.line)
  }

  /** A URL the agent printed: a new tab, or a preview tab in the area. */
  function openUrl(url: string) {
    toast.add({
      title: url,
      icon: 'i-lucide-link',
      color: 'neutral',
      actions: [
        { label: 'Open in new tab', icon: 'i-lucide-external-link', onClick: () => window.open(url, '_blank', 'noopener,noreferrer') },
        { label: 'Preview in pane', icon: 'i-lucide-panel-right', onClick: () => (tabs.value = openTab(tabs.value, 'url', url)) },
      ],
    })
  }

  /** A change from the Changes section opens as a diff against the base (design 4d). */
  function openDiff(c: ChangeRow, against: { top: string; branch?: string; base?: string; baseId?: string }) {
    tabs.value = openTab(tabs.value, 'diff', c.abs, undefined, { status: c.status, added: c.added, removed: c.removed, base: against.base, against: diffAgainst({ branch: against.branch, base: against.baseId }, against.base) })
  }

  function fold() {
    if (tabs.value.tabs.length) tabs.value = toggleFold(tabs.value)
  }

  defineShortcuts({
    t: fold,
    alt_t: { usingInput: true, handler: fold },
  })

  return { tabs, editorOpen, openFile, openUrl, openDiff, fold }
}
