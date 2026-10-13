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

/** The banner's words for a Neovim lost, with its reason. */
export function lostWords(reason: string): string {
  const why = reason.replace(/^error:\s*/, '').trim()
  return `Neovim ended unexpectedly${why ? ` (${why})` : ''}. The text here is what it last held, read only, kept until you reopen or close the tab: copy what you need first. Reopen starts it again on the file; a swap file it kept offers its changes not written.`
}
