import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DispatchEvent } from './useDispatch'
import { DISPATCH_RETRY_MAX_MS, DispatchLive, asDispatchEvent, listWatch, projectsWatch, ticketWatch } from './useDispatchLive'

// The live events' rules (docs/plans/dispatch.md, "The SSE event"), on fake timers: what each page reads again, when, and how often.

const WINDOW = 250
let seq = 100

/** An event of a commit of its own (a seq after every one before), or of the commit `at` names. */
function ev(p: Partial<DispatchEvent> & { at?: number }): DispatchEvent {
  const { at, ...rest } = p
  return { seq: at ?? ++seq, entity: 'ticket', id: '', ticket: '', project: '', ...rest }
}

/** A page's re-read that counts itself, and fails its first `fails` times. */
function reads(fails = 0) {
  const r = { count: 0, fails, at: [] as number[] }
  const read = () => {
    r.count++
    r.at.push(Date.now())
    return r.fails-- > 0 ? Promise.reject(new Error('Cannot reach the server')) : Promise.resolve()
  }
  return { r, read }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(0)
})
afterEach(() => vi.useRealTimers())

describe('a ticket page', () => {
  it("is read again at once for an event naming its ticket, and not again after the window", async () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => 't1', () => 'CND', read))
    live.event(ev({ entity: 'ticket', id: 't1', ticket: 't1', project: 'CND' }))
    expect(r.count).toBe(1)
    await vi.advanceTimersByTimeAsync(10 * WINDOW)
    expect(r.count).toBe(1)
  })

  it("is read again once after the window for an event naming another ticket of its project, three such events once", async () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => 't1', () => 'CND', read))
    live.event(ev({ entity: 'ticket', id: 't2', ticket: 't2', project: 'CND' }))
    await vi.advanceTimersByTimeAsync(WINDOW - 1)
    expect(r.count).toBe(0)
    await vi.advanceTimersByTimeAsync(1)
    expect(r.count).toBe(1)
    for (const t of ['t2', 't3', 't4']) live.event(ev({ entity: 'ticket', id: t, ticket: t, project: 'CND' }))
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect(r.count).toBe(2)
    await vi.advanceTimersByTimeAsync(10 * WINDOW)
    expect(r.count).toBe(2)
  })

  it("is not read again for an event of another project, nor for a person's, which belongs to none", async () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => 't1', () => 'CND', read))
    live.event(ev({ entity: 'ticket', id: 't9', ticket: 't9', project: 'API' }))
    live.event(ev({ entity: 'person', id: '', ticket: '', project: '' }))
    await vi.advanceTimersByTimeAsync(10 * WINDOW)
    expect(r.count).toBe(0)
  })

  it("is read again at once for a comment's event, which names its ticket", () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => 't1', () => 'CND', read))
    live.event(ev({ entity: 'comment', id: 'c1', ticket: 't1', project: 'CND' }))
    expect(r.count).toBe(1)
  })

  it('names no ticket before it is read: its project is what it re-reads on', async () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => '', () => 'CND', read))
    live.event(ev({ entity: 'person', id: '', ticket: '', project: 'CND' }))
    expect(r.count).toBe(0)
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect(r.count).toBe(1)
  })
})

describe('a project event', () => {
  it('reads a list of that project and a ticket page of it again after the window, and nothing of another project', async () => {
    const live = new DispatchLive()
    const list = reads()
    const page = reads()
    const other = reads()
    live.subscribe(listWatch(() => 'CND', list.read))
    live.subscribe(ticketWatch(() => 't1', () => 'CND', page.read))
    live.subscribe(listWatch(() => 'API', other.read))
    live.event(ev({ entity: 'project', id: 'p1', ticket: '', project: 'CND' }))
    expect([list.r.count, page.r.count]).toEqual([0, 0])
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect([list.r.count, page.r.count, other.r.count]).toEqual([1, 1, 0])
  })
})

