import { shellQuote } from './hostCommand'

// One argument per match: a double-quoted group, a single-quoted group, or a
// run of non-space characters. A quote only opens a group at the start of an
// argument, so it's and --x="a are plain characters.
const argToken = () => /"([^"]*)"|'([^']*)'|(\S+)/g

/** Minimal shell-like splitting: whitespace separated, quotes group. Nothing is expanded. */
export function splitArgs(s: string): string[] {
  const out: string[] = []
  for (const m of s.matchAll(argToken())) out.push(m[1] ?? m[2] ?? m[3] ?? '')
  return out
}

/**
 * True while the text ends inside a quoted argument that splitArgs would not
 * close, as in `--model "gpt`. Typing a space there belongs to the argument.
 */
export function hasOpenQuote(s: string): boolean {
  for (const m of s.matchAll(argToken())) {
    // Only the bare-word branch can start with a quote, and only when no closing quote follows.
    if (m[3] !== undefined && (m[3].startsWith('"') || m[3].startsWith("'"))) return true
  }
  return false
}

/** A catalog ID (`[a-z0-9-]`, at most 32 characters, no dash at either end) suggested by a display name; empty when nothing usable is left. */
export function slugId(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32)
    .replace(/-+$/, '')
}

/** An argv as one line for display, quoting only what needs it. Never fed to a shell. */
export function joinArgv(argv: string[]): string {
  return argv.map(shellQuote).join(' ')
}
