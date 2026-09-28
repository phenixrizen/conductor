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
})
