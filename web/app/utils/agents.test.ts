import { describe, expect, it } from 'vitest'
import type { AgentInfo } from '~/composables/useSessions'
import { agentItem, identity, isAvailable, misidentified, notInstalled, notInstalledTitle, serverAgents } from './agents'

const agent = (over: Partial<AgentInfo>): AgentInfo => ({ id: 'a', name: 'Agent', command: ['a'], allowArgs: true, ...over })

describe('agent availability', () => {
  it('treats a missing flag (an older server) as available', () => {
    expect(isAvailable({ available: undefined })).toBe(true)
    expect(isAvailable({ available: true })).toBe(true)
    expect(isAvailable({ available: false })).toBe(false)
  })

  it('keeps only the installed agents for the server tab', () => {
    expect(serverAgents([agent({ id: 'x', available: true }), agent({ id: 'y', available: false }), agent({ id: 'z' })]).map((a) => a.id)).toEqual(['x', 'z'])
  })

  it('names the host, or the server when it is unknown', () => {
    expect(notInstalled('build-1')).toBe('Not installed on build-1')
    expect(notInstalled('')).toBe('Not installed on the server')
  })

  it('marks an unavailable agent in a select without disabling it, and shows the bundled icon or the generic one', () => {
    expect(agentItem(agent({ id: 'claude', name: 'Claude Code', icon: 'i-lucide-sparkles', available: false }), 'build-1')).toEqual({ label: 'Claude Code · not installed on build-1', value: 'claude', icon: 'i-lucide-circle-off' })
    expect(agentItem(agent({ id: 'claude', name: 'Claude Code', icon: 'i-lucide-sparkles', available: true }), 'build-1')).toEqual({ label: 'Claude Code', value: 'claude', icon: 'i-lucide-sparkles' })
    expect(agentItem(agent({ id: 'sh', name: 'Shell', icon: 'i-lucide-not-bundled' }), 'build-1')).toEqual({ label: 'Shell', value: 'sh', icon: 'i-lucide-bot' })
  })

  it('keeps the host name as the server gives it', () => {
    expect(agentItem(agent({ name: 'Cursor CLI', available: false }), 'Build-PC').label).toBe('Cursor CLI · not installed on Build-PC')
    expect(agentItem(agent({ name: 'Cursor CLI', available: false }), '').label).toBe('Cursor CLI · not installed on the server')
  })

  it('says how the server judged it: a name on its PATH, any program of that name counting, or a path', () => {
    expect(notInstalledTitle('cursor-agent')).toBe("No program named cursor-agent is on the server's PATH (any program of that name counts as installed)")
    expect(notInstalledTitle('/opt/agent/bin/agent')).toBe('/opt/agent/bin/agent was not found on the server')
  })
})

describe('agent identity', () => {
  const probed = (over: Partial<NonNullable<AgentInfo['identity']>>) => ({ command: ['goose'], identity: { ran: true, identified: false, verified: false, name: 'Goose', ...over } })
  it('names an identified agent with its version', () => {
    const r = identity({ command: ['claude'], identity: { ran: true, identified: true, verified: true, name: 'Claude Code', version: '2.1.287' } }, 'build-1')
    expect(r).toMatchObject({ state: 'ok', label: 'Claude Code 2.1.287' })
    expect(r.title).toContain('claude on build-1 is Claude Code 2.1.287')
  })
  it('names an impostor by what it printed', () => {
    const r = identity(probed({ impostor: true, output: 'goose version: v3.22.1' }), 'build-1')
    expect(r.state).toBe('impostor')
    expect(r.label).toBe('Not Goose')
    expect(r.title).toContain('"goose version: v3.22.1"')
    expect(misidentified(probed({ impostor: true }).identity)).toBe(true)
  })
  it('refuses nothing on an unverified pattern, and says pending, failed or unprobed', () => {
    expect(identity(probed({ output: 'goose 1.0.0-beta' }), '').state).toBe('unidentified')
    expect(misidentified(probed({ output: 'goose 1.0.0-beta' }).identity)).toBe(false)
    expect(misidentified(probed({ verified: true }).identity)).toBe(true)
    expect(identity(probed({ ran: false, pending: true }), '').state).toBe('pending')
    expect(identity(probed({ ran: false, error: 'timeout' }), '').state).toBe('failed')
    expect(identity({ command: ['sh'] }, '').state).toBe('unprobed')
  })
  it('marks a misidentified agent in a select', () => {
    expect(agentItem(agent({ id: 'goose', name: 'Goose', available: true, identity: { ran: true, identified: false, impostor: true, verified: false, name: 'Goose' } }), 'build-1').label).toBe('Goose · not Goose on build-1')
  })
})
