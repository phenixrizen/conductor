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

// The host's rule for a server it refuses without --insecure: the table of
// TestPlainOffMachine (internal/config/config_test.go), kept in step with it,
// then the spellings the URL parser would have normalized away.
describe('plainOffMachine', () => {
  it('matches the host (config.PlainOffMachine) case for case', () => {
    const table: Record<string, boolean> = {
      'http://yard.example': true,
      'ws://yard.example/ws/host': true,
      'HTTP://yard.example': true,
      'http://192.168.1.10:8080': true,
      'http://0.0.0.0:8080': true,
      'http://127.1:8080': true,
      'http://localhost.yard.example': true,
      'http://127.0.0.1.yard.example': true,
      'http://[fe80::1]:8080': true,
      'https://yard.example': false,
      'wss://yard.example': false,
      'http://localhost:8080': false,
      'ws://LocalHost:8080': true,
      'http://127.0.0.1:8080': false,
      'http://127.255.0.1': false,
      'http://[::1]:8080': false,
      'http://[::ffff:127.0.0.1]:8080': false,
      'http://user:pw@localhost:8080': false,
      'ftp://yard.example': false,
      '::not a url': false,
    }
    for (const [server, want] of Object.entries(table)) expect(plainOffMachine(server), server).toBe(want)
  })
  it('reads the host as the host does, not as the URL parser rewrites it', () => {
    // The parser would write these as localhost and 127.0.0.1, which the host, reading them as given, refuses without the flag.
    for (const s of ['http://LOCALHOST:8080', 'http://localhost.:8080', 'http://127.0.0.01:8080', 'http://127.000.0.1', 'http://app.localhost:8099', 'http://conductor.test:8099']) {
      expect(plainOffMachine(s), s).toBe(true)
    }
    for (const s of ['http://[0:0:0:0:0:0:0:1]:8080', 'ws://[::1]:8099', 'http://[::1%25lo]:8080', 'http://127.0.0.1', '']) {
      expect(plainOffMachine(s), s).toBe(false)
    }
  })
})
