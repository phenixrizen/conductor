import { ref, type Ref } from 'vue'
import {
  ChunkAssembler,
  FrameType,
  ProtoVersion,
  decodeFile,
  decodeFrame,
  encodeControl,
  encodeFileWrite,
  encodeInput,
  MAX_WRITE_BYTES,
  parseJSON,
  writeParts,
  type ChatPost,
  type ChatSend,
  type ControlMessage,
  type FileResponse,
  type FileWriteOptions,
  type NvimSwapChoice,
  type TransportKind,
  type Welcome,
} from '../protocol'
import { rttFromPong } from './rtt'
import type { CloseInfo, TerminalTransport, TransportState } from './types'

const FILE_TIMEOUT_MS = 20000
/** How long an nvim_open may take: Neovim loading the person's config. */
const NVIM_OPEN_TIMEOUT_MS = 20_000

/**
 * Shared frame handling for both transports: listener registration, the
 * welcome handshake, file request correlation and chunk reassembly.
 * Subclasses provide the wire (WebSocket or data channel).
 */
export abstract class BaseTransport implements TerminalTransport {
  readonly kind: Ref<TransportKind>
  readonly state: Ref<TransportState> = ref('idle')
  readonly rtt: Ref<number | null> = ref(null)

  protected outputCbs: Array<(data: Uint8Array, replay: boolean) => void> = []
  protected controlCbs: Array<(msg: ControlMessage) => void> = []
  protected closeCbs: Array<(info: CloseInfo) => void> = []
  protected pendingFiles = new Map<string, { resolve: (r: FileResponse) => void; reject: (e: Error) => void; timer: number }>()
  /** nvim_open requests waiting for their `opened` or `error` event, by reqId (design round 12, F8). */
  protected nvimOpens = new Map<string, { resolve: (ev: NvimEvent) => void; reject: (e: Error) => void; timer: number }>()
  protected chunks = new ChunkAssembler()
  protected lastError?: { code: string; message: string }
  protected welcomeResolve?: (w: Welcome) => void
  protected welcomeReject?: (e: Error) => void
  protected closed = false
  private reqCounter = 0

  /** Display name sent in the hello; a label other viewers see, not authentication. */
  protected name = ''
  /** A quiet connection, for a run's chat alone (`hello.chatOnly`): no scrollback or output, not a viewer of the session. */
  protected chatOnly = false

  constructor(kind: TransportKind, opts: { name?: string; chatOnly?: boolean } = {}) {
    this.kind = ref(kind)
    this.name = (opts.name ?? '').trim().slice(0, 40)
    this.chatOnly = !!opts.chatOnly
  }

  abstract connect(hello: { cols: number; rows: number }): Promise<Welcome>
  protected abstract send(frame: Uint8Array<ArrayBuffer>): void
  abstract close(): void

  sendInput(data: Uint8Array): void {
    if (this.state.value !== 'open') return
    this.send(encodeInput(data))
  }