describe('the projects page', () => {
  it('is read again once after the window for any event at all', async () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(projectsWatch(read))
    live.event(ev({ entity: 'ticket', id: 't1', ticket: 't1', project: 'CND' }))
    live.event(ev({ entity: 'ticket', id: 't9', ticket: 't9', project: 'API' }))
    live.event(ev({ entity: 'person', id: '', ticket: '', project: '' }))
    expect(r.count).toBe(0)
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect(r.count).toBe(1)
    await vi.advanceTimersByTimeAsync(10 * WINDOW)
    expect(r.count).toBe(1)
  })
})

describe('a snapshot', () => {
  it('reads every page subscribed again at once', () => {
    const live = new DispatchLive()
    const pages = [reads(), reads(), reads()]
    live.subscribe(ticketWatch(() => 't1', () => 'CND', pages[0]!.read))
    live.subscribe(listWatch(() => 'CND', pages[1]!.read))
    live.subscribe(projectsWatch(pages[2]!.read))
    live.snapshot()
    expect(pages.map((p) => p.r.count)).toEqual([1, 1, 1])
  })

  it('starts the seqs over: an event after it is read whatever seq it has (a store restored from a backup)', () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => 't1', () => 'CND', read))
    live.event(ev({ at: 900, entity: 'ticket', id: 't1', ticket: 't1', project: 'CND' }))
    live.snapshot()
    expect(r.count).toBe(2)
    live.event(ev({ at: 5, entity: 'ticket', id: 't1', ticket: 't1', project: 'CND' }))
    expect(r.count).toBe(3)
  })
})

describe('a re-read that fails', () => {
  it('is asked again after the window, the wait doubled each time, and stops once one answers', async () => {
    const live = new DispatchLive()
    const { r, read } = reads(3)
    live.subscribe(listWatch(() => 'CND', read))
    live.event(ev({ project: 'CND', ticket: 't1', id: 't1' }))
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect(r.count).toBe(1)
    await vi.advanceTimersByTimeAsync(20_000)
    // Read at 250 (failed), then after 250, 500 and 1,000 more: the fourth answered, and nothing is asked after it.
    expect(r.at).toEqual([250, 500, 1000, 2000])
  })

  it('waits at most 8 s between two asks', async () => {
    const live = new DispatchLive()
    const { r, read } = reads(Number.POSITIVE_INFINITY)
    live.subscribe(ticketWatch(() => 't1', () => 'CND', read))
    live.event(ev({ entity: 'ticket', id: 't1', ticket: 't1', project: 'CND' }))
    await vi.advanceTimersByTimeAsync(40_000)
    const gaps = r.at.slice(1).map((t, i) => t - r.at[i]!)
    expect(gaps.slice(0, 7)).toEqual([250, 500, 1000, 2000, 4000, 8000, 8000])
    expect(Math.max(...gaps)).toBe(DISPATCH_RETRY_MAX_MS)
  })

  it('stops being asked once a snapshot reads it again, and the wait starts over', async () => {
    const live = new DispatchLive()
    const { r, read } = reads(2)
    live.subscribe(listWatch(() => 'CND', read))
    live.event(ev({ project: 'CND' }))
    await vi.advanceTimersByTimeAsync(WINDOW)
    await vi.advanceTimersByTimeAsync(WINDOW)
    // Two failed, the next ask is due in 500 ms: a snapshot reads it now, and it answers.
    expect(r.count).toBe(2)
    live.snapshot()
    expect(r.count).toBe(3)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(r.count).toBe(3)
    // A later failure waits the window again, not the doubled wait it had come to.
    r.fails = 1
    live.event(ev({ project: 'CND' }))
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect(r.count).toBe(4)
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect(r.count).toBe(5)
  })
})

