import { describe, expect, it } from 'vitest'
import type { SessionInfo } from '~/composables/useSessions'
import { HOST_AWAY, rowMenuItems, rowPrompt, sessionLive, stopQuestion } from './sidebarActions'

const on = { open: () => {}, openRun: () => {}, share: () => {}, yard: () => {}, stop: () => {}, forget: () => {} }
const shape = (groups: ReturnType<typeof rowMenuItems>) => groups.map((g) => g.map((i) => `${i.label}${i.kbds ? ` [${i.kbds.join('+')}]` : ''}${i.disabled ? ' (off)' : ''}${i.color ? ` ${i.color}` : ''}`))

describe('rowMenuItems', () => {
  it('offers a session Open, Share, the Yard and Stop; a member its run too', () => {
    expect(shape(rowMenuItems({ kind: 'session', name: 'docs-sweep', live: true }, on))).toEqual([['Open [enter]'], ['Share… [S]', 'Show in the Yard'], ['Stop… [X] error']])
    expect(shape(rowMenuItems({ kind: 'session', name: 'core', live: true, inRun: true }, on))).toEqual([['Open [enter]', 'Open the run [R]'], ['Share… [S]', 'Show in the Yard'], ['Stop… [X] error']])
  })

  it('leaves an exited session nothing to stop or share, and still opens', () => {
    expect(shape(rowMenuItems({ kind: 'session', name: 'old', live: false }, on))).toEqual([['Open [enter]'], ['Share… [S] (off)', 'Show in the Yard'], ['Stop… [X] (off) error']])
  })

  it('offers a run header Open run, Share run and Stop run', () => {
    expect(shape(rowMenuItems({ kind: 'run', name: 'users api', live: true }, on))).toEqual([['Open run [enter]'], ['Share run [S]'], ['Stop run… [X] error']])
    expect(shape(rowMenuItems({ kind: 'run', name: 'users api', live: false }, on))).toEqual([['Open run [enter]'], ['Share run [S] (off)'], ['Stop run… [X] (off) error']])
  })

  it('offers a shared link Open and Forget', () => {
    expect(shape(rowMenuItems({ kind: 'shared', name: 'api-review', live: false }, on))).toEqual([['Open [enter]'], ['Forget']])
  })

  it('wires every item to its handler', () => {
    const calls: string[] = []
    const h = Object.fromEntries(Object.keys(on).map((k) => [k, () => void calls.push(k)])) as unknown as typeof on
    for (const g of rowMenuItems({ kind: 'session', name: 'x', live: true, inRun: true }, h)) for (const i of g) i.onSelect?.(new Event('select'))
    expect(calls).toEqual(['open', 'openRun', 'share', 'yard', 'stop'])
  })
})

describe('stopQuestion and sessionLive', () => {
  it('asks in the row with the words of the design', () => {
    expect(stopQuestion({ kind: 'session', name: 'docs-sweep' })).toEqual({ title: 'Stop docs-sweep?', detail: 'Its terminal closes. Resume brings the conversation back from Exited.' })
    expect(stopQuestion({ kind: 'run', name: 'users api' }).title).toBe('Stop users api?')
    expect(sessionLive('running')).toBe(true)
    expect(sessionLive('starting')).toBe(true)
    expect(sessionLive('exited')).toBe(false)
    expect(sessionLive('host_disconnected')).toBe(false)
  })
})

describe('rowPrompt', () => {
  const session = (status: SessionInfo['status']) => ({ id: 's1', status } as SessionInfo)
  const options = [
    { label: 'Yes', input: 'Yes\r' },
    { label: 'No', input: 'No\r' },
  ]

  it('gives a prompt with choices its buttons and one without a reply field', () => {
    expect(rowPrompt({ session: session('running'), prompt: { message: 'Run the migration on staging?', options } })).toEqual({ message: 'Run the migration on staging?', choices: options, away: false, placeholder: 'Reply…' })
    expect(rowPrompt({ session: session('running'), prompt: { message: 'Which branch should I base it on?', options: [] } })).toEqual({ message: 'Which branch should I base it on?', choices: [], away: false, placeholder: 'Reply…' })
  })

  it('waits, with the words, while the host is away; nothing without a prompt', () => {
    const p = rowPrompt({ session: session('host_disconnected'), prompt: { message: 'Trust this folder?', options } })
    expect(p?.away).toBe(true)
    expect(p?.placeholder).toBe(HOST_AWAY)
    expect(p?.choices).toEqual(options)
    expect(rowPrompt({ session: session('running') })).toBeNull()
  })
})
