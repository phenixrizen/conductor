import type { RunInfo } from './useSessions'

/**
 * The runs of one crew: the live ones the store holds (`live`, run events keep them current) and the records the server kept of those
 * that ended (GET /api/crews/{id}/runs), read when the caller mounts, when the crew changes, and again, a moment later, whenever the
 * live runs change. `runs` lists them newest first, the live ones first; `recorded` only the records.
 */
export function useCrewRecords(crewId: MaybeRefOrGetter<string>, live: Ref<RunInfo[]>) {
  const api = useSessions()
  const recorded = ref<RunInfo[]>([])
  const error = ref('')
  let timer: number | undefined
  let seq = 0

  async function read() {
    const id = toValue(crewId)
    const n = ++seq
    if (!id) {
      recorded.value = []
      return
    }
    try {
      const r = await api.crewRuns(id)
      if (n !== seq) return
      recorded.value = r.runs.slice(r.live)
      error.value = ''
    } catch (e) {
      if (n === seq) error.value = (e as Error).message
    }
  }
  function later() {
    window.clearTimeout(timer)
    timer = window.setTimeout(read, 1000)
  }
  onMounted(read)
  onBeforeUnmount(() => window.clearTimeout(timer))
  watch(
    () => toValue(crewId),
    () => {
      recorded.value = []
      error.value = ''
      read()
    },
  )
  watch(() => live.value.map((r) => `${r.id}:${r.state}:${r.stoppedAt ?? ''}`).join(','), later)

  const runs = computed(() => {
    const l = [...live.value].sort((a, b) => b.startedAt.localeCompare(a.startedAt))
    const ids = new Set(l.map((r) => r.id))
    return [...l, ...recorded.value.filter((r) => !ids.has(r.id))]
  })
  return { recorded, runs, error, read }
}
