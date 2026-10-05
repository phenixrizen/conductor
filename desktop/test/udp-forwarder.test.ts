import dgram from 'node:dgram'
import { afterEach, describe, expect, it } from 'vitest'
import { UdpForwarder, windowsLanAddress } from '../src/udp-forwarder'

function udp(): Promise<{ socket: dgram.Socket; port: number }> {
  return new Promise((resolve) => {
    const s = dgram.createSocket('udp4')
    s.bind(0, '127.0.0.1', () => resolve({ socket: s, port: s.address().port }))
  })
}

function freePort(): Promise<number> {
  return new Promise((resolve) => {
    const s = dgram.createSocket('udp4')
    s.bind(0, '127.0.0.1', () => {
      const port = s.address().port
      s.close(() => resolve(port))
    })
  })
}

function next(s: dgram.Socket, ms = 3000): Promise<{ msg: Buffer; from: dgram.RemoteInfo }> {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('no packet')), ms)
    s.once('message', (msg, from) => {
      clearTimeout(t)
      resolve({ msg, from })
    })
  })
}

const open: Array<{ close: () => void }> = []
afterEach(() => {
  for (const o of open.splice(0)) o.close()
})

describe('UdpForwarder', () => {
  it('carries each remote peer to the target on a socket of its own and returns the replies from the listening port', async () => {
    const target = await udp()
    open.push(target.socket)
    const port = await freePort()
    const fwd = new UdpForwarder({ port, target: { host: '127.0.0.1', port: target.port }, bind: '127.0.0.1' })
    open.push({ close: () => fwd.stop() })
    await fwd.start()

    const peerA = await udp()
    const peerB = await udp()
    open.push(peerA.socket, peerB.socket)
    peerA.socket.send('hello from A', port, '127.0.0.1')
    const a = await next(target.socket)
    expect(a.msg.toString()).toBe('hello from A')
    expect(a.from.port).not.toBe(peerA.port) // the entry's socket, not the peer's
    peerB.socket.send('hello from B', port, '127.0.0.1')
    const b = await next(target.socket)
    expect(b.msg.toString()).toBe('hello from B')
    expect(b.from.port).not.toBe(a.from.port) // one socket per peer
    expect(fwd.size).toBe(2)

    // The target answers on the entry's socket: the peer hears it from the listening port.
    target.socket.send('reply to A', a.from.port, a.from.address)
    const ra = await next(peerA.socket)
    expect(ra.msg.toString()).toBe('reply to A')
    expect(ra.from.port).toBe(port)
    target.socket.send('reply to B', b.from.port, b.from.address)
    expect((await next(peerB.socket)).msg.toString()).toBe('reply to B')
    expect(fwd.forwarded).toBe(2)
    expect(fwd.returned).toBe(2)

    // A second packet from A keeps its socket.
    peerA.socket.send('again', port, '127.0.0.1')
    expect((await next(target.socket)).from.port).toBe(a.from.port)
  })

  it('drops idle entries, and follows a new target', async () => {
    const target = await udp()
    open.push(target.socket)
    const port = await freePort()
    const fwd = new UdpForwarder({ port, target: { host: '127.0.0.1', port: target.port }, bind: '127.0.0.1', idleMs: 300 })
    open.push({ close: () => fwd.stop() })
    await fwd.start()
    const peer = await udp()
    open.push(peer.socket)
    peer.socket.send('x', port, '127.0.0.1')
    await next(target.socket)
    expect(fwd.size).toBe(1)
    await new Promise((r) => setTimeout(r, 700))
    expect(fwd.size).toBe(0)

    const target2 = await udp()
    open.push(target2.socket)
    fwd.retarget({ host: '127.0.0.1', port: target2.port })
    peer.socket.send('y', port, '127.0.0.1')
    expect((await next(target2.socket)).msg.toString()).toBe('y')
  })

  it('refuses a port in use, and stops cleanly', async () => {
    const taken = await udp()
    open.push(taken.socket)
    const fwd = new UdpForwarder({ port: taken.port, target: { host: '127.0.0.1', port: 1 }, bind: '127.0.0.1' })
    await expect(fwd.start()).rejects.toThrow(/port \d+/)
    fwd.stop()
    const port = await freePort()
    const ok = new UdpForwarder({ port, target: { host: '127.0.0.1', port: 1 }, bind: '127.0.0.1' })
    await ok.start()
    await ok.start() // idempotent
    ok.stop()
    ok.stop()
    const again = new UdpForwarder({ port, target: { host: '127.0.0.1', port: 1 }, bind: '127.0.0.1' })
    await again.start() // the port is free again
    again.stop()
  })
})

describe('windowsLanAddress', () => {
  it('picks the LAN IPv4, never Hyper-V, loopback, link-local or a public one when a private one exists', () => {
    const ifaces = {
      'Loopback Pseudo-Interface 1': [{ address: '127.0.0.1', family: 'IPv4', internal: true }],
      'vEthernet (WSL (Hyper-V firewall))': [{ address: '172.26.16.1', family: 'IPv4', internal: false }],
      'vEthernet (Default Switch)': [{ address: '172.20.0.1', family: 'IPv4', internal: false }],
      'Wi-Fi': [
        { address: 'fe80::1', family: 'IPv6', internal: false },
        { address: '169.254.3.4', family: 'IPv4', internal: false },
        { address: '192.168.1.127', family: 'IPv4', internal: false },
      ],
      'Ethernet': [{ address: '203.0.113.9', family: 'IPv4', internal: false }],
    }
    expect(windowsLanAddress(ifaces)).toBe('192.168.1.127')
    expect(windowsLanAddress({ Ethernet: [{ address: '203.0.113.9', family: 4, internal: false }] })).toBe('203.0.113.9')
    expect(windowsLanAddress({ 'vEthernet (WSL)': [{ address: '172.26.16.1', family: 'IPv4', internal: false }] })).toBe('')
    expect(windowsLanAddress({})).toBe('')
  })
})
