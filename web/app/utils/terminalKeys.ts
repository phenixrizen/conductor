/**
 * The keys a terminal handles before xterm does.
 *
 * Enter with Shift or Ctrl is a newline in the agent's prompt, not a submit:
 * xterm would send a plain carriage return for it, which every agent reads as
 * Enter. ESC CR is what Claude Code's own `/terminal-setup` teaches VS Code
 * and iTerm2 to send for Shift+Enter, and what a terminal sends for
 * Option+Enter: Claude Code reads it as a newline, and Codex reads it as
 * Alt+Enter, a newline too.
 */
export const NEWLINE_IN_PROMPT = '\x1b\r'

/** Shift+Enter or Ctrl+Enter, with neither Alt nor Meta. */
export function newlineChord(e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'altKey' | 'metaKey'>): boolean {
  return e.key === 'Enter' && (e.shiftKey || e.ctrlKey) && !e.altKey && !e.metaKey
}
