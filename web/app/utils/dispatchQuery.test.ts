import { describe, expect, it } from 'vitest'
import { apiQuery, emptyFilter, filterFromURL, filterFromView, filterNarrows, filterToURL, filterToView, MAX_SEARCH, milestoneChoices, type DispatchFilter } from './dispatchQuery'

const names = {
  labels: [
    { id: 'aaaaaaaaaaaaaaa1', name: 'api' },
    { id: 'aaaaaaaaaaaaaaa2', name: 'ui' },
    // Two live labels of one name, as a merge may leave: the name stands for both.
    { id: 'aaaaaaaaaaaaaaa3', name: 'ui' },
  ],
  milestones: [{ id: 'bbbbbbbbbbbbbbb1', name: 'v1.0' }],
}

/** A filter with the fields given, the rest a list nothing narrows. */
function filterOf(p: Partial<DispatchFilter>): DispatchFilter {
  return { ...emptyFilter(), ...p }
}

describe("the filter in a page's link", () => {
  it('reads every field from the link, by its words', () => {
    const f = filterFromURL({
      status: ['in_progress', 'in_review'],
      priority: ['high', 'urgent'],
      label: 'api',
      owner: 'Alice',
      assignee: ['person:Bob', 'agent:claude'],
      milestone: 'v1.0',
      horizon: ['now', 'none'],
      parent: 'CND-3',
      blocked: '1',
      q: 'login bug',
      deleted: '1',
      sort: 'priority',
      desc: '0',
      group: 'status',
    })
    expect(f).toEqual({
      status: ['in_progress', 'in_review'],
      priority: [3, 4],
      labels: ['api'],
      owner: 'Alice',
      assignees: ['person:Bob', 'agent:claude'],
      milestone: 'v1.0',
      horizon: ['now', ''],
      parent: 'CND-3',
      blocked: true,
      q: 'login bug',
      deleted: true,
      sort: 'priority',
      desc: false,
      group: 'status',
    })
  })
  it('writes only what differs from a list nothing narrows, and reads back what it wrote', () => {
    expect(filterToURL(emptyFilter())).toEqual({})
    const f = filterOf({ status: ['in_progress'], labels: ['api'], q: 'login', priority: [4, 0], horizon: [''], sort: 'title', desc: true })
    const url = filterToURL(f)
    expect(url).toEqual({ status: 'in_progress', priority: ['urgent', 'none'], label: 'api', horizon: 'none', q: 'login', sort: 'title', desc: '1' })
    expect(filterFromURL(url)).toEqual(f)
    // A sort read in its own direction says no direction.
    expect(filterToURL(filterOf({ sort: 'priority', desc: true }))).toEqual({ sort: 'priority' })
    expect(filterToURL(filterOf({ desc: false }))).toEqual({ desc: '0' })
  })
  it('takes a list in its own direction when the link says none: the latest updated first', () => {
    expect(filterFromURL({})).toEqual(emptyFilter())
    expect(emptyFilter()).toMatchObject({ sort: 'updated', desc: true })
    expect(filterFromURL({ sort: 'title' })).toMatchObject({ sort: 'title', desc: false })
  })
  it('ignores a parameter it does not know, and a value outside its vocabulary', () => {
    const f = filterFromURL({ view: 'board', foo: '1', status: ['ready', 'bogus', 'ready'], priority: ['higher', '2'], horizon: 'someday', assignee: ['nobody', 'agent:Not Valid'], sort: 'rank', parent: 'not-a-key', group: 'colour', blocked: 'yes', label: '' })
    expect(f).toEqual(filterOf({ status: ['ready'], priority: [2] }))
    expect(filterToURL(f)).toEqual({ status: 'ready', priority: 'medium' })
  })
  it("cuts a search to the store's bound, by characters", () => {
    expect(filterFromURL({ q: 'é'.repeat(MAX_SEARCH + 5) }).q).toBe('é'.repeat(MAX_SEARCH))
  })
  it('says whether a filter narrows the list: a sort or a grouping does not', () => {
    expect(filterNarrows(emptyFilter())).toBe(false)
    expect(filterNarrows(filterOf({ sort: 'key', desc: false, group: 'status' }))).toBe(false)
    expect(filterNarrows(filterOf({ q: '   ' }))).toBe(false)
    expect(filterNarrows(filterOf({ blocked: true }))).toBe(true)
    expect(filterNarrows(filterOf({ labels: ['api'] }))).toBe(true)
  })
})

