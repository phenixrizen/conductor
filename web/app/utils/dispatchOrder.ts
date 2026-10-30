/**
 * The ordering gate every Dispatch page's state goes through (docs/plans/dispatch.md, "web/app"): the RunStore's rule (utils/runs.ts,
 * `take`) for each thing a page shows, a ticket by its key, a list by its filter, the project, the projects. Every request a page sends,
 * a read or an operation, is numbered as it is sent (`ask`); its reply is applied only when its number is above the number of the last
 * reply applied for the same thing (`take`), else dropped, so a slow reply never replaces a newer one. A request that fails is never
 * taken: it applies nothing and moves nothing, so a success it overtook still lands, and only a later applied reply supersedes an earlier
 * one. A reply carrying an entity whose `rev` is below the one shown is dropped whatever its number: the store's `rev` orders the states of
 * an entity, which the numbers, saying only when a request was sent, cannot (a list carries no `rev` and is taken by number alone). A thing
 * no longer shown is dropped (`drop`: a filter changed is a new thing), so its late replies are dropped, and the thing asked for again
 * starts afresh. Pure: the pages hold one each, vitest proves it.
 */
export class DispatchOrder {
  /** The number of the last request asked: every request's number is above every number before it. */
  private last = 0
  /** By thing: the number of the last reply applied. */
  private readonly applied = new Map<string, number>()
  /** By thing: the `rev` of the entity shown, for a thing whose replies carry one. */
  private readonly revs = new Map<string, number>()
  /** By thing: the replies to requests numbered at or below this were asked before the thing was dropped. */
  private readonly floors = new Map<string, number>()

  /** The number of a request about to be sent. */
  ask(): number {
    return ++this.last
  }

  /**
   * Whether the reply to request `n` about `thing` is to be applied: its number above that of the last reply applied for the thing and
   * of every request asked before the thing was last dropped, and `rev`, the revision of the entity it carries (none for a list), not
   * below the one shown. When it is, it is recorded as the reply applied, its `rev` as the one shown.
   */
  take(thing: string, n: number, rev?: number): boolean {
    if (n <= (this.floors.get(thing) ?? 0) || n <= (this.applied.get(thing) ?? 0)) return false
    if (rev !== undefined && rev < (this.revs.get(thing) ?? 0)) return false
    this.applied.set(thing, n)
    if (rev !== undefined) this.revs.set(thing, rev)
    return true
  }

  /** The `rev` of the entity shown for `thing`, 0 when none was taken: what the next operation on it expects. */
  rev(thing: string): number {
    return this.revs.get(thing) ?? 0
  }

  /** Forgets a thing no longer shown: a reply to any request asked until now is dropped, and a request asked after it starts afresh. */
  drop(thing: string): void {
    this.floors.set(thing, this.last)
    this.applied.delete(thing)
    this.revs.delete(thing)
  }
}

/**
 * Sends one request about `thing` through the gate: numbered as it is sent, its reply applied (`apply`) only when the gate takes it, the
 * entity's revision read from the reply by `rev` (none for a list). Resolves to whether the reply was applied; a request that fails
 * rejects with its error and applies nothing.
 */
export async function through<T>(gate: DispatchOrder, thing: string, send: () => Promise<T>, apply: (reply: T) => void, rev?: (reply: T) => number | undefined): Promise<boolean> {
  const n = gate.ask()
  const reply = await send()
  if (!gate.take(thing, n, rev?.(reply))) return false
  apply(reply)
  return true
}
