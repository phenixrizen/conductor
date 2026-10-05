import { describe, expect, it } from 'vitest'
import { isLoopbackHost, showsRunsOn } from './launch'

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
