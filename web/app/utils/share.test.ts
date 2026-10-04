import { describe, expect, it } from 'vitest'
import { hostOf, shareReach } from './share'

const offline = { mode: 'off' as const, mapped: false }

describe('shareReach', () => {
  it('a link minted at the switchyard works from anywhere', () => {
    const r = shareReach({ url: 'https://switchyard.example.net/join/abc', remote: true }, offline)
    expect(r.kind).toBe('remote')
    expect(r.title).toBe('Works from anywhere')
    expect(r.text).toContain('switchyard.example.net')
  })
  it('a link made here while a switchyard is configured says why the session is not there, then what it reaches', () => {
    const r = shareReach({ url: 'http://127.0.0.1:8080/join/abc', rendezvous: { server: 'https://switchyard.example.net', error: 'connection refused' } }, offline)
    expect(r.kind).toBe('unpublished')
    expect(r.title).toBe('Not published to the switchyard')
    expect(r.text).toContain('switchyard.example.net: connection refused')
    expect(r.text).toContain('works where this machine is reachable')
    expect(r.level).toBe('local')
  })
  it('a local link keeps the reach report, and a crew link says it is made here', () => {
    const r = shareReach({ url: 'http://192.168.1.5:8080/join/abc' }, offline)
    expect(r.kind).toBe('local')
    expect(r.title).toBe('Works on your network only')
    expect(shareReach({ url: 'http://192.168.1.5:8080/join/abc' }, offline, true).text).toMatch(/^Crew links are made here, not at the switchyard\./)
    expect(shareReach({ url: 'https://home.example.net/join/abc' }, null).title).toBe('Reachable from outside your network')
  })
  it('hostOf', () => {
    expect(hostOf('https://switchyard.example.net/join/x')).toBe('switchyard.example.net')
    expect(hostOf('nonsense')).toBe('nonsense')
  })
})
