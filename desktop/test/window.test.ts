import { describe, expect, it } from 'vitest'
import { trusted } from '../src/ipc'
import { join } from 'node:path'
import { external, followOrigin, logWindowOptions, sameOrigin } from '../src/window'

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

describe('followOrigin', () => {
  it('moves a window on the old server origin to the new one, on the same page', () => {
    expect(followOrigin('http://localhost:43853/sessions/ab?x=1#y', 'http://localhost:37225')).toBe('http://localhost:37225/sessions/ab?x=1#y')
    expect(followOrigin('http://127.0.0.1:4312/', 'http://127.0.0.1:5000')).toBe('http://127.0.0.1:5000/')
  })
  it('leaves a window already there, the splash, and outside sites alone', () => {
    expect(followOrigin('http://localhost:37225/yard', 'http://localhost:37225')).toBe(null)
    expect(followOrigin('file:///opt/Conductor/resources/static/splash.html', 'http://localhost:37225')).toBe(null)
    expect(followOrigin('https://switchyard.rslabs.net/join/x', 'http://localhost:37225')).toBe(null)
    expect(followOrigin('not a url', 'http://localhost:37225')).toBe(null)
  })
})

describe('the server log window', () => {
  it('has the preload that passes it the log lines, isolated as every window', () => {
    const o = logWindowOptions('/app/dist')
    expect(o.webPreferences?.preload).toBe(join('/app/dist', 'log-preload.js'))
    expect(o.webPreferences?.contextIsolation).toBe(true)
    expect(o.webPreferences?.nodeIntegration).toBe(false)
    expect(o.webPreferences?.sandbox).toBe(true)
  })
})
