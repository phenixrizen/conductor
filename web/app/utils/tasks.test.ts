import { describe, expect, it } from 'vitest'
import type { RunTask } from '~/composables/useSessions'
import { CommandRefused, mintCommandId, sendCommand, taskActions, taskBadges, taskCounts, taskWords, type Receipt } from './tasks'

const task = (p: Partial<RunTask>): RunTask => ({ id: 'build', member: 'lead', kind: 'work', state: 'ready', attempts: 0, ...p })

describe('taskCounts and taskBadges', () => {
  const tasks = [
    task({ id: 'a', state: 'succeeded' }),
    task({ id: 'b', state: 'failed' }),
    task({ id: 'c', state: 'running' }),
    task({ id: 'd', state: 'verifying' }),
    task({ id: 'e', state: 'blocked' }),
    task({ id: 'f', state: 'ready' }),
    task({ id: 'g', state: 'cancelled' }),
  ]
  it('counts every state', () => {
    expect(taskCounts(tasks)).toEqual({ total: 7, done: 1, failed: 1, cancelled: 1, running: 1, checking: 1, waiting: 2 })
    expect(taskCounts(undefined).total).toBe(0)
  })
  it('says the counts as badges, the wrong and the moving ones only when there are any', () => {
    expect(taskBadges({ tasks, paused: true })).toEqual([
      { label: '1/7 tasks', color: 'neutral' },
      { label: '1 failed', color: 'error' },
      { label: '1 cancelled', color: 'neutral' },
      { label: '1 running', color: 'success' },
      { label: '1 checking', color: 'info' },
      { label: 'paused', color: 'warning' },
    ])
    expect(taskBadges({ tasks: [task({ state: 'succeeded' })], outcome: 'succeeded' })).toEqual([{ label: '1/1 tasks', color: 'success' }])
  })
})

describe('taskWords', () => {
  const run = {}
  it.each([
    [task({ state: 'blocked', needs: ['build'], reason: 'needs build' }), 'needs build', 'muted'],
    [task({ state: 'blocked', needs: ['build'], reason: 'needs build, which will not succeed' }), 'needs build, which will not succeed', 'error'],
    [task({ state: 'ready' }), 'ready to start', 'default'],
    [task({ state: 'ready', reason: 'attempt 1 failed at check go: exited 1' }), 'attempt 1 failed at check go: exited 1; ready to start', 'default'],
    [task({ state: 'running', attempts: 2, attempt: { n: 2, state: 'running' } }), 'attempt 2 running', 'default'],
    [task({ state: 'running', attempts: 1, attempt: { n: 1, state: 'running' }, blocker: { reason: 'no token', by: 'lead', at: '' } }), 'attempt 1 blocked by lead: no token', 'warning'],
    [task({ state: 'verifying', attempts: 1, attempt: { n: 1, state: 'verifying' } }), 'attempt 1: checking the result', 'default'],
    [task({ state: 'succeeded', result: { attempt: 2, commit: 'abc', summary: 'built it', at: '' } }), 'succeeded by attempt 2: built it', 'success'],
    [task({ state: 'succeeded', kind: 'review', result: { attempt: 1, verdict: 'approved', at: '' } }), 'approved', 'success'],
    [task({ state: 'succeeded', kind: 'review', result: { attempt: 1, verdict: 'needs_changes', findings: [{ severity: 'major', message: 'x' }], at: '' } }), 'asked for changes (1 findings)', 'success'],
    [task({ state: 'failed', reason: 'attempt 3 failed at check go: exited 1' }), 'attempt 3 failed at check go: exited 1', 'error'],
    [task({ state: 'cancelled' }), 'cancelled', 'muted'],
  ] as const)('%o → %s', (t, text, tone) => {
    expect(taskWords(t, run)).toEqual({ text, tone })
  })
  it('says why a ready task does not start, and what a continuation takes up', () => {
    expect(taskWords(task({ state: 'ready' }), { paused: true })).toEqual({ text: 'ready, the dispatch is paused', tone: 'warning' })
    expect(taskWords(task({ state: 'ready' }), { stoppedAt: 'x' })).toEqual({ text: 'ready, the run is stopped', tone: 'muted' })
    expect(taskWords(task({ state: 'ready', reason: 'attempt 2 was interrupted by the server\'s end: the run continues it' }), { state: 'interrupted' })).toEqual({
      text: "attempt 2 was interrupted by the server's end: the run continues it; taken up when the run is continued",
      tone: 'warning',
    })
    expect(taskWords(task({ state: 'succeeded', importedFrom: 'r-1/build/1', result: { attempt: 1, commit: 'abc', summary: 'built', at: '' } }), {})).toEqual({ text: 'succeeded by attempt 1 (kept from the run continued): built', tone: 'success' })
  })
})

