import { describe, expect, it } from 'vitest'
import { hostCommand, shellQuote } from './hostCommand'

describe('hostCommand', () => {
  it('renders flags then the argv after --', () => {
    expect(hostCommand({ server: 'https://conductor.acme.dev', token: 'tok', name: 'auth-refactor', argv: ['claude'] })).toBe(
      'conductor host --server https://conductor.acme.dev --token tok --name auth-refactor -- claude',
    )
  })
  it('quotes names and args with spaces or quotes', () => {
    expect(shellQuote("it's")).toBe("'it'\\''s'")
    expect(hostCommand({ server: 'http://x', token: 't', name: 'my run', argv: ['claude', '--model', 'opus 4'] })).toBe(
      "conductor host --server http://x --token t --name 'my run' -- claude --model 'opus 4'",
    )
  })
  it('includes --cwd when given and omits --name when empty', () => {
    expect(hostCommand({ server: 'http://x', token: 't', name: '', argv: ['sh'], cwd: '/srv/app' })).toBe('conductor host --server http://x --token t --cwd /srv/app -- sh')
  })
  it('includes the agent\'s screen pattern as --signal-pattern, single-quoted, before the command', () => {
    expect(hostCommand({ server: 'http://x', token: 't', name: 'aider', argv: ['aider'], cwd: '/srv/app', pattern: '\\(Y\\)es/\\(N\\)o\\s*$' })).toBe(
      "conductor host --server http://x --token t --name aider --cwd /srv/app --signal-pattern '\\(Y\\)es/\\(N\\)o\\s*$' -- aider",
    )
  })
  it('escapes single quotes in the pattern and quotes it even when it needs no quoting', () => {
    expect(hostCommand({ server: 'http://x', token: 't', name: '', argv: ['sh'], pattern: "it's? $" })).toBe("conductor host --server http://x --token t --signal-pattern 'it'\\''s? $' -- sh")
    expect(hostCommand({ server: 'http://x', token: 't', name: '', argv: ['sh'], pattern: 'ready' })).toBe("conductor host --server http://x --token t --signal-pattern 'ready' -- sh")
  })
  it('omits --signal-pattern when there is no pattern', () => {
    expect(hostCommand({ server: 'http://x', token: 't', name: '', argv: ['sh'] })).not.toContain('--signal-pattern')
    expect(hostCommand({ server: 'http://x', token: 't', name: '', argv: ['sh'], pattern: '' })).not.toContain('--signal-pattern')
  })
})
