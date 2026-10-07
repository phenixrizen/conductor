import { describe, expect, it } from 'vitest'
import type { ChatMessage } from './protocol'
import type { MemberStatus } from './crews'
import { SHEET_SNAPS, answeredQuestions, canSendToAgent, chatBytes, chatCounter, chatNonce, chatTime, chatTimeSeconds, chatTooLong, cleanChatText, linkify, markerLine, mergeMessage, scopeItems, systemLine } from './chat'

const msg = (over: Partial<ChatMessage>): ChatMessage => ({ t: 'chat', id: 'a', at: '2026-10-06T08:32:40Z', scope: 'session', kind: 'message', by: { id: 'x', name: 'Nate', role: 'control' }, text: 'hi', ...over })

describe('chat text', () => {
  it('cleans as the owner does and counts bytes', () => {
    expect(cleanChatText('  a\r\nb\u0007c\u001b[31md  ')).toBe('a\nbc[31md')
    expect(cleanChatText('tab\tkept')).toBe('tab\tkept')
    expect(cleanChatText('\n\n')).toBe('')
    expect(chatBytes('é')).toBe(2)
    expect(chatBytes('🚂')).toBe(4)
  })

  it('counts past 1.5 KiB and refuses past 2 KiB', () => {
    expect(chatCounter(100)).toBe('')
    expect(chatCounter(1535)).toBe('')
    expect(chatCounter(1536)).toBe('1.5 / 2 KiB')
    expect(chatCounter(1638)).toBe('1.6 / 2 KiB')
    expect(chatCounter(2100)).toBe('2.1 / 2 KiB')
    expect(chatTooLong(2048)).toBe(false)
    expect(chatTooLong(2049)).toBe(true)
  })

  it('links http(s) URLs, trailing punctuation left out, and nothing else', () => {
    expect(linkify('see https://github.com/acme/api/pull/212.')).toEqual([{ text: 'see ' }, { text: 'https://github.com/acme/api/pull/212', href: 'https://github.com/acme/api/pull/212' }, { text: '.' }])
    expect(linkify('(https://x.example/a) and https://y.example/b?q=1,')).toEqual([
      { text: '(' },
      { text: 'https://x.example/a', href: 'https://x.example/a' },
      { text: ') and ' },
      { text: 'https://y.example/b?q=1', href: 'https://y.example/b?q=1' },
      { text: ',' },
    ])
    expect(linkify('https://en.wikipedia.org/wiki/Rail_(disambiguation)')).toEqual([{ text: 'https://en.wikipedia.org/wiki/Rail_(disambiguation)', href: 'https://en.wikipedia.org/wiki/Rail_(disambiguation)' }])
    expect(linkify('plain words')).toEqual([{ text: 'plain words' }])
    expect(linkify('')).toEqual([{ text: '' }])
    expect(linkify('ftp://x.example/f and javascript:alert(1)')).toEqual([{ text: 'ftp://x.example/f and javascript:alert(1)' }])
  })
})

describe('chat lines', () => {
  const at = (iso: string, s: boolean) => new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', ...(s ? { second: '2-digit' } : {}), hour12: false })
  it('words the system lines and the markers', () => {
    expect(systemLine(msg({ kind: 'system', event: 'join', by: { id: 'j', name: 'Jane', role: 'control' } }))).toBe('Jane joined · control')
    expect(systemLine(msg({ kind: 'system', event: 'leave', by: { id: 'j', name: 'Jane', role: 'view' } }))).toBe('Jane left')
    expect(markerLine(msg({ kind: 'sent_to_agent', ref: 'a' }))).toBe(`Sent to agent by Nate · ${at('2026-10-06T08:32:40Z', true)}`)
    expect(markerLine(msg({ kind: 'sent_to_agent', ref: 'a', to: 'core' }))).toBe(`Sent to core by Nate · ${at('2026-10-06T08:32:40Z', true)}`)
    expect(chatTime('2026-10-06T08:32:40Z')).toBe(at('2026-10-06T08:32:40Z', false))
    expect(chatTimeSeconds('bad')).toBe('')
  })

  it('merges a message once, in order of time', () => {
    const a = msg({ id: 'a', at: '2026-10-06T08:32:40Z' })
    const b = msg({ id: 'b', at: '2026-10-06T08:32:41Z' })
    const c = msg({ id: 'c', at: '2026-10-06T08:32:39Z' })
    let list = mergeMessage([], a)
    list = mergeMessage(list, b)
    list = mergeMessage(list, c)
    expect(list.map((m) => m.id)).toEqual(['c', 'a', 'b'])
    list = mergeMessage(list, { ...b, text: 'edited' })
    expect(list.map((m) => m.id)).toEqual(['c', 'a', 'b'])
    expect(list[2]!.text).toBe('edited')
    const same = msg({ id: 'd', at: '2026-10-06T08:32:40Z' })
    expect(mergeMessage(list, same).map((m) => m.id)).toEqual(['c', 'a', 'd', 'b'])
  })

  it('lets a controller send to a live agent only', () => {
    expect(canSendToAgent('control', false)).toBe(true)
    expect(canSendToAgent('view', false)).toBe(false)
    expect(canSendToAgent('control', true)).toBe(false)
    expect(chatNonce(1)).toMatch(/^c[0-9a-z]+1$/)
    expect(chatNonce(1)).not.toBe(chatNonce(2))
  })

  it('opens the phone sheet to two thirds of the window, then all of it', () => {
    expect(SHEET_SNAPS).toEqual([0.66, 1])
  })

  it("says who answered a question, and which questions still take an answer", () => {
    expect(systemLine({ by: { id: 'x', name: 'Nate', role: 'control' }, event: 'answered' })).toBe('Answered by Nate')
    const list = [
      msg({ id: 'q1', kind: 'question', by: { id: 'agent', name: 'codex', role: 'agent' }, text: 'Trust?' }),
      msg({ id: 'q2', kind: 'question', by: { id: 'agent', name: 'codex', role: 'agent' }, text: 'Which?' }),
      msg({ id: 'a1', kind: 'system', event: 'answered', ref: 'q1' }),
      msg({ id: 'j', kind: 'system', event: 'join' }),
    ]
    const done = answeredQuestions(list)
    expect(done.has('q1')).toBe(true)
    expect(done.has('q2')).toBe(false)
  })

  it("offers a run chat's composer chat only, then each member, the ones that cannot take a line disabled with why", () => {
    const states = new Map<string, MemberStatus>([
      ['core', 'running'],
      ['tests', 'running'],
      ['review', 'needs_input'],
      ['lead', 'pending'],
      ['docs', 'ended'],
    ])
    const items = scopeItems([{ name: 'core' }, { name: 'tests' }, { name: 'review' }, { name: 'lead' }, { name: 'docs' }, { name: 'new' }], states)
    expect(items.map((i) => `${i.to || '-'}: ${i.label} · ${i.detail}${i.disabled ? ' (off)' : ''}`)).toEqual([
      '-: Chat only · everyone here reads it',
      'core: Also send to core · typed into its terminal',
      'tests: Also send to tests · typed into its terminal',
      'review: Also send to review · waiting on a prompt: skipped (off)',
      'lead: Also send to lead · not started (off)',
      'docs: Also send to docs · ended (off)',
      'new: Also send to new · not started (off)',
    ])
    expect(markerLine({ by: { id: 'x', name: 'Nate', role: 'control' }, to: 'core', at: '2026-10-06T08:35:10Z' })).toMatch(/^Sent to core by Nate · \d\d:35:10$/)
  })
})
