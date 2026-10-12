import { describe, expect, it } from 'vitest'
import { MAX_QUESTION_ID } from './protocol'
import { answerMessage, keepQuestionId, questionOf, repliedQuestions, ReplyError, replyFailure } from './reply'
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

describe('keepQuestionId', () => {
  const shown = { state: 'needs_input' as const, message: 'Allow?', options: [{ label: 'Yes', input: 'y' }], id: '0123456789abcdef' }

  it('keeps the id the terminal reported when a listing without one shows the same question', () => {
    expect(keepQuestionId({ state: 'needs_input', message: 'Allow?', options: [{ label: 'Yes', input: 'y' }] }, shown).id).toBe('0123456789abcdef')
  })

  it('takes the listing as it is for another question, one with its own id, or nothing shown', () => {
    expect(keepQuestionId({ state: 'needs_input', message: 'Allow Edit?', options: [{ label: 'Yes', input: 'y' }] }, shown).id).toBeUndefined()
    expect(keepQuestionId({ state: 'needs_input', message: 'Allow?', options: [{ label: 'No', input: 'n' }] }, shown).id).toBeUndefined()
    expect(keepQuestionId({ state: 'done', message: 'Allow?' }, shown).id).toBeUndefined()
    expect(keepQuestionId({ state: 'needs_input', message: 'Allow?', options: [{ label: 'Yes', input: 'y' }], id: 'fedcba9876543210' }, shown).id).toBe('fedcba9876543210')
    expect(keepQuestionId({ state: 'needs_input', message: 'Allow?' }, undefined).id).toBeUndefined()
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
    expect(replyFailure(refused, 'fix-auth')).toEqual({ title: 'Already answered', description: "fix-auth's question was answered before your reply arrived. Nothing was sent.", color: 'warning' })
    expect(replyFailure(refused).description).toBe('This question was answered before your reply arrived. Nothing was sent.')
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

  it('an older owner\'s question has none: the row answers as before', () => {
    const row = sessionRow(session({ state: 'needs_input', message: 'Allow?' }))
    expect(row.prompt?.question).toBeUndefined()
    expect(rowPrompt(row)?.question).toBeUndefined()
  })
})
