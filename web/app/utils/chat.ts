import type { MemberStatus } from './crews'
import type { ChatMessage, Role } from './protocol'
import { CHAT_COUNTER_FROM, MAX_CHAT_TEXT } from './protocol'
import { linkableUrl } from './events'

/**
 * The chat beside the terminal (design 2a–2h): what the panels compute from
 * the messages the owner sends. Pure, for the tests; the components only draw.
 */

const encoder = new TextEncoder()

/** The bytes a text takes on the wire, which is what the owner bounds. */
export function chatBytes(text: string): number {
  return encoder.encode(text).length
}

/**
 * A message's text as the owner keeps it (session.CleanChatText): line breaks
 * as \n, every other control character but a tab dropped, trimmed. Applied
 * before sending, so the counter and the bound agree with the owner.
 */
export function cleanChatText(text: string): string {
  return text
    .replace(/\r\n/g, '\n')
    .replace(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g, '')
    .trim()
}

/** The composer's counter: nothing under 1.5 KiB, then "1.6 / 2 KiB". */
export function chatCounter(bytes: number): string {
  if (bytes < CHAT_COUNTER_FROM) return ''
  return `${(bytes / 1024).toFixed(1)} / ${MAX_CHAT_TEXT / 1024} KiB`
}

export function chatTooLong(bytes: number): boolean {
  return bytes > MAX_CHAT_TEXT
}

/** A piece of a message: plain text, or a link when `href` is set. */
export interface ChatSegment {
  text: string
  href?: string
}

const URL_RE = /https?:\/\/[^\s<>"']+/g

/**
 * The text split around its links: an http(s) URL `linkableUrl` allows is a
 * link, trailing punctuation left out of it. Text only otherwise: nothing a
 * person writes is rendered as HTML.
 */
export function linkify(text: string): ChatSegment[] {
  const out: ChatSegment[] = []
  let last = 0
  for (const m of text.matchAll(URL_RE)) {
    let url = m[0]
    for (;;) {
      const end = url.at(-1) ?? ''
      if ('.,;:!?'.includes(end) || (end === ')' && (url.match(/\(/g)?.length ?? 0) < (url.match(/\)/g)?.length ?? 0))) url = url.slice(0, -1)
      else break
    }
    const href = linkableUrl(url)
    const start = m.index ?? 0
    if (start > last) out.push({ text: text.slice(last, start) })
    if (href) out.push({ text: url, href })
    else out.push({ text: url })
    // The punctuation left off the link stays with the text that follows it.
    last = start + url.length
  }
  if (last < text.length) out.push({ text: text.slice(last) })
  return out.length ? out : [{ text: '' }]
}

/** "Jane joined · control", "Jane left", "Answered by Nate". */
export function systemLine(m: Pick<ChatMessage, 'by' | 'event'>): string {
  if (m.event === 'answered') return `Answered by ${m.by.name}`
  return m.event === 'leave' ? `${m.by.name} left` : `${m.by.name} joined · ${m.by.role}`
}

/** The questions answered so far: the refs of the `answered` lines. A question not among them still takes an answer. */
export function answeredQuestions(messages: readonly Pick<ChatMessage, 'kind' | 'event' | 'ref'>[]): ReadonlySet<string> {
  const out = new Set<string>()
  for (const m of messages) if (m.kind === 'system' && m.event === 'answered' && m.ref) out.add(m.ref)
  return out
}

/** "Sent to agent by Nate · 08:32:40", "Sent to core by Nate · 08:35:10". */
export function markerLine(m: Pick<ChatMessage, 'by' | 'to' | 'at'>): string {
  return `Sent to ${m.to || 'agent'} by ${m.by.name} · ${chatTimeSeconds(m.at)}`
}

function clock(at: string, seconds: boolean): string {
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', ...(seconds ? { second: '2-digit' } : {}), hour12: false })
}

/** "08:32". */
export function chatTime(at: string): string {
  return clock(at, false)
}

/** "08:32:40". */
export function chatTimeSeconds(at: string): string {
  return clock(at, true)
}

/** The list with `m` in it once, in order of time then id: a message seen twice (the history and the live frame) stays one. */
export function mergeMessage(list: readonly ChatMessage[], m: ChatMessage): ChatMessage[] {
  const i = list.findIndex((x) => x.id === m.id)
  if (i >= 0) {
    const out = [...list]
    out[i] = m
    return out
  }
  const after = (a: ChatMessage, b: ChatMessage) => a.at > b.at || (a.at === b.at && a.id > b.id)
  if (!list.length || after(m, list[list.length - 1]!)) return [...list, m]
  const out = [...list]
  let j = out.length
  while (j > 0 && after(out[j - 1]!, m)) j--
  out.splice(j, 0, m)
  return out
}

/** Whether a message can be typed into the agent from here: a controller, on a session still running. */
export function canSendToAgent(role: Role, ended: boolean): boolean {
  return role === 'control' && !ended
}

/** A message's own id for the optimistic row, echoed back by the owner. */
export function chatNonce(n: number): string {
  return `c${Date.now().toString(36)}${n.toString(36)}`
}

/** The phone sheet's snap points (design 2c): two thirds of the window, then all of it. */
export const SHEET_SNAPS: readonly number[] = [0.66, 1]

/** One choice of a run chat's composer menu (design 2e): chat only, or also typed into a member. */
export interface ScopeItem {
  /** '' for chat only, else the member's name. */
  to: string
  label: string
  detail: string
  disabled: boolean
}

/** What a member's state means for a message typed into it, as the broadcast bar says it. */
const SCOPE_DETAIL: Record<MemberStatus, { detail: string; ok: boolean }> = {
  running: { detail: 'typed into its terminal', ok: true },
  needs_input: { detail: 'waiting on a prompt: skipped', ok: false },
  pending: { detail: 'not started', ok: false },
  starting: { detail: 'starting', ok: false },
  ended: { detail: 'ended', ok: false },
}

/**
 * The composer's menu on a run's chat: "Chat only · everyone here reads it",
 * then "Also send to <member>" for each member, typed into its terminal when
 * it runs and disabled with why not otherwise, as the broadcast bar does.
 */
export function scopeItems(members: readonly { name: string }[], states: ReadonlyMap<string, MemberStatus>): ScopeItem[] {
  const out: ScopeItem[] = [{ to: '', label: 'Chat only', detail: 'everyone here reads it', disabled: false }]
  for (const m of members) {
    const st = SCOPE_DETAIL[states.get(m.name) ?? 'pending']
    out.push({ to: m.name, label: `Also send to ${m.name}`, detail: st.detail, disabled: !st.ok })
  }
  return out
}
