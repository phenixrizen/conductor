/**
 * Reads of one thing that may overlap (a watcher that reads again at each revision of a run, whose replies can arrive in any order):
 * `read` starts one and applies its value, or hands its error to `fail`, only while it is the last read started, so a reply asked
 * for earlier that arrives last never replaces a newer one; `drop` makes every read under way stale (the thing is gone, or no longer
 * read).
 */
export function latestReads() {
  let last = 0
  return {
    async read<T>(fetch: () => Promise<T>, apply: (value: T) => void, fail: (error: unknown) => void): Promise<void> {
      const mine = ++last
      let value: T
      try {
        value = await fetch()
      } catch (e) {
        if (mine === last) fail(e)
        return
      }
      if (mine === last) apply(value)
    },
    drop() {
      last++
    },
  }
}
