import type { FileHeader, FileRequester } from '~/utils/protocol'
import { changeMarks, changeRows, type ChangeRow } from '~/utils/changes'
import { commitRows, type CommitRow } from '~/utils/commits'
import { pollInterval, type InspectorView } from '~/utils/inspector'

/**
 * The working directory's git status and log, polled (design 4h, #301):
 * owned by the inspector (or a Files aside) rather than the Files pane, so
 * the rail's Changes and Commits pills stay right while the panel shows
 * another view or is collapsed. The status every 5 s while Changes shows,
 * the log every 10 s while Commits shows, each every 30 s otherwise, none
 * while the document is hidden; both read once after the first paint. One
 * request at a time per kind, so the per-connection bounds hold.
 */
export interface RepoStatus {
  status: Ref<FileHeader | null>
  statusError: Ref<string>
  statusLoading: Ref<boolean>
  statusAt: Ref<number>
  log: Ref<FileHeader | null>
  logError: Ref<string>
  logLoading: Ref<boolean>
  notRepo: ComputedRef<boolean>
  logNotRepo: ComputedRef<boolean>
  changes: ComputedRef<ChangeRow[]>
  commits: ComputedRef<CommitRow[]>
  /** The Explorer's marks beside changed files: their status letter by path. */
  marks: ComputedRef<ReturnType<typeof changeMarks>>
  loadStatus: () => Promise<void>
  loadLog: () => Promise<void>
}

export function useRepoStatus(request: FileRequester, cwd: () => string | undefined, base: () => string | undefined, shown: () => InspectorView | null): RepoStatus {
  const status = ref<FileHeader | null>(null)
  const statusError = ref('')
  const statusLoading = ref(false)
  const statusAt = ref(0)
  const log = ref<FileHeader | null>(null)
  const logError = ref('')
  const logLoading = ref(false)
  const notRepo = computed(() => status.value?.kind === 'error' && status.value.error?.code === 'not_repo')
  const logNotRepo = computed(() => log.value?.kind === 'error' && log.value.error?.code === 'not_repo')
  const changes = computed(() => (status.value?.kind === 'status' ? changeRows(status.value.path, status.value.changes ?? []) : []))
  const commits = computed<CommitRow[]>(() => commitRows(log.value))
  const marks = computed<ReturnType<typeof changeMarks>>(() => (status.value?.kind === 'status' ? changeMarks(status.value.path, status.value.changes ?? []) : new Map()))

  async function loadStatus() {
    const dir = cwd()
    if (!dir || statusLoading.value) return
    statusLoading.value = true
    try {
      const res = await request(dir, false, { op: 'status', base: base() })
      status.value = res.header
      statusError.value = res.header.kind === 'error' && res.header.error?.code !== 'not_repo' ? res.header.error?.message || 'cannot read the status' : ''
      statusAt.value = Date.now()
    } catch (e) {
      statusError.value = (e as Error).message
    } finally {
      statusLoading.value = false
    }
  }
  async function loadLog() {
    const dir = cwd()
    if (!dir || logLoading.value) return
    logLoading.value = true
    try {
      const res = await request(dir, false, { op: 'log', base: base() })
      log.value = res.header
      logError.value = res.header.kind === 'error' && res.header.error?.code !== 'not_repo' ? res.header.error?.message || 'cannot read the commits' : ''
    } catch (e) {
      logError.value = (e as Error).message
    } finally {
      logLoading.value = false
    }
  }

  // Each kind on its own timer, re-armed at the cadence the view shown asks for. Once the owner is unmounted nothing re-arms: a
  // request in flight at that moment settles and arms no timer (Codex's round on #301).
  let statusTimer: number | undefined
  let logTimer: number | undefined
  let firstTimer: number | undefined
  let disposed = false
  const visible = () => typeof document === 'undefined' || document.visibilityState !== 'hidden'
  function armStatus() {
    window.clearTimeout(statusTimer)
    if (disposed) return
    statusTimer = window.setTimeout(async () => {
      if (visible() && !disposed) await loadStatus()
      armStatus()
    }, pollInterval('changes', shown()))
  }
  function armLog() {
    window.clearTimeout(logTimer)
    if (disposed) return
    logTimer = window.setTimeout(async () => {
      if (visible() && !disposed) await loadLog()
      armLog()
    }, pollInterval('commits', shown()))
  }
  watch(shown, (v) => {
    // The view just shown reads at once, then at its own pace.
    if (v === 'changes') void loadStatus()
    if (v === 'commits') void loadLog()
    armStatus()
    armLog()
  })
  watch(cwd, () => {
    status.value = null
    log.value = null
    void loadStatus()
    void loadLog()
  })
  onMounted(() => {
    // The pills, and the Explorer's marks: one read of each when the pane is up.
    firstTimer = window.setTimeout(() => {
      if (disposed) return
      void loadStatus()
      void loadLog()
    }, 1500)
    armStatus()
    armLog()
  })
  onBeforeUnmount(() => {
    disposed = true
    window.clearTimeout(firstTimer)
    window.clearTimeout(statusTimer)
    window.clearTimeout(logTimer)
  })

  return { status, statusError, statusLoading, statusAt, log, logError, logLoading, notRepo, logNotRepo, changes, commits, marks, loadStatus, loadLog }
}
