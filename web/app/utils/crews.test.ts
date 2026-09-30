import { describe, expect, it } from 'vitest'
import type { CrewInfo, CrewMember } from '~/composables/useSessions'
import { toCrewInput } from './crews'

const lead: CrewMember = { name: 'lead', agentId: 'claude', prompt: 'Own the plan for $GOAL.', args: ['--model', 'x'], start: { when: 'immediately' } }
const tests: CrewMember = { name: 'tests', agentId: 'shell', prompt: '', start: { when: 'after', member: 'lead' } }

const info: CrewInfo = {
  id: 'api-sweep',
  name: 'API sweep',
  goal: 'ship /v1/users',
  cwd: '/srv/api',
  where: 'server',
  isolation: 'worktree',
  openAfterLaunch: true,
  viewLinkTtlSeconds: 28800,
  members: [lead, tests],
  createdAt: '2026-09-29T10:00:00Z',
  updatedAt: '2026-09-29T11:00:00Z',
}

/** What goes over the wire: undefined fields are dropped as JSON drops them. */
const wire = (v: unknown) => JSON.parse(JSON.stringify(v))

describe('toCrewInput', () => {
  it('leaves out what the server sets, so a listed crew can be saved as it is', () => {
    expect(wire(toCrewInput(info))).toEqual({
      name: 'API sweep',
      goal: 'ship /v1/users',
      cwd: '/srv/api',
      where: 'server',
      isolation: 'worktree',
      openAfterLaunch: true,
      viewLinkTtlSeconds: 28800,
      members: [
        { name: 'lead', agentId: 'claude', prompt: 'Own the plan for $GOAL.', args: ['--model', 'x'], start: { when: 'immediately' } },
        { name: 'tests', agentId: 'shell', prompt: '', start: { when: 'after', member: 'lead' } },
      ],
    })
  })

  it('keeps only the known fields of members and their start', () => {
    const edited = { ...lead, key: 7, start: { when: 'manual' as const, expanded: true } }
    expect(wire(toCrewInput({ ...info, members: [edited] })).members).toEqual([
      { name: 'lead', agentId: 'claude', prompt: 'Own the plan for $GOAL.', args: ['--model', 'x'], start: { when: 'manual' } },
    ])
  })
})