describe("a saved view's query", () => {
  it('keeps labels and the milestone by id, and reads them back by name', () => {
    const f = filterOf({ status: ['ready'], labels: ['api', 'ui'], milestone: 'v1.0', owner: 'Alice', horizon: ['now'], q: 'login', sort: 'priority', desc: true, group: 'owner' })
    const q = filterToView(f, names)
    expect(q).toEqual({ status: ['ready'], labels: ['aaaaaaaaaaaaaaa1', 'aaaaaaaaaaaaaaa2', 'aaaaaaaaaaaaaaa3'], owner: 'Alice', milestone: 'bbbbbbbbbbbbbbb1', horizon: ['now'], q: 'login', sort: 'priority', desc: true, group: 'owner' })
    // The two labels named ui read back as the one name.
    expect(filterFromView(q!, names)).toEqual(f)
  })
  it("reads a view's query and writes it back as it was", () => {
    const view = { status: ['in_review'], priority: [3], labels: ['aaaaaaaaaaaaaaa1'], assignees: ['person:Bob'], milestone: 'bbbbbbbbbbbbbbb1', horizon: [''], blocked: true, sort: 'updated' }
    const f = filterFromView(view, names)
    expect(f).toMatchObject({ labels: ['api'], milestone: 'v1.0', horizon: [''], blocked: true, sort: 'updated', desc: false })
    expect(filterToView(f, names)).toEqual(view)
  })
  it('keeps an id whose row has gone as the id, which matches nothing, both ways', () => {
    const f = filterFromView({ labels: ['ccccccccccccccc9'], sort: 'updated' }, names)
    expect(f.labels).toEqual(['ccccccccccccccc9'])
    expect(filterToView(f, names)).toEqual({ labels: ['ccccccccccccccc9'], sort: 'updated' })
  })
  it('cannot save a filter of labels or a milestone the project lacks, and keeps a known label beside an unknown one', () => {
    expect(filterToView(filterOf({ labels: ['nosuch'] }), names)).toBeNull()
    expect(filterToView(filterOf({ milestone: 'v9' }), names)).toBeNull()
    expect(filterToView(filterOf({ labels: ['api', 'nosuch'] }), names)?.labels).toEqual(['aaaaaaaaaaaaaaa1'])
  })
  it('ignores what is not a view query of the store', () => {
    expect(filterFromView({ status: ['bogus'], priority: [7], horizon: ['someday'], sort: 'rank', group: 'colour', assignees: ['x'] }, names)).toEqual(filterOf({ desc: false }))
  })
})

