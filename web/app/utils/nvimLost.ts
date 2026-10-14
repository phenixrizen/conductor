/**
 * The editor's Neovim ending (the `nvim_event` `closed`). The person's own quit (`:q`, `:x`, `:qa!`; reason `quit`) closes the tab.
 * Any other end is Neovim lost, not chosen (reason `error: …`: its connection could not be read, it died, it did not answer as it
 * started): the tab stays with the text Neovim last held, read only, and says so, across a tab switched or the area folded
 * (`NvimHolds`); Copy the text keeps it; Reopen lets it go and starts Neovim on the file again, where a swap file the lost one kept
 * offers its changes not written (Recover). Before, the tab closed as on a quit and that text went with no word. Pure.
 */

/** Whether Neovim's end was the person's own quit (no reason, from a server before reasons, counts as one). */
export function closedByQuit(reason?: string): boolean {
  return !reason || reason === 'quit'
}

/**
 * Whether a lost Neovim's text holds changes not written, as far as the page can tell: Neovim said so, a character typed that it never
 * acknowledged shows, or the text is not the file as the tab read it (Neovim may have written it since: the page cannot tell, and keeps
 * it). The tab then wears its dot and closing it asks.
 */
export function lostHoldsChanges(modified: boolean, pending: boolean, text: string, read: string): boolean {
  return modified || pending || text !== read
}

/** The reason a page gives the editors of a connection that ended: they ended with it on the machine, their swap files kept. */
export const CONNECTION_LOST = 'error: the connection to the machine was lost'

/**
 * The editors a connection opened (`opened`), until each says it closed. A connection's end ends them all on the machine (its
 * subscription's Neovims go with it, their swap files kept), and no word of that comes: not on the connection that ended, nor on the
 * next, a new subscription with editors of its own. The page tells its tabs they are lost (CONNECTION_LOST) when the connection
 * closes, or when a welcome names another subscription than the last (a close it did not see). Pure.
 */
export class NvimLive {
  private ids = new Set<string>()
  private subscriber = ''

  /** An nvim_event: `opened` adds its editor, `closed` removes it. */
  note(ev: { kind: string; id?: string }): void {
    if (!ev.id) return
    if (ev.kind === 'opened') this.ids.add(ev.id)
    else if (ev.kind === 'closed') this.ids.delete(ev.id)
  }

  /** A welcome: the editors of an earlier subscription, which ended with it, or none. A welcome naming no subscriber changes nothing. */
  welcome(subscriberId?: string): string[] {
    if (!subscriberId) return []
    const before = this.subscriber
    this.subscriber = subscriberId
    return before && before !== subscriberId ? this.ended() : []
  }

  /** The connection ended: the editors it had, ended with it. */
  ended(): string[] {
    const out = [...this.ids]
    this.ids.clear()
    return out
  }

  /** Forgets everything (the page's terminal changed). */
  clear(): void {
    this.ids.clear()
    this.subscriber = ''
  }
}

/** The banner's words for a Neovim lost, with its reason. */
export function lostWords(reason: string): string {
  const why = reason.replace(/^error:\s*/, '').trim()
  return `Neovim ended unexpectedly${why ? ` (${why})` : ''}. The text here is what it last held, read only, kept until you reopen or close the tab: copy what you need first. Reopen starts it again on the file; a swap file it kept offers its changes not written.`
}
