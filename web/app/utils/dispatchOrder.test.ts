import { describe, expect, it } from 'vitest'
import { DispatchOrder, serially, through } from './dispatchOrder'

/** A request whose reply the test hands back, or fails, when it wants. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: Error) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

interface Shown {
  key: string
  rev: number
  title: string
}

/** A page's state for one ticket, kept through the gate as a page keeps it. */
function page() {
  const gate = new DispatchOrder()
  const shown: { ticket: Shown | null } = { ticket: null }
  const thing = 'ticket:CND-12'
  /** A request sent now; its reply applied as the gate says. */
  function send(reply: Promise<Shown>): Promise<boolean> {
    return through(gate, thing, () => reply, (t) => (shown.ticket = t), (t) => t.rev)
  }
  return { gate, shown, send, thing }
}

describe('the ordering gate', () => {
  it("leaves the later read's reply when two reads of one ticket are answered in reverse order", async () => {
    const p = page()
    const first = deferred<Shown>()
    const second = deferred<Shown>()
    const a = p.send(first.promise)
    const b = p.send(second.promise)
    second.resolve({ key: 'CND-12', rev: 5, title: 'later' })
    expect(await b).toBe(true)
    first.resolve({ key: 'CND-12', rev: 5, title: 'earlier' })
    expect(await a).toBe(false)
    expect(p.shown.ticket?.title).toBe('later')
  })

  it("drops an operation's reply that arrives after a later read's was applied", async () => {
    const p = page()
    const edit = deferred<Shown>()
    const reread = deferred<Shown>()
    const op = p.send(edit.promise)
    const read = p.send(reread.promise)
    reread.resolve({ key: 'CND-12', rev: 6, title: 'after the edit and the next one' })
    expect(await read).toBe(true)
    edit.resolve({ key: 'CND-12', rev: 5, title: 'the edit' })
    expect(await op).toBe(false)
    expect(p.shown.ticket).toEqual({ key: 'CND-12', rev: 6, title: 'after the edit and the next one' })
  })

  it("leaves an earlier success applied when a later request fails: the edit's reply lands after the failed re-read, and the rev moves", async () => {
    const p = page()
    p.shown.ticket = { key: 'CND-12', rev: 4, title: 'before' }
    expect(await p.send(Promise.resolve({ key: 'CND-12', rev: 4, title: 'before' }))).toBe(true)
    // An edit is sent; its commit's event asks a re-read, which fails; the edit's reply arrives last.
    const edit = deferred<Shown>()
    const reread = deferred<Shown>()
    const op = p.send(edit.promise)
    const read = p.send(reread.promise)
    reread.reject(new Error('Cannot reach the server'))
    await expect(read).rejects.toThrow('Cannot reach the server')
    expect(p.gate.rev(p.thing)).toBe(4)
    edit.resolve({ key: 'CND-12', rev: 5, title: 'edited' })
    expect(await op).toBe(true)
    expect(p.shown.ticket).toEqual({ key: 'CND-12', rev: 5, title: 'edited' })
    // The rev shown moved: the next operation expects 5.
    expect(p.gate.rev(p.thing)).toBe(5)
  })

  it('drops a reply whose rev is below the one shown, whatever its number', async () => {
    const p = page()
    expect(await p.send(Promise.resolve({ key: 'CND-12', rev: 7, title: 'seven' }))).toBe(true)
    // A read sent later, served before a commit another browser made, answers an older rev.
    expect(await p.send(Promise.resolve({ key: 'CND-12', rev: 6, title: 'six' }))).toBe(false)
    expect(p.shown.ticket?.title).toBe('seven')
    // The same rev again is the same state, and taken.
    expect(await p.send(Promise.resolve({ key: 'CND-12', rev: 7, title: 'seven again' }))).toBe(true)
    // By hand: a later number with a lower rev is dropped; a higher rev with an earlier number too.
    const g = new DispatchOrder()
    const n1 = g.ask()
    const n2 = g.ask()
    expect(g.take('ticket:A-1', n2, 3)).toBe(true)
    expect(g.take('ticket:A-1', n1, 9)).toBe(false)
    expect(g.take('ticket:A-1', g.ask(), 2)).toBe(false)
    expect(g.rev('ticket:A-1')).toBe(3)
  })

  it("drops the old filter's late page once the filter has changed", async () => {
    const gate = new DispatchOrder()
    let shown = ''
    const oldPage = deferred<string>()
    const old = through(gate, 'list:CND?status=ready', () => oldPage.promise, (v) => (shown = v))
    // The filter changes: a new thing; the old one is dropped.
    gate.drop('list:CND?status=ready')
    expect(await through(gate, 'list:CND?status=done', () => Promise.resolve('done tickets'), (v) => (shown = v))).toBe(true)
    oldPage.resolve('ready tickets')
    expect(await old).toBe(false)
    expect(shown).toBe('done tickets')
    // The old filter asked for again starts afresh.
    expect(await through(gate, 'list:CND?status=ready', () => Promise.resolve('ready again'), (v) => (shown = v))).toBe(true)
    expect(shown).toBe('ready again')
  })

  it("does not drop another thing's reply", async () => {
    const gate = new DispatchOrder()
    const shown: Record<string, string> = {}
    const project = deferred<string>()
    const p = through(gate, 'project:CND', () => project.promise, (v) => (shown.project = v), () => 2)
    expect(await through(gate, 'ticket:CND-12', () => Promise.resolve('ticket'), (v) => (shown.ticket = v), () => 9)).toBe(true)
    expect(await through(gate, 'list:CND?', () => Promise.resolve('list'), (v) => (shown.list = v))).toBe(true)
    project.resolve('project')
    expect(await p).toBe(true)
    expect(shown).toEqual({ project: 'project', ticket: 'ticket', list: 'list' })
  })

  it('takes a reply once: the same number twice is no later reply', () => {
    const g = new DispatchOrder()
    const n = g.ask()
    expect(g.take('projects', n)).toBe(true)
    expect(g.take('projects', n)).toBe(false)
  })
})

describe("a page's operations, one after another", () => {
  /** Every promise callback queued so far run: what a call can start on has started. */
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0))
  it('start each call once the one before has settled, a failed one among them, each answering as its own', async () => {
    const started: string[] = []
    const gates = new Map<string, ReturnType<typeof deferred<string>>>()
    const op = serially(async (name: string) => {
      started.push(name)
      const d = deferred<string>()
      gates.set(name, d)
      return d.promise
    })
    const a = op('a')
    const b = op('b')
    const c = op('c')
    await settle()
    expect(started).toEqual(['a'])
    gates.get('a')!.resolve('a done')
    expect(await a).toBe('a done')
    await settle()
    expect(started).toEqual(['a', 'b'])
    gates.get('b')!.reject(new Error('b refused'))
    await expect(b).rejects.toThrow('b refused')
    await settle()
    expect(started).toEqual(['a', 'b', 'c'])
    gates.get('c')!.resolve('c done')
    expect(await c).toBe('c done')
  })
})
