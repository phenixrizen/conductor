import { describe, expect, it } from 'vitest'
import { isPrivateHost, linkReach } from './reach'
import type { ReachInfo } from '~/composables/useSessions'

describe('isPrivateHost', () => {
  it('knows the private, loopback and link-local ranges and local names', () => {
    for (const u of ['http://localhost:8080/join/x', 'http://127.0.0.1:8080/', 'http://10.1.2.3/', 'http://172.16.0.1/', 'http://172.31.255.255/', 'http://192.168.1.20:8080/', 'http://169.254.1.1/', 'http://[::1]:8080/', 'http://[fe80::1]/', 'http://[fd12::1]/', 'http://router.local/', 'http://box.localhost/', 'not a url'])
      expect(isPrivateHost(u), u).toBe(true)
    for (const u of ['https://203.0.113.9/join/x', 'https://172.32.0.1/', 'https://team.example.net/join/x', 'https://[2001:db8::1]/'])
      expect(isPrivateHost(u), u).toBe(false)
  })
})

const base: ReachInfo = { mode: 'auto', mapped: false }

describe('linkReach', () => {
  it('calls a public address reachable, with the phone check when the server mapped it', () => {
    const r = linkReach('https://203.0.113.9/join/x', { ...base, mapped: true, method: 'upnp', tls: { mode: 'acme', ready: true } })
    expect(r.level).toBe('public')
    expect(r.text).toContain('mobile data')
    expect(linkReach('https://team.example.net/join/x', base).text).not.toContain('mobile data')
  })
  it('says the certificate is pending while the port is mapped and TLS is not ready', () => {
    const r = linkReach('http://192.168.1.20:8080/join/x', { ...base, mapped: true, method: 'upnp', externalIp: '203.0.113.9', externalPort: 443, tls: { mode: 'acme', ready: false } })
    expect(r.level).toBe('pending')
    expect(r.text).toContain('203.0.113.9')
  })
  it('explains a local link: off, an error, or no TLS listener', () => {
    expect(linkReach('http://192.168.1.20:8080/join/x', { ...base, mode: 'off' }).text).toMatch(/^Reach is off/)
    expect(linkReach('http://192.168.1.20:8080/join/x', { ...base, error: 'no gateway mapped the port' }).text).toMatch(/^no gateway mapped the port/)
    expect(linkReach('http://192.168.1.20:8080/join/x', { ...base, mapped: true, externalPort: 443 }).text).toContain('no TLS listener')
    expect(linkReach('http://localhost:8080/join/x', null).level).toBe('unknown')
  })
})
