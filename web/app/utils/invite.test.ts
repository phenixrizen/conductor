import { describe, expect, it } from 'vitest'
import { inviteRoute, joinServer, parseInvite, wsBaseOf } from './invite'

const tok = 'MGzwgDXWv1sbiMNn3L9Lk7ejmTGy6M2u-5WkRjcyvdA'

describe('parseInvite', () => {
  it('reads the host, an optional path, the token and the http flag for loopback', () => {
    expect(parseInvite(`conductor://switchyard.example.net/join/${tok}`)).toEqual({ server: 'https://switchyard.example.net', token: tok })
    expect(parseInvite(`conductor://switchyard.example.net:8443/join/${tok}`)).toEqual({ server: 'https://switchyard.example.net:8443', token: tok })
    expect(parseInvite(`conductor://host.example/conductor/join/${tok}`)).toEqual({ server: 'https://host.example/conductor', token: tok })
    expect(parseInvite(`conductor://127.0.0.1:18424/join/${tok}?http=1`)).toEqual({ server: 'http://127.0.0.1:18424', token: tok })
    expect(parseInvite(`  conductor://localhost:8080/join/${tok}?http=1 `)).toEqual({ server: 'http://localhost:8080', token: tok })
  })

  it('refuses plain http off loopback, bad tokens, bad hosts and other schemes', () => {
    expect(parseInvite(`conductor://switchyard.example.net/join/${tok}?http=1`)).toBeNull()
    expect(parseInvite('conductor://switchyard.example.net/join/short')).toBeNull()
    expect(parseInvite(`conductor://bad host/join/${tok}`)).toBeNull()
    expect(parseInvite(`https://switchyard.example.net/join/${tok}`)).toBeNull()
    expect(parseInvite(`conductor://switchyard.example.net/sessions/${tok}`)).toBeNull()
    expect(parseInvite('')).toBeNull()
  })
})

describe('joinServer', () => {
  it('takes https origins and loopback http, and nothing else', () => {
    expect(joinServer('https://switchyard.example.net')).toBe('https://switchyard.example.net')
    expect(joinServer('https://switchyard.example.net:8443/')).toBe('https://switchyard.example.net:8443')
    expect(joinServer('https://host.example/conductor/')).toBe('https://host.example/conductor')
    expect(joinServer('http://127.0.0.1:18424')).toBe('http://127.0.0.1:18424')
    expect(joinServer('http://localhost:3000')).toBe('http://localhost:3000')
    expect(joinServer('http://switchyard.example.net')).toBe('')
    expect(joinServer('https://user:pw@switchyard.example.net')).toBe('')
    expect(joinServer('https://switchyard.example.net/?x=1')).toBe('')
    expect(joinServer('https://switchyard.example.net/a/../b')).toBe('')
    expect(joinServer('ftp://x')).toBe('')
    expect(joinServer('nope')).toBe('')
    expect(joinServer('')).toBe('')
  })

  it('turns a server base into its WebSocket base', () => {
    expect(wsBaseOf('https://switchyard.example.net')).toBe('wss://switchyard.example.net')
    expect(wsBaseOf('http://127.0.0.1:1')).toBe('ws://127.0.0.1:1')
  })
})

describe('inviteRoute', () => {
  it('is the join page for an invite the desktop app hands over, as the app itself would load it', () => {
    expect(inviteRoute({ server: 'https://switchyard.example.net', token: tok })).toBe(`/join/${tok}?server=https%3A%2F%2Fswitchyard.example.net`)
    expect(inviteRoute({ server: 'http://127.0.0.1:18424', token: tok })).toBe(`/join/${tok}?server=http%3A%2F%2F127.0.0.1%3A18424`)
    expect(inviteRoute({ server: 'https://host.example/conductor', token: tok })).toBe(`/join/${tok}?server=https%3A%2F%2Fhost.example%2Fconductor`)
  })

  it('routes every invite parseInvite takes, the server as the join page reads it (lower-case host, no default port)', () => {
    const canonical = `/join/${tok}?server=https%3A%2F%2Fswitchyard.example.net`
    for (const text of [
      `conductor://switchyard.example.net/join/${tok}`,
      `conductor://Switchyard.Example.NET/join/${tok}`,
      `conductor://switchyard.example.net:443/join/${tok}`,
    ]) {
      const inv = parseInvite(text)
      expect(inv, text).not.toBeNull()
      expect(inviteRoute(inv!), text).toBe(canonical)
    }
    expect(inviteRoute(parseInvite(`conductor://LocalHost:80/join/${tok}?http=1`)!)).toBe(`/join/${tok}?server=http%3A%2F%2Flocalhost`)
    expect(inviteRoute({ server: 'https://switchyard.example.net/', token: tok })).toBe(canonical)
  })

  it('is nothing for what a join page would not take', () => {
    expect(inviteRoute({ server: 'http://switchyard.example.net', token: tok })).toBe('')
    expect(inviteRoute({ server: 'https://a.example/../b', token: tok })).toBe('')
    expect(inviteRoute({ server: 'https://switchyard.example.net', token: 'short' })).toBe('')
    expect(inviteRoute({ server: 'https://switchyard.example.net', token: '../../settings?x=aaaaaaaaaaaaaaaa' })).toBe('')
    expect(inviteRoute({ server: 42, token: tok })).toBe('')
    expect(inviteRoute({})).toBe('')
  })
})
