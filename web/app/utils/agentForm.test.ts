import { describe, expect, it } from 'vitest'
import type { AgentInfo } from '~/composables/useSessions'
import { AGENT_ID_PATTERN, MASK, agentPayload, commandOf, formErrors, formFromAgent, signalOut, siteError } from './agentForm'
import { slugId } from './argv'

function counter() {
  let n = 0
  return () => n++
}

const keyed: AgentInfo = {
  id: 'keyed',
  name: 'Keyed',
  command: ['aider', '--model', 'x'],
  allowArgs: true,
  env: { REGION: MASK, API_KEY: MASK },
  envPassthrough: ['HTTP_PROXY'],
  cwd: '/srv',
  icon: 'i-lucide-sparkles',
  adapter: 'claude',
  signal: { kind: 'hook', toolEvents: true },
}

describe('formFromAgent and agentPayload', () => {
  it('round-trips an agent: stored values stay masked, passthrough names stay names', () => {
    const f = formFromAgent(keyed, counter())
    expect(f.env.map((r) => [r.key, r.masked])).toEqual([
      ['API_KEY', true],
      ['REGION', true],
      ['HTTP_PROXY', false],
    ])
    expect(agentPayload(f, keyed)).toEqual({
      id: 'keyed',
      name: 'Keyed',
      description: undefined,
      command: ['aider', '--model', 'x'],
      allowArgs: true,
      env: { API_KEY: MASK, REGION: MASK },
      envPassthrough: ['HTTP_PROXY'],
      cwd: '/srv',
      icon: 'i-lucide-sparkles',
      adapter: 'claude',
      signal: { kind: 'hook', toolEvents: true },
    })
  })
  it('sends a value typed in a new row as it is, next to the masked ones', () => {
    const f = formFromAgent(keyed, counter())
    f.env.push({ uid: 99, key: 'TOKEN', value: 's3cret', masked: false })
    expect(agentPayload(f, keyed).env).toEqual({ API_KEY: MASK, REGION: MASK, TOKEN: 's3cret' })
  })
  it('leaves a removed stored value out of the payload', () => {
    const f = formFromAgent(keyed, counter())
    f.env = f.env.filter((r) => r.key !== 'API_KEY')
    expect(agentPayload(f, keyed)).toMatchObject({ env: { REGION: MASK }, envPassthrough: ['HTTP_PROXY'] })
    expect(agentPayload(f, keyed).env).not.toHaveProperty('API_KEY')
  })
  it('starts a new agent empty, ringing the bell', () => {
    const f = formFromAgent(undefined, counter())
    expect(f).toMatchObject({ name: '', id: '', command: [], pendingCommand: '', env: [], allowArgs: true, signal: 'bell' })
    expect(agentPayload({ ...f, name: 'X', id: 'x', command: ['x'] }).signal).toBeUndefined()
  })
  it('counts what is typed in the command field and not yet an argument', () => {
    const f = { ...formFromAgent(undefined, counter()), command: ['aider'], pendingCommand: '--model "gpt 5"' }
    expect(commandOf(f)).toEqual(['aider', '--model', 'gpt 5'])
    expect(agentPayload({ ...f, name: 'A', id: 'a' }).command).toEqual(['aider', '--model', 'gpt 5'])
  })
})

describe('formErrors', () => {
  const ok = () => ({ ...formFromAgent(undefined, counter()), name: 'Aider', id: 'aider', command: ['aider'] })
  it('accepts a complete form', () => {
    expect(formErrors(ok())).toEqual({})
  })
  it('catches *** typed as a new value: the server would read it as the stored one', () => {
    const f = ok()
    f.env.push({ uid: 1, key: 'API_KEY', value: MASK, masked: false })
    expect(formErrors(f).env).toBe('API_KEY: *** stands for a stored value; type the real value')
  })
  it('refuses an unclosed quote in the command', () => {
    expect(formErrors({ ...ok(), pendingCommand: '--model "gpt' }).command).toBe('Close the quote, or remove it')
  })
  it("wants a command, a name and an ID of the server's shape", () => {
    expect(formErrors({ ...ok(), command: [] }).command).toBe('Add the command to run')
    expect(formErrors({ ...ok(), name: ' ' }).name).toBe('Give the agent a name')
    expect(formErrors({ ...ok(), id: 'Bad ID' }).id).toBe('Use lowercase letters, digits and dashes, up to 32')
    expect(formErrors({ ...ok(), id: 'a'.repeat(33) }).id).toBeDefined()
    expect(formErrors({ ...ok(), id: '' }).id).toBe('An ID is required')
  })
  it('names a variable listed twice, and a value without a name', () => {
    const f = ok()
    f.env.push({ uid: 1, key: 'A', value: 'x', masked: false }, { uid: 2, key: 'A', value: 'y', masked: false })
    expect(formErrors(f).env).toBe('A is listed twice')
    expect(formErrors({ ...ok(), env: [{ uid: 3, key: ' ', value: 'v', masked: false }] }).env).toBe('Give every variable a name')
  })
})

describe('signalOut', () => {
  it('leaves the bell out unless the agent had a signal', () => {
    expect(signalOut('bell', '')).toBeUndefined()
    expect(signalOut('bell', '', { kind: 'pattern', pattern: 'x' })).toEqual({ kind: 'bell' })
  })
  it('keeps the tool-events flag the form has no control for', () => {
    expect(signalOut('pattern', '^> $', { kind: 'hook', toolEvents: true })).toEqual({ kind: 'pattern', pattern: '^> $', toolEvents: true })
    expect(signalOut('none', '', { kind: 'hook', toolEvents: true })).toEqual({ kind: 'none', toolEvents: true })
  })
})

describe('AGENT_ID_PATTERN and slugId', () => {
  it('a suggested ID is empty or one the server takes, never ending in a dash', () => {
    for (const name of ['Claude (opus)', 'a'.repeat(31) + ' b', '--x--', 'My  Tool -- v2', '!!!', 'x'.repeat(40)]) {
      const id = slugId(name)
      expect(id === '' || AGENT_ID_PATTERN.test(id)).toBe(true)
      expect(id.endsWith('-')).toBe(false)
    }
  })
})

describe('the site field', () => {
  it('round-trips the site, and sends none when it is empty', () => {
    const f = formFromAgent({ ...keyed, site: 'https://example.com/keyed' }, counter())
    expect(f.site).toBe('https://example.com/keyed')
    expect(agentPayload(f, keyed).site).toBe('https://example.com/keyed')
    expect(agentPayload({ ...f, site: '  ' }, keyed).site).toBeUndefined()
  })
  it('accepts an empty or https site and refuses the rest, as the server does', () => {
    expect(siteError('')).toBe('')
    expect(siteError('https://example.com/x')).toBe('')
    for (const bad of ['http://example.com', 'example.com', 'https://user:pw@example.com', 'javascript:alert(1)', `https://example.com/${'a'.repeat(200)}`]) {
      expect(siteError(bad)).toBe('An https:// address, or nothing')
    }
    const f = formFromAgent(keyed, counter())
    expect(formErrors({ ...f, site: 'http://example.com' }).site).toBe('An https:// address, or nothing')
  })
})
