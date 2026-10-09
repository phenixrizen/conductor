import type { NvimViewState } from './nvimSwap'

/** A Neovim kept for a tab not in front: its editor id, what its status line showed, and where its cursor was. */
export interface HeldNvim {
  id: string
  state: NvimViewState
  cursor: { line: number; col: number }
}

/**
 * The Neovims of tabs not in front (the Neovim keymap). A tab whose buffer holds changes not written keeps its Neovim while another
 * tab is in front: the editor hands it here as it leaves and takes it back when its tab is in front again, so the changes stay where
 * they were typed. Closing it instead ended it as a lost connection does, and the next open of the file stumbled on its swap file.
 * A tab that closes takes its held Neovim with it (`orphans`: the person said Don't save, or Neovim kept nothing). Keyed by the
 * file's path; one per page connection (`useEditorBridge`).
 */
export class NvimHolds {
  private held = new Map<string, HeldNvim>()

  hold(path: string, h: HeldNvim): void {
    this.held.set(path, h)
  }
  /** The Neovim held for path, left held. */
  peek(path: string): HeldNvim | undefined {
    return this.held.get(path)
  }
  /** The Neovim held for path, no longer held. */
  take(path: string): HeldNvim | undefined {
    const h = this.held.get(path)
    this.held.delete(path)
    return h
  }
  /** Neovim id ended on its own (it quit or died): nothing to take back. */
  ended(id: string): void {
    for (const [path, h] of this.held) if (h.id === id) this.held.delete(path)
  }
  /** The held Neovims whose file has no tab open now, no longer held: the caller ends them. */
  orphans(open: Iterable<string>): Array<{ path: string; id: string }> {
    const keep = new Set(open)
    const out: Array<{ path: string; id: string }> = []
    for (const [path, h] of this.held) {
      if (keep.has(path)) continue
      out.push({ path, id: h.id })
      this.held.delete(path)
    }
    return out
  }
  clear(): void {
    this.held.clear()
  }
  get size(): number {
    return this.held.size
  }
}
