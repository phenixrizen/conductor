import type { ActivityEntry, FileRequester, TouchedFile } from '~/utils/protocol'
import { touchedPaths, touchedRows, type TouchedRow } from '~/utils/touched'

/** A read that fails (the terminal not connected yet, as the pane mounts beside it) is tried again this many times, this far apart. */
export const TOUCHED_RETRIES = 3
export const TOUCHED_RETRY_MS = 2000

/**
 * The files the agent touched (design 4e; round 13, G1; an Activity filter
 * since 4h, #301): the session's own index (the `touched` file request),
 * asked for when the pane shows, when the working directory changes and
 * whenever the Files filter opens, with the `file` activity entries the page
 * sees laid over it. An older session without the index: the entries alone.
 * A read that fails is tried again a few times (the pane mounts while the
 * terminal still connects), and `load` may be asked for again by whoever
 * shows the Explorer (Codex's round on #301).
 */
export function useTouched(request: FileRequester, cwd: () => string | undefined, activity: () => ActivityEntry[] | undefined) {
  const index = ref<TouchedFile[] | null>(null)
  let retryTimer: number | undefined
  let disposed = false
  let attempt = 0
  async function load(tries = TOUCHED_RETRIES) {
    const dir = cwd()
    if (!dir || !activity() || disposed) return
    window.clearTimeout(retryTimer)
    const mine = ++attempt
    try {
      const res = await request(dir, false, { op: 'touched' })
      if (res.header.kind === 'touched' && mine === attempt) index.value = res.header.touched ?? []
    } catch {
      // The page's own entries stand in; a later try may find the terminal connected.
      if (tries > 1 && !disposed && mine === attempt) retryTimer = window.setTimeout(() => void load(tries - 1), TOUCHED_RETRY_MS)
    }
  }
  const rows = computed<TouchedRow[]>(() => {
    const dir = cwd()
    const entries = activity()
    return dir && entries ? touchedRows(entries, dir, index.value) : []
  })
  const paths = computed(() => touchedPaths(rows.value))
  watch(cwd, () => {
    index.value = null
    void load()
  })
  onMounted(() => void load())
  onBeforeUnmount(() => {
    disposed = true
    window.clearTimeout(retryTimer)
  })
  return { index, rows, paths, load }
}