describe('taskActions', () => {
  const live = {}
  it('offers retry and accept on a failed task with a submitted commit, cancel on a live one, open and copy with an attempt', () => {
    expect(taskActions(task({ state: 'failed', attempt: { n: 3, state: 'failed', result: 'abc', sessionId: 's', branch: 'crew/r/build.3' } }), live)).toEqual({ retry: true, accept: true, cancel: false, open: true, copyBranch: true, showResult: false })
    expect(taskActions(task({ state: 'failed', attempt: { n: 3, state: 'failed' } }), live).accept).toBe(false)
    expect(taskActions(task({ state: 'failed', kind: 'review', attempt: { n: 1, state: 'failed', result: 'abc' } }), live).accept).toBe(false)
    expect(taskActions(task({ state: 'cancelled' }), live)).toMatchObject({ retry: true, accept: false, cancel: false })
    expect(taskActions(task({ state: 'running', attempt: { n: 1, state: 'running', sessionId: 's' } }), live)).toMatchObject({ retry: false, cancel: true, open: true })
    expect(taskActions(task({ state: 'succeeded', result: { attempt: 1, at: '' } }), live)).toMatchObject({ retry: false, cancel: false, showResult: true })
  })
  it('offers nothing that changes a stopped run, a record or a run continued', () => {
    expect(taskActions(task({ state: 'failed', attempt: { n: 1, state: 'failed', result: 'abc' } }), { stoppedAt: 'x' })).toMatchObject({ retry: false, accept: false, cancel: false })
    expect(taskActions(task({ state: 'running' }), { record: true })).toMatchObject({ cancel: false })
    // A finished run, continued: its continuation owns its tasks.
    expect(taskActions(task({ state: 'failed', attempt: { n: 1, state: 'failed', result: 'abc', sessionId: 's' } }), { resumedBy: 'next' })).toMatchObject({ retry: false, accept: false, cancel: false, open: true })
  })
})

describe('commands with receipts', () => {
  const receipt = (p: Partial<Receipt>): Receipt => ({ commandId: 'x', intent: 'cancel', revision: 3, status: 'done', ...p })
  it('mints 16 hex characters, different each time', () => {
    const a = mintCommandId()
    const b = mintCommandId()
    expect(a).toMatch(/^[0-9a-f]{16}$/)
    expect(b).not.toBe(a)
  })
  it('sends one id, carries the expected revision, and retries a lost reply once with the same id', async () => {
    const ids: string[] = []
    let calls = 0
    const r = await sendCommand(async (body) => {
      ids.push(body.commandId)
      expect(body.expectedRevision).toBe(7)
      if (++calls === 1) throw Object.assign(new Error('Cannot reach the server'), { status: 0 })
      return { receipt: receipt({ commandId: body.commandId, repeat: true }) }
    }, 7)
    expect(calls).toBe(2)
    expect(ids[0]).toBe(ids[1])
    expect(r.repeat).toBe(true)
    expect(r.commandId).toBe(ids[0])
  })
  it('gives up after a second lost reply, and leaves other errors as they are', async () => {
    await expect(sendCommand(async () => Promise.reject(Object.assign(new Error('Cannot reach the server'), { status: 0 })))).rejects.toThrow('Cannot reach the server')
    await expect(sendCommand(async () => Promise.reject(Object.assign(new Error('stale'), { status: 409, code: 'stale_revision' })))).rejects.toThrow('stale')
  })
  it('turns a refusal with a receipt into its words', async () => {
    const failed = receipt({ status: 'failed', error: 'the task is not in a state for that: it is running', refusal: 'task_state' })
    const err = await sendCommand(async () => Promise.reject(Object.assign(new Error('refused'), { status: 409, body: { error: { code: 'task_state', message: failed.error }, receipt: failed } }))).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(CommandRefused)
    expect((err as CommandRefused).message).toBe('the task is not in a state for that: it is running')
    expect((err as CommandRefused).receipt.refusal).toBe('task_state')
  })
})
