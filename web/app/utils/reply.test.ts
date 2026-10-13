import { describe, expect, it } from 'vitest'
import { MAX_QUESTION_ID } from './protocol'
import { answerMessage, draftQuestion, listedAttention, questionOf, repliedQuestions, ReplyError, replyFailure } from './reply'
import { sessionRow } from './sidebar'
import { rowPrompt } from './sidebarActions'
import type { SessionInfo } from '~/composables/useSessions'

describe('questionOf', () => {
  it('is the id of a question that waits on input, and nothing for any other state or an older owner', () => {
    expect(questionOf({ state: 'needs_input', id: '0123456789abcdef' })).toBe('0123456789abcdef')
    expect(questionOf({ state: 'needs_input' })).toBeUndefined()
    expect(questionOf({ state: 'done', id: '0123456789abcdef' })).toBeUndefined()
    expect(questionOf({ state: '' })).toBeUndefined()
    expect(questionOf(undefined)).toBeUndefined()
  })
})

describe('draftQuestion', () => {
  const q1 = { state: 'needs_input' as const, id: 'q1q1q1q1q1q1q1q1' }
  const q2 = { state: 'needs_input' as const, id: 'q2q2q2q2q2q2q2q2' }
  it('binds a line to the question shown as its first character is typed', () => {
    expect(draftQuestion('', 'm', undefined, q1)).toBe(q1.id)
    expect(draftQuestion('  ', 'main', undefined, q1)).toBe(q1.id)
  })
  it('keeps naming that question as the line grows, whatever shows by then (someone else answered, the next came)', () => {
    let bound = draftQuestion('', 'm', undefined, q1)
    bound = draftQuestion('m', 'ma', bound, q2)
    bound = draftQuestion('ma', 'main', bound, q2)
    expect(bound).toBe(q1.id)
  })
  it('binds anew once the line is emptied and begun again', () => {
    let bound = draftQuestion('', 'main', undefined, q1)
    bound = draftQuestion('main', '', bound, q2)
    bound = draftQuestion('', 'x', bound, q2)
    expect(bound).toBe(q2.id)
  })
  it('names nothing for a line begun while no question waits, or for an older owner', () => {
    expect(draftQuestion('', 'x', undefined, { state: 'working' })).toBeUndefined()
    expect(draftQuestion('', 'x', undefined, { state: 'needs_input' })).toBeUndefined()
  })
})

describe('answerMessage', () => {
  it('names the question a choice or a line was shown for', () => {
    expect(answerMessage({ question: 'q1', input: 'y' }, 'a1')).toEqual({ t: 'answer', reqId: 'a1', question: 'q1', input: 'y' })
    expect(answerMessage({ question: 'q1', text: 'Postgres' }, 'a2')).toEqual({ t: 'answer', reqId: 'a2', question: 'q1', text: 'Postgres' })
  })

  it('is nothing for a reply that names no question the owner could take: it goes as before', () => {
    expect(answerMessage({ input: 'y' }, 'a1')).toBeNull()
    expect(answerMessage({ question: '', text: 'x' }, 'a1')).toBeNull()
    expect(answerMessage({ question: 'q'.repeat(MAX_QUESTION_ID + 1), input: 'y' }, 'a1')).toBeNull()
    expect(answerMessage({ question: 'a b', input: 'y' }, 'a1')).toBeNull()
    expect(answerMessage({ question: 'q1', input: '' }, 'a1')).toBeNull()
    expect(answerMessage({ question: 'q'.repeat(MAX_QUESTION_ID), input: 'y' }, 'a1')).not.toBeNull()
  })
})

describe('listedAttention', () => {
  const shown = { state: 'needs_input' as const, message: 'Allow?', options: [{ label: 'Yes', input: 'y' }], id: '0123456789abcdef' }

  it("leaves the owner's own word standing while its connection is open and the listing names no question", () => {
    expect(listedAttention({ state: 'needs_input', message: 'Which port?' }, shown, true)).toBe(shown)
    expect(listedAttention({ state: '' }, shown, true)).toBe(shown)
  })

  it('takes the listing when it names a question, when nothing named one, or with the connection closed', () => {
    const named = { state: 'needs_input' as const, message: 'Allow?', options: [{ label: 'Yes', input: 'y' }], id: 'fedcba9876543210' }
    expect(listedAttention(named, shown, true)).toBe(named)
    const plain = { state: 'needs_input' as const, message: 'Allow?' }
    expect(listedAttention(plain, { state: '' }, true)).toBe(plain)
    expect(listedAttention(plain, shown, false)).toBe(plain)
  })

  it('never gives a listing the id of the question shown by its words: the same words asked again are another question', () => {
    const again = { state: 'needs_input' as const, message: 'Allow?', options: [{ label: 'Yes', input: 'y' }] }
    expect(listedAttention(again, shown, false).id).toBeUndefined()
  })
})

