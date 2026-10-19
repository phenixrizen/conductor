import { describe, expect, it } from 'vitest'
import { crumbsOf, findNode, foldCrumbs, holdsMatch, looksLikePath, matches, onPath, pathQuery, resolveTyped, rootNode, setChildren, visibleRows } from './fileTree'

const entries = (names: string[]) => names.map((n) => (n.endsWith('/') ? { name: n.slice(0, -1), dir: true, size: 0 } : { name: n, dir: false, size: n.length }))

describe('the Explorer tree', () => {
  it('lists a folder with folders first, then files, each alphabetical without regard to case', () => {
    const root = rootNode('/tmp/repo/')
    expect(root.path).toBe('/tmp/repo')
    expect(root.name).toBe('repo')
    setChildren(root, entries(['README.md', 'web/', 'go.mod', '.github/', '.git/', 'cmd/', 'Makefile']))
    expect(root.children.map((c) => c.name), 'the .git folder is left out').toEqual(['.github', 'cmd', 'web', 'go.mod', 'Makefile', 'README.md'])
    expect(root.children[0]!.path).toBe('/tmp/repo/.github')
    expect(root.children[0]!.depth).toBe(0)
    expect(root.loaded).toBe(true)
  })

  it('shows the rows of the root, then an expanded folder\'s after it, keeping a folder\'s state across a relisting', () => {
    const root = rootNode('/r')
    setChildren(root, entries(['internal/', 'go.mod']))
    const internal = root.children[0]!
    setChildren(internal, entries(['api/', 'store/']))
    internal.expanded = true
    expect(visibleRows(root).map((n) => `${n.depth}:${n.name}`)).toEqual(['0:internal', '1:api', '1:store', '0:go.mod'])
    setChildren(root, entries(['internal/', 'go.mod', 'go.sum']))
    expect(root.children[0]!.expanded).toBe(true)
    expect(root.children[0]!.children.map((c) => c.name)).toEqual(['api', 'store'])
    expect(findNode(root, '/r/internal/api')?.name).toBe('api')
    expect(findNode(root, '/r/internal/api/')?.name).toBe('api')
    expect(findNode(root, '/r/nope')).toBeNull()
  })

  it('filters the loaded tree by name, opening the folders that hold a match', () => {
    const root = rootNode('/r')
    setChildren(root, entries(['internal/', 'docs/', 'go.mod', 'README.md']))
    const internal = findNode(root, '/r/internal')!
    setChildren(internal, entries(['api/', 'store/']))
    const api = findNode(root, '/r/internal/api')!
    setChildren(api, entries(['users.go', 'users_test.go', 'router.go']))
    expect(matches('users.go', 'USER')).toBe(true)
    expect(matches('router.go', 'user')).toBe(false)
    expect(holdsMatch(internal, 'user')).toBe(true)
    expect(holdsMatch(findNode(root, '/r/docs')!, 'user')).toBe(false)
    expect(visibleRows(root, 'user').map((n) => n.name)).toEqual(['internal', 'api', 'users.go', 'users_test.go'])
    expect(visibleRows(root, '.go').map((n) => n.name)).toEqual(['internal', 'api', 'router.go', 'users.go', 'users_test.go'])
    expect(visibleRows(root, 'go.').map((n) => n.name)).toEqual(['go.mod'])
    expect(visibleRows(root, 'zzz')).toEqual([])
  })

  it('tells a path to open from a name to filter by, and resolves it against the working directory', () => {
    for (const p of ['internal/api/users.go', './README.md', '~/x', '/etc/hosts', 'users.go:14', 'users.go:14:9']) expect(looksLikePath(p), p).toBe(true)
    for (const n of ['users', 'README.md', 'api']) expect(looksLikePath(n), n).toBe(false)
    expect(resolveTyped('/r/', 'internal/api/users.go')).toBe('/r/internal/api/users.go')
    expect(resolveTyped('/r', './README.md')).toBe('/r/README.md')
    expect(resolveTyped('/r', '/etc/hosts')).toBe('/etc/hosts')
  })

  it('draws the breadcrumb from the top of the working directory', () => {
    expect(crumbsOf('/tmp/conductor-e2e/repo/')).toEqual([
      { label: 'tmp', path: '/tmp' },
      { label: 'conductor-e2e', path: '/tmp/conductor-e2e' },
      { label: 'repo', path: '/tmp/conductor-e2e/repo' },
    ])
    expect(crumbsOf('/')).toEqual([{ label: '/', path: '/' }])
  })
})

