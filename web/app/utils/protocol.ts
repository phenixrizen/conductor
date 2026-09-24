// Mirror of internal/proto (see docs/protocol.md). Keep both in sync.

export const FrameType = {
  Output: 0x01,
  Input: 0x02,
  Control: 0x03,
  Scrollback: 0x04,
  Signal: 0x05,
  File: 0x06,
  Chunk: 0x07,
  Relay: 0x10,
} as const

export const ProtoVersion = 1

export const CloseCode = {
  Normal: 1000,
  GoingAway: 1001,
  ProtocolError: 4400,
  Unauthorized: 4401,
  Forbidden: 4403,
  NotFound: 4404,
  TooManyViewers: 4409,
  SessionEnded: 4410,
} as const

export type Role = 'view' | 'control'
export type TransportKind = 'ws' | 'webrtc' | 'relay'

export interface ICEServer {
  urls: string[]
  username?: string
  credential?: string
}

export interface Welcome {
  t: 'welcome'
  proto: number
  sessionId: string
  role: Role
  subscriberId?: string
  viewerId?: string
  cols: number
  rows: number
  status: string
  scrollbackBytes: number
  transport: TransportKind
  fileView: boolean
  iceServers?: ICEServer[]
  relayTimeoutMs?: number
  relayOnly?: boolean
}

export type ControlMessage =
  | Welcome
  | { t: 'ready' }
  | { t: 'resize'; cols: number; rows: number; by?: string }
  | { t: 'status'; status: string; exitCode?: number }
  | { t: 'viewers'; count: number }
  | { t: 'error'; code: string; message: string }
  | { t: 'pong'; ts: number }

export interface FileEntry {
  name: string
  dir: boolean
  size: number
}

export interface FileHeader {
  reqId: string
  path: string
  kind: 'file' | 'dir' | 'error'
  size?: number
  truncated?: boolean
  binary?: boolean
  mime?: string
  exists: boolean
  entries?: FileEntry[]
  error?: { code: string; message: string }
}

export interface FileResponse {
  header: FileHeader
  body: Uint8Array
}

const encoder = new TextEncoder()
const decoder = new TextDecoder()

export function encodeFrame(type: number, payload: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(1 + payload.length)
  out[0] = type
  out.set(payload, 1)
  return out
}

export function encodeJSONFrame(type: number, value: unknown): Uint8Array<ArrayBuffer> {
  return encodeFrame(type, encoder.encode(JSON.stringify(value)))
}

export function encodeControl(value: Record<string, unknown>): Uint8Array<ArrayBuffer> {
  return encodeJSONFrame(FrameType.Control, value)
}

export function encodeInput(data: Uint8Array): Uint8Array<ArrayBuffer> {
  return encodeFrame(FrameType.Input, data)
}

export function encodeText(text: string): Uint8Array {
  return encoder.encode(text)
}

export interface Frame {
  type: number
  payload: Uint8Array
}

export function decodeFrame(data: ArrayBuffer | Uint8Array): Frame | null {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data)
  if (bytes.length === 0) return null
  return { type: bytes[0]!, payload: bytes.subarray(1) }
}

export function parseJSON<T = Record<string, unknown>>(payload: Uint8Array): T | null {
  try {
    return JSON.parse(decoder.decode(payload)) as T
  } catch {
    return null
  }
}

export function decodeText(payload: Uint8Array): string {
  return decoder.decode(payload)
}

// FILE payload: [8-byte reqId][uint32 headerLen][JSON header][bytes]
export function decodeFile(payload: Uint8Array): FileResponse | null {
  if (payload.length < 12) return null
  const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength)
  const headerLen = view.getUint32(8)
  if (12 + headerLen > payload.length) return null
  const header = parseJSON<FileHeader>(payload.subarray(12, 12 + headerLen))
  if (!header) return null
  return { header, body: payload.subarray(12 + headerLen) }
}

// Reassembles Chunk frames ([uint16 msgId][uint32 total][uint32 offset][data])
// into the original frame bytes.
export class ChunkAssembler {
  private buffers = new Map<number, { data: Uint8Array; received: number }>()

  push(payload: Uint8Array): Uint8Array | null {
    if (payload.length < 10) return null
    const view = new DataView(payload.buffer, payload.byteOffset, payload.byteLength)
    const id = view.getUint16(0)
    const total = view.getUint32(2)
    const offset = view.getUint32(6)
    const data = payload.subarray(10)
    if (offset + data.length > total || total > 4 * 1024 * 1024) return null
    let entry = this.buffers.get(id)
    if (!entry) {
      entry = { data: new Uint8Array(total), received: 0 }
      this.buffers.set(id, entry)
    }
    entry.data.set(data, offset)
    entry.received += data.length
    if (entry.received >= total) {
      this.buffers.delete(id)
      return entry.data
    }
    return null
  }
}

export function closeReason(code: number, reason: string): string {
  switch (code) {
    case CloseCode.Normal:
      return 'Connection closed'
    case CloseCode.GoingAway:
      return 'Server is restarting'
    case CloseCode.Unauthorized:
      return 'Not authorized for this session'
    case CloseCode.Forbidden:
      return 'This share link was revoked'
    case CloseCode.NotFound:
      return 'Session not found'
    case CloseCode.TooManyViewers:
      return 'Too many viewers on this session'
    case CloseCode.SessionEnded:
      return reason || 'Session ended'
    case CloseCode.ProtocolError:
      return 'Protocol error'
    case 1006:
      return 'Connection lost'
  }
  return reason || `Connection closed (${code})`
}
