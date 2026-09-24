import { ref, type Ref } from 'vue'
import {
  ChunkAssembler,
  FrameType,
  ProtoVersion,
  decodeFile,
  decodeFrame,
  encodeControl,
  encodeInput,
  parseJSON,
  type ControlMessage,
  type FileResponse,
  type TransportKind,
  type Welcome,
} from '../protocol'
import type { CloseInfo, TerminalTransport, TransportState } from './types'

const FILE_TIMEOUT_MS = 20000

/**
 * Shared frame handling for both transports: listener registration, the
 * welcome handshake, file request correlation and chunk reassembly.
 * Subclasses provide the wire (WebSocket or data channel).
 */
export abstract class BaseTransport implements TerminalTransport {
  readonly kind: Ref<TransportKind>
  readonly state: Ref<TransportState> = ref('idle')

  protected outputCbs: Array<(data: Uint8Array, replay: boolean) => void> = []
  protected controlCbs: Array<(msg: ControlMessage) => void> = []
  protected closeCbs: Array<(info: CloseInfo) => void> = []
  protected pendingFiles = new Map<string, { resolve: (r: FileResponse) => void; reject: (e: Error) => void; timer: number }>()
  protected chunks = new ChunkAssembler()
  protected lastError?: { code: string; message: string }
  protected welcomeResolve?: (w: Welcome) => void
  protected welcomeReject?: (e: Error) => void
  protected closed = false
  private reqCounter = 0

  constructor(kind: TransportKind) {
    this.kind = ref(kind)
  }

  abstract connect(hello: { cols: number; rows: number }): Promise<Welcome>
  protected abstract send(frame: Uint8Array<ArrayBuffer>): void
  abstract close(): void

  sendInput(data: Uint8Array): void {
    if (this.state.value !== 'open') return
    this.send(encodeInput(data))
  }

  resize(cols: number, rows: number): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ t: 'resize', cols, rows }))
  }

  ping(): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ t: 'ping', ts: Date.now() }))
  }

  requestFile(path: string, stat = false): Promise<FileResponse> {
    if (this.state.value !== 'open') return Promise.reject(new Error('not connected'))
    const reqId = `f${(++this.reqCounter).toString(36)}`
    return new Promise((resolve, reject) => {
      const timer = window.setTimeout(() => {
        this.pendingFiles.delete(reqId)
        reject(new Error('file request timed out'))
      }, FILE_TIMEOUT_MS)
      this.pendingFiles.set(reqId, { resolve, reject, timer })
      this.send(encodeControl({ t: 'file_get', reqId, path, stat }))
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
    return encodeControl({ t: 'hello', proto: ProtoVersion, cols: hello.cols, rows: hello.rows, client: 'web/1' })
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