describe('repliedQuestions', () => {
  it('lets one reply per question go from a page: a double click or a digit pressed twice is one answer', () => {
    const replied = repliedQuestions()
    expect(replied.take('q1')).toBe(true)
    expect(replied.take('q1')).toBe(false)
    expect(replied.take('q2')).toBe(true)
    // A reply that names no question (an older owner's) always goes.
    expect(replied.take(undefined)).toBe(true)
    expect(replied.take(undefined)).toBe(true)
  })

  it('forgets a question whose reply did not go for another reason, so it can be tried again; remembers the newest 64', () => {
    const replied = repliedQuestions()
    replied.take('q1')
    replied.release('q1')
    expect(replied.take('q1')).toBe(true)
    for (let i = 0; i < 64; i++) replied.take(`n${i}`)
    expect(replied.take('q1')).toBe(true)
    expect(replied.take('n63')).toBe(false)
  })
})

describe('replyFailure', () => {
  it('says a question was answered already, nothing sent, naming the session where the page lists several', () => {
    const refused = new ReplyError('question_answered', 'this question was already answered: nothing was sent')
    expect(replyFailure(refused, 'fix-auth')).toEqual({
      title: 'Already answered',
      description: "fix-auth's question was answered before your reply arrived. Nothing was sent. If the agent still asks, answer it in its terminal.",
      color: 'warning',
    })
    expect(replyFailure(refused).description).toMatch(/^This question was answered before your reply arrived\. Nothing was sent\./)
  })

  it('says a reply the session did not confirm in time, or before the connection closed, was not confirmed, and to look first', () => {
    const late = replyFailure(new ReplyError('no_word', 'x'), 'fix-auth')
    expect(late).toMatchObject({ title: 'Not confirmed', color: 'warning' })
    expect(late.description).toBe('fix-auth did not say within 15 s whether it took your reply. Look at the session before you answer again.')
    expect(replyFailure(new ReplyError('closed', 'x')).description).toBe('The session did not say before the connection closed whether it took your reply. Look at the session before you answer again.')
  })

  it('says a reply that could not name its question was not sent', () => {
    expect(replyFailure(new ReplyError('no_question', 'x'))).toMatchObject({ title: 'Not sent', color: 'warning' })
    expect(replyFailure(new ReplyError('no_question', 'x')).description).toContain('nothing was sent')
  })

  it('says a terminal not connected, and any other failure with its own words', () => {
    expect(replyFailure(new ReplyError('not_connected', 'not connected'))).toMatchObject({ title: 'Not connected', color: 'warning' })
    expect(replyFailure(new Error('This link is view-only'), 'fix-auth')).toEqual({ title: 'Reply to fix-auth failed', description: 'This link is view-only', color: 'error' })
    expect(replyFailure(new ReplyError('read_only', 'this link is view-only'))).toEqual({ title: 'Reply failed', description: 'this link is view-only', color: 'error' })
  })
})

describe('the sidebar row names its question', () => {
  const session = (attention: SessionInfo['attention']): SessionInfo =>
    ({ id: 's1', name: 'fix-auth', kind: 'server', agentId: 'codex', status: 'running', attention, createdAt: '2026-10-12T08:00:00Z' }) as unknown as SessionInfo

  it('carries the id of the question it shows into the answer it offers (a click, a digit, a typed line)', () => {
    const row = sessionRow(session({ state: 'needs_input', message: 'Allow?', options: [{ label: 'Yes', input: 'y' }], id: '0123456789abcdef' }))
    expect(row.prompt?.question).toBe('0123456789abcdef')
    expect(rowPrompt(row)?.question).toBe('0123456789abcdef')
  })

  it("a hosted session's row names the host's id, as the server lists it", () => {
    const hosted = { ...session({ state: 'needs_input', message: 'Allow?', options: [{ label: 'Yes', input: 'y' }], id: 'fedcba9876543210' }), kind: 'hosted' } as SessionInfo
    expect(rowPrompt(sessionRow(hosted))?.question).toBe('fedcba9876543210')
  })

  it('an older owner\'s question has none: the row answers as before', () => {
    const row = sessionRow(session({ state: 'needs_input', message: 'Allow?' }))
    expect(row.prompt?.question).toBeUndefined()
    expect(rowPrompt(row)?.question).toBeUndefined()
  })
})
