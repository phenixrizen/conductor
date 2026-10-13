import type { NvimEcho } from './nvimEcho'
import type { NvimViewState } from './nvimSwap'
import { closedByQuit } from './nvimLost'

/**
 * A Neovim kept for a tab not in front: its editor id, what its status line showed, and where its cursor was. A Neovim lost (not quit,
 * `utils/nvimLost`) is kept too, `state.lost` its reason: its tab's text is what it last held, until Reopen or a close. `stop` ends the
 * following of its changes into the tab's text while it is held (the editor's `follow`): a change on its way as the tab was left, one a
 * plugin makes later, a lost Neovim's last ones, which can come after its end. `echo` is the local echo as it left, finished: the
 * characters typed that Neovim had not acknowledged, laid over its text and reconciled with each of those changes (NvimEcho.lateLines).
 */
export interface HeldNvim {
  id: string
  state: NvimViewState
  cursor: { line: number; col: number }
  stop?: () => void
  echo?: NvimEcho | null
}

/**
 * The Neovims of tabs not in front (the Neovim keymap). A tab whose buffer holds changes not written keeps its Neovim while another
 * tab is in front: the editor hands it here as it leaves and takes it back when its tab is in front again, so the changes stay where
 * they were typed. Closing it instead ended it as a lost connection does, and the next open of the file stumbled on its swap file.
 * A tab that closes takes its held Neovim with it (`orphans`: the person said Don't save, or Neovim kept nothing). Keyed by the
 * file's path; one per page connection (`useEditorBridge`). A Neovim lost stays held as lost, whether it was in front
 * (its editor hands it here as it leaves, a tab switched or the area folded) or not (`ended`), so the next showing keeps its text.
 * Whatever leaves the holds stops being followed.
 */
export class NvimHolds {
  private held = new Map<string, HeldNvim>()

  hold(path: string, h: HeldNvim): void {
    const was = this.held.get(path)
    if (was && was !== h) was.stop?.()
    this.held.set(path, h)
  }
  /** The Neovim held for path, left held. */
  peek(path: string): HeldNvim | undefined {
    return this.held.get(path)
  }
  /** The Neovim held for path, no longer held nor followed: its editor follows it again. */
  take(path: string): HeldNvim | undefined {
    const h = this.held.get(path)
    this.held.delete(path)
    h?.stop?.()
    return h ? { id: h.id, state: h.state, cursor: h.cursor, echo: h.echo } : undefined
  }
  /**
   * Neovim id ended on its own: quit, nothing to take back; lost (reason `error: …`), its tab keeps what it held, as lost, still
   * following the changes it sent before its end.
   */
  ended(id: string, reason?: string): void {
    for (const [path, h] of this.held) {
      if (h.id !== id || h.state.lost) continue
      if (closedByQuit(reason)) {
        h.stop?.()
        this.held.delete(path)
      } else this.held.set(path, { ...h, state: { ...h.state, lost: reason, cmdline: '' } })
    }
  }
  /** The held Neovims whose file has no tab open now, no longer held: the caller ends them, but for a lost one, which has none to end. */
  orphans(open: Iterable<string>): Array<{ path: string; id: string; lost: boolean }> {
    const keep = new Set(open)
    const out: Array<{ path: string; id: string; lost: boolean }> = []
    for (const [path, h] of this.held) {
      if (keep.has(path)) continue
      h.stop?.()
      out.push({ path, id: h.id, lost: !!h.state.lost })
      this.held.delete(path)
    }
    return out
  }
  clear(): void {
    for (const h of this.held.values()) h.stop?.()
    this.held.clear()
  }
  get size(): number {
    return this.held.size
  }
}
