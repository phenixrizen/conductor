import { describe, expect, it } from 'vitest'
import { crumbsOf, findNode, holdsMatch, looksLikePath, matches, resolveTyped, rootNode, setChildren, visibleRows } from './fileTree'

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
