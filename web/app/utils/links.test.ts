import { describe, expect, it } from 'vitest'
import { findFileLocations, parseLocation } from './links'

describe('findFileLocations', () => {
  it('finds path:line:col', () => {
    const [loc] = findFileLocations('error at internal/api/server.go:42:7 in handler')
    expect(loc).toMatchObject({ path: 'internal/api/server.go', line: 42, col: 7 })
  })

  it('finds (line,col) style', () => {
    const [loc] = findFileLocations('src/app.ts(12,3): error TS2304')
    expect(loc).toMatchObject({ path: 'src/app.ts', line: 12, col: 3 })
  })

  it('finds python traceback lines', () => {
    const [loc] = findFileLocations('  File "pkg/mod.py", line 88, in run')
    expect(loc).toMatchObject({ path: 'pkg/mod.py', line: 88 })
  })

  it('finds absolute, home and dotted paths', () => {
    const locs = findFileLocations('see /etc/hosts and ~/notes.md or ./README.md and ../lib/x.go')
    expect(locs.map((l) => l.path)).toEqual(['/etc/hosts', '~/notes.md', './README.md', '../lib/x.go'])
  })

  it('finds bare file names with known extensions', () => {
    const locs = findFileLocations('edited package.json and Makefile.go, not foo.unknownext')
    expect(locs.map((l) => l.path)).toEqual(['package.json', 'Makefile.go'])
  })

  it('ignores urls, version numbers and prose slashes', () => {
    expect(findFileLocations('open https://example.com/a/b.go:1 now')).toEqual([])
    expect(findFileLocations('node v20.11.1 and 3/4 done')).toEqual([])
    expect(findFileLocations('either/or')).toHaveLength(1)
  })

  it('reports columns for the whole token including the line suffix', () => {
    const text = 'x main.go:10 y'
    const [loc] = findFileLocations(text)
    expect(text.slice(loc!.start, loc!.end)).toBe('main.go:10')
  })
})

describe('parseLocation', () => {
  it('splits path and line', () => {
    expect(parseLocation('a/b.go:12:4')).toEqual({ path: 'a/b.go', line: 12 })
    expect(parseLocation('a/b.go')).toEqual({ path: 'a/b.go', line: undefined })
  })
})
