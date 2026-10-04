import { describe, expect, it } from 'vitest'
import type { CrewInput, CrewMember, RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import {
  askedCount,
  barsSummary,
  changedFields,
  crewShape,
  liveRunLine,
  medianMinutes,
  memberWords,
  minutesLabel,
  ownError,
  runBars,
  runNote,
  runOutcome,
  runTook,
  runWhen,
  shortRunId,
  startSentence,
  startWords,
  STOPPED_ERROR,
} from './crewWords'

const m = (name: string, start: CrewMember['start'] = { when: 'immediately' }): CrewMember => ({ name, agentId: 'claude', prompt: '', start })
const after = (member: string): CrewMember['start'] => ({ when: 'after', member })

const T0 = Date.parse('2026-10-04T08:31:00Z')
const iso = (s: number) => new Date(T0 + s * 1000).toISOString()
const member = (p: Partial<RunMember>): RunMember => ({ name: 'lead', agentId: 'claude', start: { when: 'immediately' }, status: 'running', ...p })
const run = (p: Partial<RunInfo> = {}): RunInfo => ({
  id: 'users-api-cca6cdf3',
  crewId: 'users-api',
  name: 'users api',
  goal: 'ship',
  cwd: '/srv/api',
  isolation: 'worktree',
  startedAt: iso(0),
  members: [],
  log: [],
  state: 'running',
  needsInput: 0,
  yolo: false,
  ...p,
})
const session = (p: Partial<SessionInfo>): SessionInfo =>
  ({ id: 's1', name: 'lead', kind: 'server', agentId: 'claude', command: [], cwd: '/w', status: 'running', cols: 80, rows: 24, viewers: 0, createdAt: iso(0), ...p }) as SessionInfo

describe('startWords', () => {
  it('says each rule with its icon, and mutes the one that waits for a person', () => {
    expect(startWords({ when: 'immediately' })).toEqual({ icon: 'i-lucide-zap', text: 'At launch', muted: false })
    expect(startWords(after('lead'))).toEqual({ icon: 'i-lucide-corner-down-right', text: 'After lead is done', muted: false })
    expect(startWords({ when: 'manual' })).toEqual({ icon: 'i-lucide-hand', text: 'When you press Start', muted: true })
  })
})

describe('crewShape', () => {
  it('counts the members and says how they start', () => {
    expect(crewShape([])).toBe('no members')
    expect(crewShape([m('solo')])).toBe('1 · at launch')
    expect(crewShape([m('solo', { when: 'manual' })])).toBe('1 · by hand')
    expect(crewShape([m('a'), m('b'), m('c'), m('d')])).toBe('4 · all at launch')
    expect(crewShape([m('writer'), m('checker', after('writer'))])).toBe('2 · checker after writer')
    expect(crewShape([m('notes'), m('tag', after('notes')), m('publish', after('tag'))])).toBe('3 · a chain')
    expect(crewShape([m('lead'), m('core', after('lead')), m('tests', after('core')), m('docs', { when: 'manual' }), m('review')])).toBe('5 · a chain of 3, 1 by hand, 1 alone')
  })

  it('calls two members starting after the same one a tree', () => {
    expect(crewShape([m('lead'), m('core', after('lead')), m('cli', after('lead')), m('tester', after('cli'))])).toBe('4 · a tree')
    expect(crewShape([m('lead'), m('core', after('lead')), m('cli', after('lead')), m('docs', { when: 'manual' })])).toBe('4 · a tree of 3, 1 by hand')
  })

  it('treats a rule naming a member that is not here as starting on its own', () => {
    expect(crewShape([m('a', after('ghost')), m('b')])).toBe('2 · all at launch')
  })
})

describe('startSentence', () => {
  it('reads the start order as one sentence', () => {
    expect(startSentence([])).toBe('no members yet')
    expect(startSentence([m('solo')])).toBe('solo starts at launch')
    expect(startSentence([m('a'), m('b')])).toBe('all start at launch')
    expect(startSentence([m('lead'), m('core', after('lead')), m('tests', after('core')), m('docs', { when: 'manual' }), m('review')])).toBe(
      'lead → core → tests start in a chain; docs waits for you; review starts at once',
    )
    expect(startSentence([m('lead'), m('core', after('lead')), m('cli', after('lead')), m('tester', after('cli'))])).toBe('lead → core, cli → tester start in order')
    expect(startSentence([m('a', { when: 'manual' }), m('b', { when: 'manual' }), m('c'), m('d')])).toBe('a and b wait for you; c and d start at once')
  })
})

describe('runOutcome', () => {
  it('keeps failed for an error of a member\'s own, not for the stop that cut it off', () => {
    const stopped = run({ stoppedAt: iso(30), members: [member({ status: 'ended' }), member({ name: 'late', status: 'ended', error: STOPPED_ERROR })] })
    expect(runOutcome(stopped, 'stopped')).toBe('stopped')
    const failed = run({ members: [member({ status: 'ended', error: 'exited (exit 1) before its prompt was typed' })] })
    expect(runOutcome(failed, 'finished')).toBe('failed')
    expect(runOutcome(run({ ...failed, stoppedAt: iso(30) }), 'stopped')).toBe('failed')
    expect(runOutcome(run(), 'running')).toBe('running')
    expect(runOutcome(run(), 'needs_input')).toBe('needs you')
    expect(runOutcome(run({ members: [member({ status: 'ended' })] }), 'finished')).toBe('finished')
    expect(ownError({ error: STOPPED_ERROR })).toBe('')
    expect(ownError({ error: 'boom' })).toBe('boom')
  })
})

describe('memberWords', () => {
  it('says what a stop cut off in grey and keeps red for an error of its own', () => {
    const going = run()
    const stopped = run({ stoppedAt: iso(30) })
    expect(memberWords(going, member({}), 'needs_input')).toEqual({ text: 'needs input', tone: 'warning' })
    expect(memberWords(going, member({}), 'running')).toEqual({ text: 'running', tone: 'default' })
    expect(memberWords(going, member({}), 'starting')).toEqual({ text: 'starting…', tone: 'muted' })
    expect(memberWords(going, member({ start: after('lead') }), 'pending').text).toBe('starts after lead is done')
    expect(memberWords(going, member({ start: { when: 'manual' } }), 'pending').text).toBe('waits for Start now')
    expect(memberWords(going, member({}), 'pending').text).toBe('waiting to start')
    expect(memberWords(stopped, member({}), 'pending')).toEqual({ text: 'not started', tone: 'muted' })
    expect(memberWords(stopped, member({ error: STOPPED_ERROR }), 'ended')).toEqual({ text: 'not started', tone: 'muted' })
    expect(memberWords(stopped, member({}), 'ended')).toEqual({ text: 'stopped', tone: 'muted' })
    expect(memberWords(going, member({}), 'ended')).toEqual({ text: 'ended', tone: 'muted' })
    expect(memberWords(stopped, member({ error: 'exited (exit 1)' }), 'ended')).toEqual({ text: 'ended: exited (exit 1)', tone: 'error' })
  })
})

describe('runNote', () => {
  it('names who asks, then a failure, then what a stop cut off, then what changed', () => {
    const asking = run({ members: [member({ sessionId: 's1' })] })
    expect(runNote(asking, [session({ id: 's1', attention: { state: 'needs_input' } })])).toEqual({ text: 'lead asks a question', tone: 'warning' })
    const failed = run({ members: [member({ name: 'tests', status: 'ended', error: 'exited (exit 1) before its prompt was typed' })] })
    expect(runNote(failed, [])).toEqual({ text: 'tests: exited (exit 1) before its prompt was typed', tone: 'error' })
    const cut = run({ stoppedAt: iso(33), members: [member({ status: 'ended' }), member({ name: 'b', status: 'pending' }), member({ name: 'c', status: 'ended', error: STOPPED_ERROR }), member({ name: 'd', status: 'pending' })] })
    expect(runNote(cut, [])).toEqual({ text: '3 never started', tone: 'muted' })
    const waits = run({ members: [member({}), member({ name: 'docs', status: 'pending', start: { when: 'manual' } })] })
    expect(runNote(waits, [])).toEqual({ text: 'docs waits for Start now', tone: 'muted' })
    const changed = run({
      state: 'finished',
      members: [member({ status: 'ended', branch: 'crew/x/lead', diff: { added: 400, removed: 50 } }), member({ name: 'b', status: 'ended', branch: 'crew/x/b', diff: { added: 12, removed: 8 } })],
    })
    expect(runNote(changed, [])).toEqual({ text: '+412 −58 on 2 branches', tone: 'muted' })
    const answered = run({ state: 'finished', members: [member({ status: 'ended' })], log: [{ at: iso(1), type: 'status', message: 'review asks "Trust this folder?": answer it' }, { at: iso(2), type: 'status', message: 'review asks "Continue?": answer it' }] })
    expect(runNote(answered, [])).toEqual({ text: 'answered twice', tone: 'muted' })
    expect(runNote(run({ state: 'finished', members: [member({ status: 'ended' })] }), []).text).toBe('')
  })
})

describe('names and times', () => {
  it('names a run by its crew and start, with its short id', () => {
    expect(shortRunId('users-api-cca6cdf3')).toBe('cca6cdf3')
    expect(shortRunId('cca6cdf3')).toBe('cca6cdf3')
    expect(runWhen(iso(0), T0 + 3_600_000)).toMatch(/^today \d/)
    expect(runWhen(iso(0), T0 + 2 * 86_400_000)).toMatch(/^Oct 4 \d/)
    expect(runWhen(iso(0), T0 + 2 * 86_400_000, false)).toBe('Oct 4')
    expect(runWhen('nope')).toBe('')
    expect(askedCount([{ message: 'lead asks "Trust?": …' }, { message: 'typed lead\'s prompt' }, { message: 'asks "x"' }])).toBe(1)
  })

  it('says how long a run went', () => {
    expect(runTook(run(), true, T0 + 4 * 60_000)).toBe('up 4m')
    expect(runTook(run({ stoppedAt: iso(33) }), false, T0 + 3_600_000)).toBe('33s')
    expect(runTook(run({ state: 'finished', members: [member({ status: 'ended', endedAt: iso(22 * 60) })] }), false, T0 + 3_600_000)).toBe('22m')
  })

  it('draws the last runs as bars, oldest first, and sums them up', () => {
    const runs = [
      run({ id: 'r3', startedAt: iso(300), state: 'needs_input', members: [member({ needsInput: true })] }),
      run({ id: 'r2', startedAt: iso(200), stoppedAt: iso(230), state: 'stopped', members: [member({ status: 'ended' })] }),
      run({ id: 'r1', startedAt: iso(100), state: 'finished', members: [member({ status: 'ended', error: 'exited (exit 2)', endedAt: iso(160) })], log: [{ at: iso(1), type: 'status', message: 'lead asks "x": y' }] }),
      run({ id: 'r0', startedAt: iso(0), state: 'finished', members: [member({ status: 'ended', endedAt: iso(120) })] }),
    ]
    const bars = runBars(runs, [], T0 + 600_000, 3)
    expect(bars.map((b) => b.id)).toEqual(['r1', 'r2', 'r3'])
    expect(bars.map((b) => b.outcome)).toEqual(['failed', 'stopped', 'needs you'])
    expect(bars.map((b) => b.minutes)).toEqual([1, 0.5, 5])
    expect(bars.map((b) => b.asked)).toEqual([1, 0, 0])
    expect(medianMinutes(bars)).toBe(1)
    expect(medianMinutes(bars.slice(0, 2))).toBe(0.8)
    expect(medianMinutes([])).toBe(0)
    expect(barsSummary(bars)).toBe('duration in minutes · median 1m · 1 needed an answer · 1 failed')
    expect(minutesLabel(0.5)).toBe('30s')
    expect(minutesLabel(64)).toBe('1h 4m')
    expect(minutesLabel(120)).toBe('2h')
  })

  it('reads the live-run banner from the members', () => {
    const r = run({ members: [member({ sessionId: 's1' }), member({ name: 'review', sessionId: 's2' }), member({ name: 'docs', status: 'pending', start: { when: 'manual' } })] })
    const line = liveRunLine(r, [session({ id: 's1' }), session({ id: 's2', attention: { state: 'needs_input' } })])
    expect(line.running).toBe(1)
    expect(line.needs).toEqual(['review'])
    expect(line.waits).toEqual(['docs'])
    expect(line.started).toMatch(/\d/)
  })
})

describe('changedFields', () => {
  const saved: CrewInput = { name: 'users api', goal: 'ship', cwd: '/srv/api', where: 'server', isolation: 'worktree', openAfterLaunch: true, members: [m('lead')] }
  it('names what a draft changed', () => {
    expect(changedFields(saved, saved)).toEqual([])
    expect(changedFields({ ...saved, goal: 'ship it' }, saved)).toEqual(['Goal'])
    expect(changedFields({ ...saved, name: 'x', cwd: '/x', yolo: true, members: [m('lead'), m('b')] }, saved)).toEqual(['Name', 'Working directory', 'Each run', 'Members'])
    expect(changedFields(saved, undefined)).toEqual([])
  })
})
