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

/**
 * A hello of 0 × 0 follows the session's size (proto.HelloSize): a scaled tile or a quick reply never resizes a
 * session. Any other size a controller sends sets it, latest controller wins (docs/protocol.md, Resize policy).
 */
export const FOLLOW_SIZE = { cols: 0, rows: 0 } as const

/** The longest line a `submit` control message carries, in bytes (proto.MaxSubmit). */
export const MAX_SUBMIT = 4096

/** A chat message's text, in bytes after cleaning (proto.MaxChatText); the composer counts from CHAT_COUNTER_FROM. */
export const MAX_CHAT_TEXT = 2048
export const CHAT_COUNTER_FROM = 1536
/** One connection may post this many a second, this many at once (proto.ChatRatePerSecond, ChatBurst). */
export const CHAT_RATE_PER_SECOND = 10
export const CHAT_BURST = 20
/** The `to` of a post that is also typed into this session's agent. */
export const CHAT_TO_AGENT = 'agent'

export type ChatScope = 'session' | 'run'
export type ChatKind = 'message' | 'system' | 'sent_to_agent' | 'question'
/** Who a chat message is from: the viewer's subscription, as the roster names it. */
export interface ChatBy {
  id: string
  name: string
  /** `agent` on a question: the agent's, no viewer's. */
  role: Role | 'agent'
}
/** One message of a chat (`chat`, owner → client): a message, a join or leave line about `by`, or a marker that `by` typed `ref` into the agent (`to` names a run's member). */
export interface ChatMessage {
  t: 'chat'
  id: string
  at: string
  scope: ChatScope
  kind: ChatKind
  by: ChatBy
  text?: string
  ref?: string
  to?: string
  on?: string
  /** `answered` names, in `ref`, the question `by` answered. */
  event?: 'join' | 'leave' | 'answered'
  /** The sender's own id of the post, echoed. */
  nonce?: string
  /** A question's choices (at most 6), typed as the attention's are. */
  options?: AttentionOption[]
}
/** A viewer's post (`chat`, client → owner). */
export interface ChatPost {
  t: 'chat'
  nonce?: string
  scope?: ChatScope
  text: string
  on?: string
  to?: string
}
/** Types a kept message into the agent (`chat_send`, client → owner, controllers only). */
export interface ChatSend {
  t: 'chat_send'
  ref: string
  scope?: ChatScope
  to?: string
}
/** The kept chat replayed to a new viewer, oldest first; `more` says another frame follows. */
export interface ChatHistory {
  t: 'chat_history'
  scope: ChatScope
  messages: ChatMessage[]
  more?: boolean
}
/** One person on a run's chat: a viewer of any member, one row per name; `on` is the member they look at, when one. */
export interface ChatPerson {
  id: string
  name: string
  role: Role
  on?: string
}
/** Who is on a run's chat (`chat_roster`, owner → client), sent to every member's viewers as any member's roster changes; `count` counts everyone, `list` the first 32. */
export interface ChatRoster {
  t: 'chat_roster'
  scope: 'run'
  count: number
  list: ChatPerson[]
}

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
export type AttentionState = '' | 'working' | 'needs_input' | 'done'

export type AttentionKind = '' | 'permission' | 'prompt' | 'done'

/** A quick-reply choice; `input` is exactly what a client sends as INPUT. */
export interface AttentionOption {
  label: string
  input: string
}

export interface Attention {
  state: AttentionState
  message?: string
  source?: string
  since?: string
  kind?: AttentionKind
  options?: AttentionOption[]
}
export type TransportKind = 'ws' | 'webrtc' | 'relay'

/** One attached client, as listed in the `viewers` roster. */
export interface ViewerInfo {
  id: string
  name: string
  role: Role
  /** Label of the share link the viewer joined through, if any. */
  link?: string
  since: string
  lastInputAt?: string
  /** A connection for a run's chat alone (`hello.chatOnly`): left out of a session's `viewers`, on the run's roster. */
  quiet?: boolean
}

/** One line of a session's activity log (`activity` control message). */
export interface ActivityEntry {
  at: string
  type:
    | 'attention' | 'input' | 'join' | 'leave' | 'link' | 'status'
    | 'progress' | 'artifact' | 'handoff' | 'tool_use' | 'tool_denied' | 'error'
  by?: string
  byName?: string
  message?: string
  /** `artifact`: where the result lives (≤ 2048 bytes). Stored as sent: link it only when it is http(s). */
  url?: string
  /** `handoff`: who the work goes to (≤ 40 characters). */
  to?: string
  /** `tool_use`, `tool_denied`, `error`: the tool involved (≤ 100 bytes). */
  tool?: string
  /** GET /api/events only: for an attention entry, the state it records. Absent from a session's own replay and from an older host's entries. */
  state?: 'needs_input' | 'working' | 'done'
}

