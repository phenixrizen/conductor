/**
 * A Neovim buffer change as Monaco sees it (design round 12, F8): the
 * `lines` event says the 0-based lines [first, last) became `lines` (last
 * -1: to the end). The edit replaces that range in a Monaco model whose
 * text is kept equal to the buffer, with the buffer's lines joined by "\n".
 */
export interface LinesEvent {
  first: number
  last: number
  lines: string[]
}

export interface LineRange {
  startLineNumber: number
  startColumn: number
  endLineNumber: number
  endColumn: number
}

/** The Monaco range and text replacing the model's lines for the event; lineCount is the model's current line count. */
export function linesEdit(ev: LinesEvent, lineCount: number, lineLength: (line: number) => number): { range: LineRange; text: string } {
  const first = Math.max(0, Math.min(ev.first, lineCount))
  const last = ev.last < 0 ? lineCount : Math.max(first, Math.min(ev.last, lineCount))
  const text = ev.lines.join('\n')
  // Replacing whole lines: the range runs from the start of `first` to the end
  // of `last - 1`; a pure insertion at the end (first == lineCount) appends
  // after the last line with a leading newline, one before the first line
  // adds a trailing one, so the join stays a line boundary.
  if (last === first) {
    if (first >= lineCount) {
      const endLine = Math.max(1, lineCount)
      return { range: { startLineNumber: endLine, startColumn: lineLength(endLine) + 1, endLineNumber: endLine, endColumn: lineLength(endLine) + 1 }, text: lineCount === 0 && first === 0 ? text : '\n' + text }
    }
    return { range: { startLineNumber: first + 1, startColumn: 1, endLineNumber: first + 1, endColumn: 1 }, text: text + '\n' }
  }
  if (ev.lines.length === 0) {
    // A deletion: take the lines and one of the newlines around them.
    if (last >= lineCount) {
      if (first === 0) return { range: { startLineNumber: 1, startColumn: 1, endLineNumber: lineCount, endColumn: lineLength(lineCount) + 1 }, text: '' }
      return { range: { startLineNumber: first, startColumn: lineLength(first) + 1, endLineNumber: lineCount, endColumn: lineLength(lineCount) + 1 }, text: '' }
    }
    return { range: { startLineNumber: first + 1, startColumn: 1, endLineNumber: last + 1, endColumn: 1 }, text: '' }
  }
  return { range: { startLineNumber: first + 1, startColumn: 1, endLineNumber: last, endColumn: lineLength(last) + 1 }, text }
}

/** applyLines is linesEdit on an array of lines, for the tests and the model's shadow. */
export function applyLines(lines: string[], ev: LinesEvent): string[] {
  const first = Math.max(0, Math.min(ev.first, lines.length))
  const last = ev.last < 0 ? lines.length : Math.max(first, Math.min(ev.last, lines.length))
  return [...lines.slice(0, first), ...ev.lines, ...lines.slice(last)]
}

/** The UTF-8 length of one character. */
function utf8Len(ch: string): number {
  const cp = ch.codePointAt(0) ?? 0
  return cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4
}

/**
 * Neovim's column is a byte's (1-based, of the line's UTF-8); Monaco's a UTF-16 unit's. The Monaco column of Neovim's byteCol on a line,
 * so the cursor sits right after a multibyte character.
 */
export function byteColToUtf16(line: string, byteCol: number): number {
  const target = Math.max(0, byteCol - 1)
  let bytes = 0
  let units = 0
  for (const ch of line) {
    if (bytes >= target) break
    bytes += utf8Len(ch)
    units += ch.length
  }
  return units + 1
}
