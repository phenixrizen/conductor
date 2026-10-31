import { describe, expect, it } from 'vitest'
import { latestReads } from './latestReads'

/** A reply the test hands back when it chooses: its promise, and resolve and reject. */
function reply<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('latestReads', () => {
  it('keeps the last read started when an earlier one replies after it (the prerequisites read at two revisions)', async () => {
    const reads = latestReads()
    const applied: string[] = []
    const failed: unknown[] = []
    const older = reply<string>()
    const newer = reply<string>()
    const a = reads.read(() => older.promise, (v) => applied.push(v), (e) => failed.push(e))
    const b = reads.read(() => newer.promise, (v) => applied.push(v), (e) => failed.push(e))
    // The newer read (the receipt finished) replies first, the older (it was running) last.
    newer.resolve('finished')
    await b
    older.resolve('running')
    await a
    expect(applied).toEqual(['finished'])
    expect(failed).toEqual([])
  })
  it("drops an older read's error as it drops its value, and still says the last read's", async () => {
    const reads = latestReads()
    const applied: string[] = []
    const failed: string[] = []
    const older = reply<string>()
    const newer = reply<string>()
    const a = reads.read(() => older.promise, (v) => applied.push(v), (e) => failed.push((e as Error).message))
    const b = reads.read(() => newer.promise, (v) => applied.push(v), (e) => failed.push((e as Error).message))
    newer.resolve('finished')
    older.reject(new Error('older read failed'))
    await Promise.all([a, b])
    expect(applied).toEqual(['finished'])
    expect(failed).toEqual([])
    const last = reads.read(() => Promise.reject(new Error('the ledger is unavailable')), (v) => applied.push(v), (e) => failed.push((e as Error).message))
    await last
    expect(failed).toEqual(['the ledger is unavailable'])
  })
  it('applies a read in order when nothing overlaps, and none under way after drop', async () => {
    const reads = latestReads()
    const applied: string[] = []
    await reads.read(() => Promise.resolve('one'), (v) => applied.push(v), () => {})
    await reads.read(() => Promise.resolve('two'), (v) => applied.push(v), () => {})
    const late = reply<string>()
    const c = reads.read(() => late.promise, (v) => applied.push(v), () => {})
    reads.drop()
    late.resolve('three')
    await c
    expect(applied).toEqual(['one', 'two'])
  })
})
