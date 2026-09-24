import type { Welcome } from '../protocol'
import { BaseTransport } from './base'

const HELLO_TIMEOUT_MS = 10000

/** Server-hosted sessions: frames flow directly over the WebSocket. */
export class WebSocketTransport extends BaseTransport {
  private ws?: WebSocket

  constructor(private readonly url: string) {
    super('ws')
  }

  connect(hello: { cols: number; rows: number }): Promise<Welcome> {
    this.state.value = 'connecting'
    return new Promise<Welcome>((resolve, reject) => {
      this.welcomeResolve = resolve
      this.welcomeReject = reject
      const timer = window.setTimeout(() => {
        if (this.state.value !== 'open') {
          this.ws?.close()
          this.emitClose(4400, 'no welcome from server')
        }
      }, HELLO_TIMEOUT_MS)
      let ws: WebSocket
      try {
        ws = new WebSocket(this.url)
      } catch (e) {
        window.clearTimeout(timer)
        this.emitClose(1006, (e as Error).message)
        return
      }
      ws.binaryType = 'arraybuffer'
      this.ws = ws
      ws.onopen = () => ws.send(this.helloFrame(hello))
      ws.onmessage = (ev) => {
        if (ev.data instanceof ArrayBuffer) this.handleFrame(ev.data)
      }
      ws.onclose = (ev) => {
        window.clearTimeout(timer)
        this.emitClose(ev.code, ev.reason)
      }
      ws.onerror = () => {
        /* onclose follows with the code */
      }
    })
  }

  protected send(frame: Uint8Array<ArrayBuffer>): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(frame)
  }

  close(): void {
    this.ws?.close(1000, 'bye')
    this.emitClose(1000, 'bye')
  }
}
