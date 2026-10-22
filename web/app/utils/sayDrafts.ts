/**
 * The lines sessions refused, kept for the palette rows' fields (#322): one
 * per session. Each send begins a generation for its session, and settles
 * with it: a refusal keeps the line, any other outcome drops it, unless a
 * newer send for the session began since, whose draft an earlier send's
 * late word must not erase (the whole-batch round). Pure.
 */
export class SayDrafts {
  private readonly drafts = new Map<string, string>()
  private readonly gens = new Map<string, number>()

  /** A send for the session begins: its generation, to settle with. */
  begin(id: string): number {
    const g = (this.gens.get(id) ?? 0) + 1
    this.gens.set(id, g)
    return g
  }

  /** The send of generation `gen` settled: the line kept (a refusal) or dropped, unless a newer send began since. */
  settle(id: string, gen: number, keep: boolean, line: string): void {
    if (this.gens.get(id) !== gen) return
    if (keep) this.drafts.set(id, line)
    else this.drafts.delete(id)
  }

  get(id: string): string | undefined {
    return this.drafts.get(id)
  }
}
