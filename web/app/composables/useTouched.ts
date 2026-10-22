import type { ActivityEntry, FileRequester, TouchedFile } from '~/utils/protocol'
import { touchedPaths, touchedRows, type TouchedRow } from '~/utils/touched'

/**
 * The files the agent touched (design 4e; round 13, G1; an Activity filter
 * since 4h, #301): the session's own index (the `touched` file request),
 * asked for when the pane shows, when the working directory changes and
 * whenever the Files filter opens, with the `file` activity entries the page
 * sees laid over it. An older session without the index: the entries alone.
 */
export function useTouched(request: FileRequester, cwd: () => string | undefined, activity: () => ActivityEntry[] | undefined) {
  const index = ref<TouchedFile[] | null>(null)
  async function load() {
    const dir = cwd()
    if (!dir || !activity()) return
    try {
      const res = await request(dir, false, { op: 'touched' })
      if (res.header.kind === 'touched') index.value = res.header.touched ?? []
    } catch {
      // The page's own entries stand in.
    }
  }
  const rows = computed<TouchedRow[]>(() => {
    const dir = cwd()
    const entries = activity()
    return dir && entries ? touchedRows(entries, dir, index.value) : []
  })
  const paths = computed(() => touchedPaths(rows.value))
  watch(cwd, () => void load())
  onMounted(() => void load())
  return { index, rows, paths, load }
}