  submit(text: string): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ t: 'submit', text }))
  }

  chat(post: ChatPost): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ ...post, t: 'chat' }))
  }

  chatSend(send: ChatSend): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ ...send, t: 'chat_send' }))
  }

  resize(cols: number, rows: number, take?: boolean): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl(take ? { t: 'resize', cols, rows, take: true } : { t: 'resize', cols, rows }))
  }

  ping(): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ t: 'ping', ts: Date.now() }))
  }

  nvimOpen(path: string): Promise<NvimEvent> {
    if (this.state.value !== 'open') return Promise.reject(new Error('not connected'))
    const reqId = `n${(++this.reqCounter).toString(36)}`
    return new Promise((resolve, reject) => {
      const timer = window.setTimeout(() => {
        this.nvimOpens.delete(reqId)
        reject(new Error('neovim did not answer'))
      }, NVIM_OPEN_TIMEOUT_MS)
      this.nvimOpens.set(reqId, { resolve, reject, timer })
      this.send(encodeControl({ t: 'nvim_open', reqId, path }))
    })
  }
  nvimInput(id: string, keys: string, seq?: number): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl(seq ? { t: 'nvim_input', id, keys, seq } : { t: 'nvim_input', id, keys }))
  }
  nvimSwap(id: string, choice: NvimSwapChoice): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ t: 'nvim_swap', id, choice }))
  }
  nvimClose(id: string, discard?: boolean): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl(discard ? { t: 'nvim_close', id, discard: true } : { t: 'nvim_close', id }))
  }

  writeFile(path: string, data: Uint8Array, opts: FileWriteOptions = {}): Promise<FileResponse> {
    if (this.state.value !== 'open') return Promise.reject(new Error('not connected'))
    if (data.length > MAX_WRITE_BYTES) return Promise.reject(new Error('a file saved from the editor is at most 1 MiB'))
    const reqId = `w${(++this.reqCounter).toString(36)}`
    return new Promise((resolve, reject) => {
      const timer = window.setTimeout(() => {
        this.pendingFiles.delete(reqId)
        reject(new Error('the save did not answer'))
      }, FILE_TIMEOUT_MS)
      this.pendingFiles.set(reqId, { resolve, reject, timer })
      for (const [start, end] of writeParts(data.length)) {
        this.send(encodeFileWrite({ reqId, path, offset: start, total: data.length, baseSha256: opts.baseSha256, force: opts.force }, data.subarray(start, end)))
      }
    })
  }

  requestFile(path: string, stat = false, extra: FileGetExtra = {}): Promise<FileResponse> {
    if (this.state.value !== 'open') return Promise.reject(new Error('not connected'))
    const reqId = `f${(++this.reqCounter).toString(36)}`
    return new Promise((resolve, reject) => {
      const timer = window.setTimeout(() => {
        this.pendingFiles.delete(reqId)
        reject(new Error('file request timed out'))
      }, FILE_TIMEOUT_MS)
      this.pendingFiles.set(reqId, { resolve, reject, timer })
      this.send(encodeControl({ t: 'file_get', reqId, path, stat, ...extra }))
    })
  }

  onOutput(cb: (data: Uint8Array, replay: boolean) => void): void {
    this.outputCbs.push(cb)
  }

  onControl(cb: (msg: ControlMessage) => void): void {
    this.controlCbs.push(cb)
  }

  onClose(cb: (info: CloseInfo) => void): void {
    this.closeCbs.push(cb)
  }

  protected helloFrame(hello: { cols: number; rows: number }): Uint8Array<ArrayBuffer> {
    return encodeControl({ t: 'hello', proto: ProtoVersion, cols: hello.cols, rows: hello.rows, client: 'web/1', ...(this.name ? { name: this.name } : {}), ...(this.chatOnly ? { chatOnly: true } : {}) })
  }

  /** Dispatches a terminal-stream frame. Returns false for unknown types. */
  protected handleFrame(data: ArrayBuffer | Uint8Array): boolean {
    const frame = decodeFrame(data)
    if (!frame) return false
    switch (frame.type) {
      case FrameType.Output:
        for (const cb of this.outputCbs) cb(frame.payload, false)
        return true
      case FrameType.Scrollback:
        for (const cb of this.outputCbs) cb(frame.payload, true)
        return true
      case FrameType.Control: {
        const msg = parseJSON<ControlMessage>(frame.payload)
        if (!msg) return true
        this.handleControl(msg)
        return true
      }
      case FrameType.File: {
        const res = decodeFile(frame.payload)
        if (!res) return true
        const pending = this.pendingFiles.get(res.header.reqId)
        if (pending) {
          window.clearTimeout(pending.timer)
          this.pendingFiles.delete(res.header.reqId)
          pending.resolve({ header: res.header, body: res.body.slice() })
        }
        return true
      }
      case FrameType.Chunk: {
        const whole = this.chunks.push(frame.payload)
        if (whole) this.handleFrame(whole)
        return true
      }
    }
    return false
  }

  protected handleControl(msg: ControlMessage): void {
    if (msg.t === 'welcome') {
      this.state.value = 'open'
      this.welcomeResolve?.(msg)
      this.welcomeResolve = undefined
      this.welcomeReject = undefined
    } else if (msg.t === 'error') {
      this.lastError = { code: msg.code, message: msg.message }
    } else if (msg.t === 'pong') {
      const r = rttFromPong(msg.ts, Date.now())
      if (r !== null) this.rtt.value = r
    } else if (msg.t === 'nvim_event' && msg.reqId && (msg.kind === 'opened' || msg.kind === 'error')) {
      const waiting = this.nvimOpens.get(msg.reqId)
      if (waiting) {
        this.nvimOpens.delete(msg.reqId)
        window.clearTimeout(waiting.timer)
        if (msg.kind === 'opened') waiting.resolve(msg)
        else waiting.reject(Object.assign(new Error(msg.message || msg.code || 'refused'), { code: msg.code }))
      }
    }
    for (const cb of this.controlCbs) cb(msg)
  }

  protected emitClose(code: number, reason: string): void {
    if (this.closed) return
    this.closed = true
    this.state.value = 'closed'
    const info: CloseInfo = { code, reason, error: this.lastError }
    this.welcomeReject?.(new Error(this.lastError?.message || reason || `closed (${code})`))
    this.welcomeReject = undefined
    this.welcomeResolve = undefined
    for (const p of this.pendingFiles.values()) {
      window.clearTimeout(p.timer)
      p.reject(new Error('connection closed'))
    }
    this.pendingFiles.clear()
    for (const cb of this.closeCbs) cb(info)
  }
}
