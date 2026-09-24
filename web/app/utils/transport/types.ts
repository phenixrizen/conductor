import type { Ref } from 'vue'
import type { ControlMessage, FileResponse, TransportKind, Welcome } from '../protocol'

export type TransportState = 'idle' | 'connecting' | 'signaling' | 'open' | 'closed'

export interface CloseInfo {
  code: number
  reason: string
  /** Last error control message received before the close, if any. */
  error?: { code: string; message: string }
}

export interface TerminalTransport {
  readonly kind: Ref<TransportKind>
  readonly state: Ref<TransportState>
  connect(hello: { cols: number; rows: number }): Promise<Welcome>
  sendInput(data: Uint8Array): void
  resize(cols: number, rows: number): void
  ping(): void
  requestFile(path: string, stat?: boolean): Promise<FileResponse>
  onOutput(cb: (data: Uint8Array, replay: boolean) => void): void
  onControl(cb: (msg: ControlMessage) => void): void
  onClose(cb: (info: CloseInfo) => void): void
  close(): void
}