// The owner's decision of 2026-10-30 (Owner decisions, 22): a commit's events arrive together, and a commit that changed more than 64
// entities of one kind in one project arrives as one event for them, its id and ticket empty, its project set. The pages key on the
// project: an empty ticket falls to the project's gathered re-read, and nothing reads the event's id.
describe("a commit's collapsed event", () => {
  it("falls to its project's gathered re-read on a ticket page and reads its lists and the projects page, never another project's", async () => {
    const live = new DispatchLive()
    const page = reads()
    const list = reads()
    const projects = reads()
    const other = reads()
    live.subscribe(ticketWatch(() => 't1', () => 'CND', page.read))
    live.subscribe(listWatch(() => 'CND', list.read))
    live.subscribe(projectsWatch(projects.read))
    live.subscribe(ticketWatch(() => 't9', () => 'API', other.read))
    live.event(ev({ entity: 'ticket', id: '', ticket: '', project: 'CND' }))
    // Not at once: an empty ticket names no ticket, not even one whose id the page has not read yet.
    expect(page.r.count).toBe(0)
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect([page.r.count, list.r.count, projects.r.count, other.r.count]).toEqual([1, 1, 1, 0])
  })

  it('is read by its data whatever its id holds, and an empty ticket never matches a page that has not read its own', () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => '', () => 'CND', read))
    live.event(ev({ entity: 'comment', id: '', ticket: '', project: 'CND' }))
    expect(r.count).toBe(0)
  })
})

describe("a commit's events", () => {
  it('arrive together: the read one of them caused stands for the rest', async () => {
    const live = new DispatchLive()
    const { r, read } = reads()
    live.subscribe(ticketWatch(() => 't1', () => 'CND', read))
    // A relation added between t1 and t2: one event for each ticket, of one commit.
    live.event(ev({ at: 500, entity: 'ticket', id: 't1', ticket: 't1', project: 'CND' }))
    live.event(ev({ at: 500, entity: 'ticket', id: 't2', ticket: 't2', project: 'CND' }))
    live.event(ev({ at: 500, entity: 'comment', id: 'c1', ticket: 't1', project: 'CND' }))
    expect(r.count).toBe(1)
    await vi.advanceTimersByTimeAsync(10 * WINDOW)
    expect(r.count).toBe(1)
    // The next commit is read again.
    live.event(ev({ at: 501, entity: 'ticket', id: 't1', ticket: 't1', project: 'CND' }))
    expect(r.count).toBe(2)
  })
})

describe('a page gone', () => {
  it('is read no more, its window and its retry dropped', async () => {
    const live = new DispatchLive()
    const { r, read } = reads(1)
    const stop = live.subscribe(listWatch(() => 'CND', read))
    live.event(ev({ project: 'CND' }))
    await vi.advanceTimersByTimeAsync(WINDOW)
    expect(r.count).toBe(1)
    stop()
    await vi.advanceTimersByTimeAsync(20_000)
    live.event(ev({ project: 'CND' }))
    live.snapshot()
    await vi.advanceTimersByTimeAsync(20_000)
    expect(r.count).toBe(1)
    expect(live.size).toBe(0)
  })
})

describe('the stream data', () => {
  it('reads a dispatch event, and nothing that is not one', () => {
    expect(asDispatchEvent({ seq: 7, entity: 'ticket', id: 'a', ticket: 'a', project: 'CND' })).toEqual({ seq: 7, entity: 'ticket', id: 'a', ticket: 'a', project: 'CND' })
    expect(asDispatchEvent({ seq: 7, entity: 'person', id: '', ticket: '', project: '' })).toEqual({ seq: 7, entity: 'person', id: '', ticket: '', project: '' })
    expect(asDispatchEvent({ seq: 8, entity: 'ticket', project: 'CND' })).toEqual({ seq: 8, entity: 'ticket', id: '', ticket: '', project: 'CND' })
    expect(asDispatchEvent(null)).toBeNull()
    expect(asDispatchEvent({ entity: 'ticket' })).toBeNull()
    expect(asDispatchEvent({ seq: '7', entity: 'ticket' })).toBeNull()
  })
})
