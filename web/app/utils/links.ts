// Detection of file locations in terminal output for clickable links.

export interface FileLocation {
  path: string
  line?: number
  col?: number
  /** Start and end column (0-based, end exclusive) in the scanned text. */
  start: number
  end: number
}

const EXT =
  'go|ts|tsx|js|jsx|mjs|cjs|vue|svelte|py|rs|java|kt|rb|php|c|h|cc|cpp|hpp|cs|swift|m|json|ya?ml|toml|md|txt|sh|bash|zsh|sql|css|scss|less|html?|xml|env|lock|mod|sum|ini|cfg|conf|dockerfile|makefile|proto|graphql|tf'

// path-like tokens: absolute, home, dotted-relative, or containing a slash,
// or a bare file name with a known extension. Followed optionally by :line[:col]
// or (line,col).
const PATH_RE = new RegExp(
  String.raw`(?<![\w@:/.-])((?:~|\.{1,2})?/(?:[\w.@%+-]+/)*[\w.@%+-]+|(?:[\w.@%+-]+/)+[\w.@%+-]+|[\w@%+-]+(?:\.[\w-]+)*\.(?:${EXT}))(?::(\d+)(?::(\d+))?|\((\d+)(?:,(\d+))?\))?(?![\w/])`,
  'gi',
)

// Python tracebacks: File "x/y.py", line 12
const PY_RE = /File "([^"\n]+)", line (\d+)/g

const URL_PREFIX = /^[a-z][a-z0-9+.-]*:\/\//i

/** Finds file locations in one line of text. URLs are left to the web-links addon. */
export function findFileLocations(text: string): FileLocation[] {
  const out: FileLocation[] = []
  const taken: Array<[number, number]> = []
  const overlaps = (s: number, e: number) => taken.some(([a, b]) => s < b && e > a)

  for (const m of text.matchAll(PY_RE)) {
    const start = m.index! + 6
    const path = m[1]!
    out.push({ path, line: Number(m[2]), start, end: start + path.length })
    taken.push([m.index!, m.index! + m[0].length])
  }
  for (const m of text.matchAll(PATH_RE)) {
    const whole = m[0]
    const start = m.index!
    const end = start + whole.length
    if (overlaps(start, end)) continue
    const path = m[1]!
    // Skip things that are clearly not paths: URLs, version numbers, ratios.
    const before = text.slice(Math.max(0, start - 8), start)
    if (URL_PREFIX.test(before + path) || /https?:\/\/\S*$/i.test(before)) continue
    if (/^[\d./]+$/.test(path)) continue
    if (path === '/' || path === './' || path === '../') continue
    const line = m[2] ?? m[4]
    const col = m[3] ?? m[5]
    out.push({
      path,
      line: line ? Number(line) : undefined,
      col: col ? Number(col) : undefined,
      start,
      end,
    })
    taken.push([start, end])
  }
  return out.sort((a, b) => a.start - b.start)
}

/** Extracts a "path:line" style location from a user-entered string. */
export function parseLocation(input: string): { path: string; line?: number } {
  const m = /^(.*?)(?::(\d+))?(?::\d+)?$/.exec(input.trim())
  if (!m) return { path: input }
  return { path: m[1]!, line: m[2] ? Number(m[2]) : undefined }
}
