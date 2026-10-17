import { describe, expect, it } from 'vitest'
import { isLoopbackHost, plainOffMachine, showsRunsOn } from './launch'

describe('showsRunsOn', () => {
  it('never asks in the desktop app', () => {
    expect(showsRunsOn(true, 'conductor.example.net')).toBe(false)
    expect(showsRunsOn(true, 'localhost')).toBe(false)
  })
  it('never asks on a workbench opened at this computer', () => {
    for (const h of ['localhost', 'LOCALHOST', 'app.localhost', '127.0.0.1', '127.1.2.3', '::1', '[::1]', '']) {
      expect(showsRunsOn(false, h), h).toBe(false)
    }
  })
  it('asks when the server is another machine, a LAN one too', () => {
    for (const h of ['conductor.example.net', '192.168.1.57', 'switchyard.rslabs.net', 'box.local']) {
      expect(showsRunsOn(false, h), h).toBe(true)
    }
  })
  it('isLoopbackHost', () => {
    expect(isLoopbackHost('127.0.0.1')).toBe(true)
    expect(isLoopbackHost('10.0.0.1')).toBe(false)
  })
})

// The host's rule for a server it refuses without --insecure (config.PlainOffMachine).
describe('plainOffMachine', () => {
  it('is a plain http or ws server that is not localhost or a loopback address', () => {
    for (const s of ['http://192.168.1.57:8099', 'http://conductor.test:8099', 'HTTP://box.local', 'ws://10.0.0.2:8099', 'http://[fe80::1]:8099', 'http://app.localhost:8099']) {
      expect(plainOffMachine(s), s).toBe(true)
    }
  })
  it('is not localhost or a loopback address, whatever the scheme', () => {
    for (const s of ['http://localhost:8099', 'http://LOCALHOST', 'http://127.0.0.1:8099', 'http://127.1.2.3', 'http://[::1]:8099', 'ws://[::1]:8099', 'http://[::ffff:127.0.0.1]:8099']) {
      expect(plainOffMachine(s), s).toBe(false)
    }
  })
  it('is never https, wss, or no URL at all', () => {
    for (const s of ['https://conductor.acme.dev', 'wss://192.168.1.57:8099', 'https://192.168.1.57', '', 'not a url', 'conductor.test:8099']) {
      expect(plainOffMachine(s), s).toBe(false)
    }
  })
})
