import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { encodeControl, FrameType, parseJSON, type FileHeader, type FileResponse, type Welcome } from '../protocol'
import { ReplyError } from '../reply'
import { ANSWER_TIMEOUT_MS, BaseTransport } from './base'

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

  it('a save whose part the owner could not read is refused at once, sends nothing, and the next save goes', async () => {
    const t = new TestTransport()
    // A path of 4026 bytes: with the hash and the rest, the part's header passes 4 KiB.
    const long = '/w/' + 'd'.repeat(4023)
    const refused = track(t.writeFile(long, new TextEncoder().encode('hi'), { baseSha256: 'a'.repeat(64) }))
    await vi.advanceTimersByTimeAsync(0)
    expect(refused.error?.message).toBe('the path is too long to save from the editor')
    expect(t.sent).toHaveLength(0)
    const next = track(t.writeFile('a.txt', new TextEncoder().encode('hi'), { baseSha256: 'a'.repeat(64) }))
    expect(t.sent).toHaveLength(1)
    t.receive(fileFrame({ reqId: t.reqIdOf(0), path: '/w/a.txt', kind: 'written', sha256: 'new' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(next.value?.header.kind).toBe('written')
  })

  it('a save whose header fits 4 KiB is sent whole', async () => {
    const t = new TestTransport()
    track(t.writeFile('/w/' + 'd'.repeat(3800), new TextEncoder().encode('hi'), { baseSha256: 'a'.repeat(64) }))
    expect(t.sent).toHaveLength(1)
  })

  it('a file request still gives up after 20 s', async () => {
    const t = new TestTransport()
    const read = track<FileResponse>(t.requestFile('a.txt'))
    await vi.advanceTimersByTimeAsync(20_000)
    expect(read.error?.message).toBe('file request timed out')
  })
})

describe('BaseTransport.answer', () => {
  beforeEach(() => {
    vi.stubGlobal('window', globalThis)
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  /** A transport welcomed by an owner that takes answers (`answer`), or an older one. */
  function welcomed(answer: boolean): TestTransport {
    const t = new TestTransport()
    t.receive(encodeControl({ t: 'welcome', proto: 1, sessionId: 's', role: 'control', cols: 80, rows: 24, status: 'running', scrollbackBytes: 0, transport: 'ws', fileView: true, ...(answer ? { answer: true } : {}) }))
    return t
  }
  function sentControl(t: TestTransport, n: number): Record<string, unknown> {
    const f = t.sent[n]!
    expect(f[0]).toBe(FrameType.Control)
    return parseJSON<Record<string, unknown>>(f.subarray(1))!
  }

  it('names the question to an owner that takes answers, and settles true on its answer_ok', async () => {
    const t = welcomed(true)
    const seen = vi.fn()
    t.onControl(seen)
    const sent = track(t.answer({ question: '0123456789abcdef', input: 'y' }))
    const msg = sentControl(t, 0)
    expect(msg).toMatchObject({ t: 'answer', question: '0123456789abcdef', input: 'y' })
    expect(msg.text).toBeUndefined()
    t.receive(encodeControl({ t: 'answer_ok', requestId: msg.reqId as string }))
    await vi.advanceTimersByTimeAsync(0)
    expect(sent.value).toBe(true)
    // The word was the answer's own: the page's handlers never see it.
    expect(seen).not.toHaveBeenCalled()
  })

  it('a question answered already is refused with question_answered, the page told nothing else', async () => {
    const t = welcomed(true)
    const seen = vi.fn()
    t.onControl(seen)
    const line = track(t.answer({ question: 'q1', text: 'Postgres' }))
    const msg = sentControl(t, 0)
    expect(msg).toMatchObject({ t: 'answer', question: 'q1', text: 'Postgres' })
    t.receive(encodeControl({ t: 'error', code: 'question_answered', message: 'this question was already answered: nothing was sent', requestId: msg.reqId as string }))
    await vi.advanceTimersByTimeAsync(0)
    expect(line.error).toBeInstanceOf(ReplyError)
    expect((line.error as ReplyError).code).toBe('question_answered')
    expect(seen).not.toHaveBeenCalled()
    // An error naming no answer of this transport goes to the page as before.
    t.receive(encodeControl({ t: 'error', code: 'not_sent', message: 'x', requestId: 'chat-nonce' }))
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it('to an older owner goes as keys or a submit and settles false at once', async () => {
    const old = welcomed(false)
    const keys = track(old.answer({ question: 'q1', input: 'y' }))
    const line = track(old.answer({ text: 'Postgres' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(keys.value).toBe(false)
    expect(line.value).toBe(false)
    expect(old.sent[0]![0]).toBe(FrameType.Input)
    expect(new TextDecoder().decode(old.sent[0]!.subarray(1))).toBe('y')
    expect(sentControl(old, 1)).toEqual({ t: 'submit', text: 'Postgres' })
  })

  it('to an owner that takes answers, a reply that names no question is not sent: unguarded keys could land on another', async () => {
    const t = welcomed(true)
    const plain = track(t.answer({ input: '1' }))
    const unnamed = track(t.answer({ question: 'not an id', text: 'Postgres' }))
    await vi.advanceTimersByTimeAsync(0)
    expect((plain.error as ReplyError).code).toBe('no_question')
    expect((unnamed.error as ReplyError).code).toBe('no_question')
    expect(t.sent).toHaveLength(0)
  })

  it('a connection that closes, or no word in time, ends the wait with a ReplyError; one not open refuses at once', async () => {
    const t = welcomed(true)
    const closed = track(t.answer({ question: 'q1', input: 'y' }))
    t.close()
    await vi.advanceTimersByTimeAsync(0)
    expect((closed.error as ReplyError).code).toBe('closed')
    const notOpen = track(t.answer({ question: 'q1', input: 'y' }))
    await vi.advanceTimersByTimeAsync(0)
    expect((notOpen.error as ReplyError).code).toBe('not_connected')
    const slow = welcomed(true)
    const late = track(slow.answer({ question: 'q1', input: 'y' }))
    await vi.advanceTimersByTimeAsync(ANSWER_TIMEOUT_MS)
    expect((late.error as ReplyError).code).toBe('no_word')
  })
})
