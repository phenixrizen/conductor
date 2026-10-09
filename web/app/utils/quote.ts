import { MAX_QUOTE_LINE, MAX_QUOTE_LINES, MAX_QUOTE_PATH, type ChatQuote } from './protocol'

/**
 * Comments on lines of a file (design round 12, F7): the quote a selection
 * makes, its words, and where its lines are in the file now. Pure.
 */

const enc = new TextEncoder()

/** s cut to at most n UTF-8 bytes on a character boundary. */
export function cutBytes(s: string, n: number): string {
  if (enc.encode(s).length <= n) return s
  let out = ''
  let used = 0
  for (const ch of s) {
    const b = enc.encode(ch).length
    if (used + b > n) break
    out += ch
    used += b
  }
  return out
}

/** The path a quote names: relative to the working directory when the file is inside it. */
export function quotePath(abs: string, cwd?: string): string {
  const root = (cwd || '').replace(/\/+$/, '')
  return root && abs.startsWith(root + '/') ? abs.slice(root.length + 1) : abs
}

/** The quote of lines from..to (1-based) of a file's lines, bounded as the server keeps it. */
export function makeQuote(path: string, fileLines: readonly string[], from: number, to: number): ChatQuote {
  const lo = Math.max(1, Math.min(from, to))
  const hi = Math.max(lo, Math.min(Math.max(from, to), Math.max(fileLines.length, 1)))
  const lines = fileLines.slice(lo - 1, hi).map((l) => cutBytes(l, MAX_QUOTE_LINE))
  const cut = lines.length > MAX_QUOTE_LINES
  return { path: cutBytes(path, MAX_QUOTE_PATH), from: lo, to: hi, lines: lines.slice(0, MAX_QUOTE_LINES), ...(cut ? { cut } : {}) }
}

/** "internal/api/users.go:14", "internal/api/users.go:14–16". */
export function quoteLocation(q: Pick<ChatQuote, 'path' | 'from' | 'to'>): string {
  return q.to > q.from ? `${q.path}:${q.from}–${q.to}` : `${q.path}:${q.from}`
}

/** What a comment's quote shows beside a message: "lines 14–16", "line 14". */
export function rangeWords(from: number, to: number): string {
  return to > from ? `lines ${from}–${to}` : `line ${from}`
}

/** Where the quoted lines are in the file now: where they were, moved (with the new range), or nowhere (changed). */
export type Relocation = { state: 'same' } | { state: 'moved'; from: number; to: number } | { state: 'changed' }

export function relocate(q: ChatQuote, fileLines: readonly string[]): Relocation {
  const want = q.lines
  if (!want.length) return { state: 'same' }
  const span = q.to - q.from + 1
  const at = (start: number) => want.every((l, i) => cutBytes(fileLines[start + i] ?? '\u0000', MAX_QUOTE_LINE) === l)
  if (at(q.from - 1)) return { state: 'same' }
  // The nearest place the lines are now, either way from where they were.
  let best = -1
  for (let i = 0; i + want.length <= fileLines.length; i++) {
    if (at(i) && (best < 0 || Math.abs(i - (q.from - 1)) < Math.abs(best - (q.from - 1)))) best = i
  }
  if (best < 0) return { state: 'changed' }
  return { state: 'moved', from: best + 1, to: best + span }
}

/** The words under a quote card for where its lines are now. */
export function relocationWords(r: Relocation | null | undefined): string {
  if (!r || r.state === 'same') return ''
  if (r.state === 'moved') return `Lines moved since · now ${r.to > r.from ? `${r.from}–${r.to}` : r.from}`
  return 'Lines changed since'
}

/** What Copy puts on the clipboard: the location, then the lines. */
export function copyText(q: ChatQuote): string {
  return `${quoteLocation(q).replace('–', '-')}\n${q.lines.join('\n')}`
}
