import { describe, expect, it } from 'vitest'
import { HandshakeReader, parseHandshake } from '../src/handshake'

describe('handshake', () => {
  it('parses the line and ignores the rest', () => {
    expect(parseHandshake('{"listen":"127.0.0.1:43123","publicUrl":"http://localhost:43123","pid":12,"version":"v1","workbenchToken":"abc"}')).toEqual({ listen: '127.0.0.1:43123', publicUrl: 'http://localhost:43123', pid: 12, version: 'v1', workbenchToken: 'abc' })
    expect(parseHandshake('Welcome to Ubuntu')).toBeNull()
    expect(parseHandshake('{"listen":"x"}')).toBeNull()
    expect(parseHandshake('{"listen":"x","publicUrl":"ftp://a","pid":1}')).toBeNull()
  })
  it('reads the handshake out of chunked output with noise before it', () => {
    const r = new HandshakeReader()
    expect(r.feed('motd line\n{"listen":"127.0.0.1:1","pub')).toBeNull()
    const h = r.feed('licUrl":"http://localhost:1","pid":3,"version":"v"}\nlater line\n')
    expect(h?.listen).toBe('127.0.0.1:1')
    expect(r.noise).toEqual(['motd line'])
    expect(r.feed('more')).toBe(h)
  })
  it('bounds what it holds while waiting', () => {
    const r = new HandshakeReader()
    r.feed('x'.repeat(200_000))
    expect(r.feed('\n')).toBeNull()
  })
})

describe('parseHandshake and the old token name', () => {
  it('reads adminToken from a server older than the rename', () => {
    expect(parseHandshake('{"listen":"127.0.0.1:1","publicUrl":"http://localhost:1","pid":1,"version":"v0","adminToken":"abc"}')?.workbenchToken).toBe('abc')
  })
})