/** Data of an `activity` event on GET /api/events: an activity entry and the session it belongs to. */
export type SessionActivity = ActivityEntry & { sessionId: string }

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
  /** The owner takes `chat` and `chat_send`; absent from an older owner, which must be sent neither. */
  chat?: boolean
  /** The session is a run's member with a run chat: scope `run` posts and sends, and `chat_roster`. */
  runChat?: boolean
  iceServers?: ICEServer[]
  relayTimeoutMs?: number
  relayOnly?: boolean
}

export type ControlMessage =
  | Welcome
  | { t: 'ready' }
  | { t: 'resize'; cols: number; rows: number; by?: string }
  | { t: 'status'; status: string; exitCode?: number }
  | { t: 'attention'; state: AttentionState; message?: string; source?: string; kind?: AttentionKind; options?: AttentionOption[] }
  | { t: 'viewers'; count: number; list?: ViewerInfo[] }
  | { t: 'activity'; at: string; type: ActivityEntry['type']; by?: string; byName?: string; message?: string; url?: string; to?: string; tool?: string }
  | { t: 'error'; code: string; message: string; requestId?: string }
  | { t: 'pong'; ts: number }
  | ChatMessage
  | ChatHistory
  | ChatRoster

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

/**
 * Host control connection (`/ws/host`, docs/protocol.md): a host asks the
 * server for a share link to its session and the server answers, or refuses
 * with an `error` naming the request. The web never sends these; they are
 * mirrored here with their limits, as every message is.
 */
export interface HostLinkMessage {
  t: 'link'
  /** ≤ 32 bytes. */
  requestId: string
  role: 'view' | 'control'
  /** ≤ 86400 (a day). */
  ttlSeconds?: number
  /** ≤ 120 bytes. */
  label?: string
}

export interface HostLinkCreatedMessage {
  t: 'link_created'
  requestId: string
  url: string
  /** `conductor://<host>/join/<token>`, which the desktop app opens itself. */
  invite: string
  linkId: string
  role: 'view' | 'control'
  label?: string
  expiresAt?: string
}

export interface HostLinkRevokeMessage {
  t: 'link_revoke'
  /** ≤ 32 bytes. */
  requestId: string
  /** ≤ 64 bytes: a linkId from link_created. */
  linkId: string
}

export interface HostLinkRevokedMessage {
  t: 'link_revoked'
  requestId: string
  linkId: string
}

/** A host connection may ask for this many links a minute (a revoke is never counted). */
export const HOST_LINK_REQUESTS_PER_MINUTE = 5
/** A link id in a revoke is at most this long. */
export const HOST_MAX_LINK_ID = 64

/** What a host's register says of the host and its session that fixes the session's id across a server restart (docs/protocol.md). */
export interface HostIdentity {
  /** A secret of the host process: with the session's local id, the session's id on the server. Never shown or logged. */
  instance?: string
  /** The session's id on the host. */
  localId?: string
}
/** The bounds of HostIdentity's fields. */
export const HOST_MAX_INSTANCE = 64
export const HOST_MAX_LOCAL_ID = 64

/** The part of registered that says which links the server still holds for the session; absent from an older server. */
export interface HostRegisteredLinks {
  links?: string[]
}

/** A crew run as a switchyard shows it: what a host sends with link_run and link_run_update. */
export interface HostRunGroup {
  id: string
  name: string
  members: Array<{ name: string; sessionId?: string; agentId: string; status: 'pending' | 'starting' | 'running' | 'ended' }>
}
/** host → server: one link to the sessions of a run's members; answered by link_created with runId. */
export interface HostRunLinkMessage {
  t: 'link_run'
  requestId: string
  role: 'view' | 'control'
  ttlSeconds?: number
  label?: string
  run: HostRunGroup
}
/** host → server: the run's members now; answered by link_run_updated, or error not_found. */
export interface HostRunLinkUpdateMessage {
  t: 'link_run_update'
  requestId: string
  run: HostRunGroup
}
/** server → host: how many of the run's links follow the new members. */
export interface HostRunLinkUpdatedMessage {
  t: 'link_run_updated'
  requestId: string
  runId: string
  links: number
}
/** The bounds of a run group, and how many updates a connection may send a minute. */
export const HOST_MAX_RUN_LINK_MEMBERS = 32
export const HOST_MAX_RUN_ID = 64
export const HOST_MAX_RUN_NAME = 120
export const HOST_MAX_RUN_MEMBER_NAME = 40
export const HOST_RUN_LINK_UPDATES_PER_MINUTE = 30
