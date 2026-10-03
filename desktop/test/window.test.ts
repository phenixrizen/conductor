import { describe, expect, it } from 'vitest'
import { trusted } from '../src/ipc'
import { external, sameOrigin } from '../src/window'

describe('window guards', () => {
  it('keeps navigation on the server\'s origin and opens only http(s) outside', () => {
    expect(sameOrigin('http://127.0.0.1:4312/sessions/x', 'http://127.0.0.1:4312')).toBe(true)
    expect(sameOrigin('http://127.0.0.1:9999/', 'http://127.0.0.1:4312')).toBe(false)
    expect(external('https://github.com/x')).toBe(true)
    expect(external('file:///etc/passwd')).toBe(false)
    expect(external('javascript:alert(1)')).toBe(false)
  })
  it('trusts only the workbench and the app\'s own pages as IPC senders', () => {
    expect(trusted('http://127.0.0.1:4312/agents', 'http://127.0.0.1:4312')).toBe(true)
    expect(trusted('file:///app/static/log.html', 'http://127.0.0.1:4312')).toBe(true)
    expect(trusted('https://evil.example/', 'http://127.0.0.1:4312')).toBe(false)
    expect(trusted('', 'http://127.0.0.1:4312')).toBe(false)
  })
})
