import { describe, expect, it } from 'vitest'
import { formedCrew } from './activity'
import type { ActivityEntry } from '~/utils/protocol'

const at = '2026-10-03T10:00:00Z'

describe('formedCrew', () => {
  it('offers the run of a link entry that asked to be opened', () => {
    const e: ActivityEntry = { at, type: 'link', message: 'formed crew review team (open)', url: 'https://203.0.113.5/runs/team-1a2b3c4d' }
    expect(formedCrew('lead', e)).toEqual({ title: 'lead formed crew review team', path: '/runs/team-1a2b3c4d' })
  })

  it('ignores a crew formed without open, other link entries and other types', () => {
    expect(formedCrew('s', { at, type: 'link', message: 'formed crew review team', url: 'http://h/runs/r1' })).toBeNull()
    expect(formedCrew('s', { at, type: 'link', message: 'link created: for the PR (view)' })).toBeNull()
    expect(formedCrew('s', { at, type: 'link', message: 'published at the rendezvous', url: 'http://h/sessions/s1' })).toBeNull()
    expect(formedCrew('s', { at, type: 'status', message: 'formed crew x (open)', url: 'http://h/runs/r1' })).toBeNull()
  })

  it('refuses a URL that is not a run page', () => {
    expect(formedCrew('s', { at, type: 'link', message: 'formed crew x (open)', url: 'http://h/sessions/s1' })).toBeNull()
    expect(formedCrew('s', { at, type: 'link', message: 'formed crew x (open)', url: 'http://h/runs/../admin' })).toBeNull()
    expect(formedCrew('s', { at, type: 'link', message: 'formed crew x (open)', url: 'not a url' })).toBeNull()
  })
})
