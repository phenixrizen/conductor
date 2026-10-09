import type { Ref } from 'vue'
import type { ChatPost, ChatSend, ControlMessage, FileGetExtra, FileResponse, FileWriteOptions, NvimEvent, NvimSwapChoice, TransportKind, Welcome } from '../protocol'

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
  /** Posts to the session's chat (every role); only once the owner's welcome said `chat`. */
  chat(post: ChatPost): void
  /** Types a kept chat message into the agent (controllers only). */
  chatSend(send: ChatSend): void
  resize(cols: number, rows: number): void
  ping(): void
  requestFile(path: string, stat?: boolean, extra?: FileGetExtra): Promise<FileResponse>
  /** Saves a file in parts (F6); the FILE reply (kind `written` or `error`) settles it. */
  writeFile(path: string, data: Uint8Array, opts?: FileWriteOptions): Promise<FileResponse>
  /** The editor's Neovim (F8): open a file on the session's machine (the `opened` event, or the `error` one, settles it), send keys, close. */
  nvimOpen(path: string): Promise<NvimEvent>
  nvimInput(id: string, keys: string, seq?: number): void
  nvimClose(id: string): void
  nvimSwap(id: string, choice: NvimSwapChoice): void
  onOutput(cb: (data: Uint8Array, replay: boolean) => void): void
  onControl(cb: (msg: ControlMessage) => void): void
  onClose(cb: (info: CloseInfo) => void): void
  close(): void
}
