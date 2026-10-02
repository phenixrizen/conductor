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
  /** Last measured ping/pong round trip in ms; null until the first pong. */
  readonly rtt: Ref<number | null>
  /** `hello` 0 × 0 (FOLLOW_SIZE) follows the session's size; a controller's other size sets it. */
  connect(hello: { cols: number; rows: number }): Promise<Welcome>
  sendInput(data: Uint8Array): void
  /**
   * Submits a line as a reply box does: the owner types it (as a paste when the program asks for one) and presses
   * Enter 250 ms later, so that a TUI takes the Enter as Enter. Controllers only; at most MAX_SUBMIT bytes.
   */
  submit(text: string): void
  resize(cols: number, rows: number): void
  ping(): void
  requestFile(path: string, stat?: boolean): Promise<FileResponse>
  onOutput(cb: (data: Uint8Array, replay: boolean) => void): void
  onControl(cb: (msg: ControlMessage) => void): void
  onClose(cb: (info: CloseInfo) => void): void
  close(): void
}
