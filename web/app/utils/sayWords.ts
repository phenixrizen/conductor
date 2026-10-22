/**
 * What the palette says after a line typed into a session (#303, #322): typed
 * when the owner said the line was entered, sent when an older owner said
 * nothing, and not sent with why when it was refused, the line then kept
 * for the row's field. Pure.
 */
export interface SayToast {
  title: string
  description: string
  color: 'success' | 'neutral' | 'warning'
}

export type SayOutcome = { typed: boolean } | { refused: string }

/** The line as the toast shows it: cut past 80 characters. */
export function sayLine(line: string): string {
  return line.length > 80 ? `${line.slice(0, 80)}…` : line
}

export function sayWords(name: string, line: string, outcome: SayOutcome): { toast: SayToast; keepDraft: boolean } {
  const short = sayLine(line)
  if ('refused' in outcome) {
    return { toast: { title: `${name}: not sent`, description: `${outcome.refused} · the line waits in the row's field`, color: 'warning' }, keepDraft: true }
  }
  if (outcome.typed) return { toast: { title: `Typed into ${name}`, description: `${short} · entered, the session's owner says`, color: 'success' }, keepDraft: false }
  return {
    toast: { title: `Sent to ${name}`, description: `${short} · the session's owner types it and its Enter; a refusal shows here, nothing confirms it landed`, color: 'neutral' },
    keepDraft: false,
  }
}