describe('the filter beyond the loaded tree', () => {
  it('lists what the find found and the tree does not draw', async () => {
    const { foundRows } = await import('./fileTree')
    const rows = foundRows('/r/repo/', ['internal/api/users.go', 'users.md', 'docs/users.md'], new Set(['/r/repo/users.md']))
    expect(rows).toEqual([
      { abs: '/r/repo/internal/api/users.go', dir: 'internal/api/', name: 'users.go' },
      { abs: '/r/repo/docs/users.md', dir: 'docs/', name: 'users.md' },
    ])
  })
})

describe('the Explorer filter as a path (#138)', () => {
  it('reads a filter with a slash as a path from the working directory', () => {
    expect(pathQuery('go/src')).toBe('go/src')
    expect(pathQuery(' ./Go/Src/ ')).toBe('go/src')
    expect(pathQuery('/internal/api')).toBe('internal/api')
    expect(pathQuery('users')).toBeNull()
    expect(onPath('go', 'go/src')).toBe(true)
    expect(onPath('go/src', 'go/src')).toBe(true)
    expect(onPath('go/src/github.com', 'go/src')).toBe(true)
    expect(onPath('docs', 'go/src')).toBe(false)
    expect(onPath('gopher', 'go/src')).toBe(false)
  })

  it('narrows the tree to the folder and what is below it, the folders on the way kept, nothing beside them', () => {
    const root = rootNode('/home/nater')
    setChildren(root, entries(['docs/', 'go/', 'README.md']))
    const go = root.children[1]!
    setChildren(go, entries(['bin/', 'src/']))
    const src = go.children[1]!
    setChildren(src, entries(['github.com/', 'notes.txt']))
    expect(visibleRows(root, 'go/src').map((n) => `${n.depth}:${n.name}`)).toEqual(['0:go', '1:src', '2:github.com', '2:notes.txt'])
    expect(visibleRows(root, 'go/').map((n) => n.name)).toEqual(['go', 'bin', 'src'])
    // A folder not loaded yet on the way shows as far as it is known.
    expect(visibleRows(root, 'docs/plans').map((n) => n.name)).toEqual(['docs'])
  })
})

describe('the breadcrumb folded from the left (#169)', () => {
  const crumbs = crumbsOf('/home/nater/go/src/github.com/phenixrizen/conductor')
  it('keeps the last crumbs whole and folds the rest into one', () => {
    const f = foldCrumbs(crumbs, 1)
    expect(f.shown.map((c) => c.label)).toEqual(['conductor'])
    expect(f.hidden.map((c) => c.label)).toEqual(['home', 'nater', 'go', 'src', 'github.com', 'phenixrizen'])
    expect(foldCrumbs(crumbs, 3).shown.map((c) => c.label)).toEqual(['github.com', 'phenixrizen', 'conductor'])
  })
  it('shows a short path whole, with one folder above the kept ones', () => {
    expect(foldCrumbs(crumbsOf('/srv/work'), 1)).toEqual({ hidden: [], shown: crumbsOf('/srv/work') })
    expect(foldCrumbs(crumbsOf('/a/b/c'), 2).hidden).toEqual([])
    expect(foldCrumbs(crumbsOf('/a/b/c/d'), 2).hidden.map((c) => c.label)).toEqual(['a', 'b'])
  })
})