describe('milestones of one name', () => {
  // The store lets a project's milestones share a name (internal/dispatch/milestones.go): each keeps its identity, by its id.
  const repeated = {
    labels: names.labels,
    milestones: [
      { id: 'bbbbbbbbbbbbbbb1', name: 'v1.0', target: '' },
      { id: 'bbbbbbbbbbbbbbb2', name: '1.0', target: '2026-12-01' },
      { id: 'bbbbbbbbbbbbbbb3', name: '1.0', target: '' },
    ],
  }
  const asked = (f: DispatchFilter) => new URLSearchParams(apiQuery('CND', f, repeated)!).get('milestone')

  it('are offered each apart, by id, told apart by their place and target; a name no other has is offered by its name', () => {
    expect(milestoneChoices(repeated.milestones)).toEqual([
      { label: 'v1.0', value: 'v1.0' },
      { label: '1.0 (1 of 2, due 2026-12-01)', value: 'bbbbbbbbbbbbbbb2' },
      { label: '1.0 (2 of 2)', value: 'bbbbbbbbbbbbbbb3' },
    ])
  })

  it('keep their identity through a saved view, the link and the request', () => {
    // A view of the second of the name: by its id in the filter and the link, and asked for by it, never by the first's.
    const f = filterFromView({ milestone: 'bbbbbbbbbbbbbbb3', sort: 'updated' }, repeated)
    expect(f.milestone).toBe('bbbbbbbbbbbbbbb3')
    expect(filterToURL(f).milestone).toBe('bbbbbbbbbbbbbbb3')
    expect(asked(filterFromURL(filterToURL(f)))).toBe('bbbbbbbbbbbbbbb3')
    expect(filterToView(filterFromURL(filterToURL(f)), repeated)).toEqual({ milestone: 'bbbbbbbbbbbbbbb3', sort: 'updated' })
    // Each one chosen from the menu is asked for by its own id.
    expect(milestoneChoices(repeated.milestones).map((c) => asked(filterOf({ milestone: c.value })))).toEqual(['bbbbbbbbbbbbbbb1', 'bbbbbbbbbbbbbbb2', 'bbbbbbbbbbbbbbb3'])
    // One whose name no other has reads by its name, both ways.
    expect(filterFromView({ milestone: 'bbbbbbbbbbbbbbb1', sort: 'updated' }, repeated).milestone).toBe('v1.0')
  })

  it("take a link that names a repeated name, written before it repeated, as the first of the project's order", () => {
    expect(asked(filterOf({ milestone: '1.0' }))).toBe('bbbbbbbbbbbbbbb2')
  })
})

describe("the list's request", () => {
  it('says the filter as the API takes it: ids, numbers, the page', () => {
    const f = filterOf({ status: ['ready', 'in_progress'], priority: [4], labels: ['ui'], owner: 'Alice Smith', assignees: ['person:Bob'], milestone: 'v1.0', horizon: ['', 'now'], blocked: true, q: 'a;b c', sort: 'priority', desc: true })
    const q = new URLSearchParams(apiQuery('CND', f, names, { limit: 200, cursor: 'xyz' })!)
    expect(q.get('project')).toBe('CND')
    expect(q.getAll('status')).toEqual(['ready', 'in_progress'])
    expect(q.getAll('priority')).toEqual(['4'])
    expect(q.getAll('label')).toEqual(['aaaaaaaaaaaaaaa2', 'aaaaaaaaaaaaaaa3'])
    expect(q.get('owner')).toBe('Alice Smith')
    expect(q.getAll('assignee')).toEqual(['person:Bob'])
    expect(q.get('milestone')).toBe('bbbbbbbbbbbbbbb1')
    expect(q.getAll('horizon')).toEqual(['', 'now'])
    expect(q.get('blocked')).toBe('1')
    expect(q.get('q')).toBe('a;b c')
    expect(q.get('sort')).toBe('priority')
    expect(q.get('desc')).toBe('1')
    expect(q.get('limit')).toBe('200')
    expect(q.get('cursor')).toBe('xyz')
    // The ';' is sent escaped, which the server reads as part of the value.
    expect(apiQuery('CND', f, names)).toContain('q=a%3Bb+c')
  })
  it("leaves out what the list does not narrow by, the direction among it when it is the store's own", () => {
    expect(apiQuery('CND', filterOf({ sort: 'title', desc: false }), names)).toBe('project=CND&sort=title')
    expect(apiQuery('CND', filterOf({ q: '  ' }), names)).toBe('project=CND&sort=updated&desc=1')
  })
  it('asks nothing when the filter can match no ticket of the project', () => {
    expect(apiQuery('CND', filterOf({ labels: ['nosuch'] }), names)).toBeNull()
    expect(apiQuery('CND', filterOf({ milestone: 'v9' }), names)).toBeNull()
    expect(apiQuery('CND', filterOf({ labels: ['nosuch', 'api'] }), names)).toContain('label=aaaaaaaaaaaaaaa1')
  })
})
