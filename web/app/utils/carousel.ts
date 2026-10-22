// The Roundhouse's strip (#310): the agents at the foot rearranged by drag
// or keyboard, the order kept per browser with the Roundhouse's settings;
// stepping, autoplay, "Next up" and follow mode go in the kept order. A
// button switches the strip between chips and thumbnails, kept the same
// way. Pure.

export type StripView = 'chips' | 'thumbs'

export interface CarouselSettings {
  intervalMs: number
  autoplay: boolean
  follow: boolean
  /** The strip's order, by session id; a session the order does not know goes at the end, one that ended leaves it. */
  order: string[]
  strip: StripView
}

export const CAROUSEL_DEFAULTS: CarouselSettings = { intervalMs: 10000, autoplay: true, follow: true, order: [], strip: 'chips' }

/** The settings as kept, over the defaults, each field checked: a value of another shape is the default. */
export function normalizeSettings(raw: unknown): CarouselSettings {
  const v = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  return {
    intervalMs: typeof v.intervalMs === 'number' && v.intervalMs >= 1000 && v.intervalMs <= 600_000 ? v.intervalMs : CAROUSEL_DEFAULTS.intervalMs,
    autoplay: typeof v.autoplay === 'boolean' ? v.autoplay : CAROUSEL_DEFAULTS.autoplay,
    follow: typeof v.follow === 'boolean' ? v.follow : CAROUSEL_DEFAULTS.follow,
    order: Array.isArray(v.order) ? v.order.filter((id): id is string => typeof id === 'string').slice(0, 500) : [],
    strip: v.strip === 'thumbs' ? 'thumbs' : 'chips',
  }
}

/**
 * The sessions in the kept order: those the order names first, in its
 * order, then the rest as `active` lists them (a session started since
 * goes at the end); a session no longer active is not in the result, and
 * `keptOrder` says what to keep after.
 */
export function orderSessions<T extends { id: string }>(active: readonly T[], order: readonly string[]): T[] {
  const byId = new Map(active.map((s) => [s.id, s]))
  const out: T[] = []
  const seen = new Set<string>()
  for (const id of order) {
    const s = byId.get(id)
    if (s && !seen.has(id)) {
      out.push(s)
      seen.add(id)
    }
  }
  for (const s of active) if (!seen.has(s.id)) out.push(s)
  return out
}

/** The order to keep: the ids of the sessions shown, in their order (one that ended leaves it). */
export function keptOrder<T extends { id: string }>(ordered: readonly T[]): string[] {
  return ordered.map((s) => s.id)
}

/** `ids` with the item at `from` moved to `to` (both indexes into the list); an index out of the list leaves it as it is. */
export function moveInOrder(ids: readonly string[], from: number, to: number): string[] {
  if (from < 0 || from >= ids.length || to < 0 || to >= ids.length || from === to) return [...ids]
  const out = [...ids]
  const [it] = out.splice(from, 1)
  out.splice(to, 0, it!)
  return out
}

/**
 * The index a drop lands at: the chip the pointer is over, and after it
 * when the pointer is past its middle along the strip's axis. `over` is
 * the chip's index, `ratio` the pointer's position within it (0 at its
 * start, 1 at its end).
 */
export function dropIndex(over: number, ratio: number, from: number): number {
  const after = ratio > 0.5
  let to = after ? over + 1 : over
  if (from < to) to -= 1
  return to
}

/**
 * A keyboard move on the strip: Space (or Enter) picks the focused chip up
 * or drops it, the arrows move the picked chip one place, Escape drops it
 * where it was picked. `grabbed` is the picked chip's index, null for none.
 */
export function keyMove(ids: readonly string[], focused: number, grabbed: number | null, key: string): { ids: string[]; grabbed: number | null; focused: number; done?: 'dropped' | 'cancelled' } {
  const list = [...ids]
  if (key === ' ' || key === 'Enter') {
    if (grabbed === null) return { ids: list, grabbed: focused, focused }
    return { ids: list, grabbed: null, focused: grabbed, done: 'dropped' }
  }
  if (key === 'Escape') return grabbed === null ? { ids: list, grabbed: null, focused } : { ids: list, grabbed: null, focused: grabbed, done: 'cancelled' }
  if (grabbed === null) return { ids: list, grabbed, focused }
  const delta = key === 'ArrowLeft' || key === 'ArrowUp' ? -1 : key === 'ArrowRight' || key === 'ArrowDown' ? 1 : 0
  if (!delta) return { ids: list, grabbed, focused }
  const to = grabbed + delta
  if (to < 0 || to >= list.length) return { ids: list, grabbed, focused }
  return { ids: moveInOrder(list, grabbed, to), grabbed: to, focused: to }
}
