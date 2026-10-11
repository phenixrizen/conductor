import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FrameType, parseJSON, type FileHeader, type FileResponse, type Welcome } from '../protocol'
import { BaseTransport } from './base'

/** A transport whose wire is a list: what it sends is kept, and a test hands it frames from the other end. */
class TestTransport extends BaseTransport {
  sent: Uint8Array[] = []
  constructor() {
    super('ws')
    this.state.value = 'open'
  }
  connect(): Promise<Welcome> {
    return Promise.reject(new Error('not used'))
  }
  protected send(frame: Uint8Array<ArrayBuffer>): void {
    this.sent.push(frame)
  }
  close(): void {
    this.emitClose(1000, 'closed')
  }
  receive(frame: Uint8Array): void {
    this.handleFrame(frame)
  }
  /** The reqId of the n-th frame sent, a FILE_WRITE part or a file_get. */
  reqIdOf(n: number): string {
    const f = this.sent[n]!
    if (f[0] === FrameType.FileWrite) {
      const len = new DataView(f.buffer, f.byteOffset, f.byteLength).getUint32(9)
      return parseJSON<{ reqId: string }>(f.subarray(13, 13 + len))!.reqId
    }
    return parseJSON<{ reqId: string }>(f.subarray(1))!.reqId
  }
}

/** A FILE frame answering reqId: [type][8-byte reqId][uint32 headerLen][header]. */
function fileFrame(header: Partial<FileHeader> & { reqId: string }): Uint8Array {
  const hb = new TextEncoder().encode(JSON.stringify(header))
  const out = new Uint8Array(1 + 8 + 4 + hb.length)
  out[0] = FrameType.File
  out.set(new TextEncoder().encode(header.reqId).subarray(0, 8), 1)
  new DataView(out.buffer).setUint32(9, hb.length)
  out.set(hb, 13)
  return out
}

/** Where a promise stands, without waiting for it. */
function track<T>(p: Promise<T>): { value?: T; error?: Error } {
  const out: { value?: T; error?: Error } = {}
  p.then(
    (v) => (out.value = v),
    (e: Error) => (out.error = e),
  )
  return out
}

describe('BaseTransport.writeFile', () => {
  beforeEach(() => {
    // The transport's timers are the page's (window.setTimeout); in node the global stands for it.
    vi.stubGlobal('window', globalThis)
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('does not give a save up after 20 s: it says it is slow, once, and takes the answer when it comes', async () => {
    const t = new TestTransport()
    const onSlow = vi.fn()
    const save = track(t.writeFile('a.txt', new TextEncoder().encode('hi'), { baseSha256: 'old', onSlow }))
    expect(t.sent).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(19_999)
    expect(onSlow).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(onSlow).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    expect(save).toEqual({})
    t.receive(fileFrame({ reqId: t.reqIdOf(0), path: '/w/a.txt', kind: 'written', sha256: 'new' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(save.value?.header).toMatchObject({ kind: 'written', sha256: 'new' })
    expect(onSlow).toHaveBeenCalledTimes(1)
  })

  it('a save answered in time is never called slow', async () => {
    const t = new TestTransport()
    const onSlow = vi.fn()
    const save = track(t.writeFile('a.txt', new TextEncoder().encode('hi'), { onSlow }))
    t.receive(fileFrame({ reqId: t.reqIdOf(0), path: '/w/a.txt', kind: 'error', error: { code: 'changed_on_disk', message: 'changed' } }))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(save.value?.header.error?.code).toBe('changed_on_disk')
    expect(onSlow).not.toHaveBeenCalled()
  })

  it('a connection that closes ends a save still waiting', async () => {
    const t = new TestTransport()
    const save = track(t.writeFile('a.txt', new TextEncoder().encode('hi')))
    await vi.advanceTimersByTimeAsync(30_000)
    t.close()
    await vi.advanceTimersByTimeAsync(0)
    expect(save.error?.message).toBe('connection closed')
  })

  it('a file request still gives up after 20 s', async () => {
    const t = new TestTransport()
    const read = track<FileResponse>(t.requestFile('a.txt'))
    await vi.advanceTimersByTimeAsync(20_000)
    expect(read.error?.message).toBe('file request timed out')
  })
})
