import dgram from 'node:dgram'
import { networkInterfaces } from 'node:os'

/**
 * The UDP port every WebRTC connection of the server uses (its ICE UDP
 * mux), on Windows and inside WSL alike: the forwarder listens on it on
 * Windows and the server inside the distribution does too.
 */
export const ICE_UDP_PORT = Number(process.env.CONDUCTOR_ICE_UDP_PORT) > 0 ? Number(process.env.CONDUCTOR_ICE_UDP_PORT) : 7877

export interface ForwarderOptions {
  /** The port to listen on, on Windows. */
  port: number
  /** Where packets go: the distribution's address, and the same port. */
  target: { host: string; port: number }
  /** An entry unused for this long is dropped (60 s). */
  idleMs?: number
  /** At most this many remote peers at once (1024); past it the oldest goes. */
  maxEntries?: number
  /** The address to listen on (every IPv4 interface). */
  bind?: string
  log?: (line: string) => void
}

interface Entry {
  socket: dgram.Socket
  remote: { address: string; port: number }
  lastSeen: number
  ready: boolean
  queued: Buffer[]
}

/**
 * UdpForwarder is a NAT in user space, for ICE from a server inside WSL 2 in
 * its default NAT mode: Windows forwards nothing to the distribution on its
 * own, and the Hyper-V NAT in between is one NAT too many for ICE. The app
 * listens on the ICE port on Windows and forwards each remote peer's packets
 * to the distribution's address through a socket of that peer's own, so the
 * server sees one source per peer; what the server answers on that socket
 * goes back to the peer from the listening port, the port the server
 * advertises as its own (CONDUCTOR_ICE_PUBLIC_IP and CONDUCTOR_ICE_UDP_PORT).
 * The server's STUN checks and keepalives run through it like any traffic;
 * an idle peer's entry is dropped after idleMs. Mirrored networking needs
 * none of this and is never required: it changes WSL for every other tool.
 */
export class UdpForwarder {
  private listener: dgram.Socket | null = null
  private entries = new Map<string, Entry>()
  private sweep: NodeJS.Timeout | null = null
  private target: { host: string; port: number }
  forwarded = 0
  returned = 0

  constructor(private o: ForwarderOptions) {
    this.target = { ...o.target }
  }

  /** Where packets go now; a new address (the distribution restarted) takes effect for new entries and the ones still open. */
  retarget(target: { host: string; port: number }): void {
    this.target = { ...target }
  }

  get port(): number {
    return this.o.port
  }

  get size(): number {
    return this.entries.size
  }

  /** Binds the listening port; rejects when it is taken. */
  start(): Promise<void> {
    if (this.listener) return Promise.resolve()
    return new Promise((resolve, reject) => {
      const s = dgram.createSocket({ type: 'udp4', reuseAddr: false })
      const fail = (err: Error) => {
        s.removeAllListeners()
        try {
          s.close()
        } catch {
          /* already closed */
        }
        reject(new Error(`udp forwarder: port ${this.o.port}: ${err.message}`))
      }
      s.once('error', fail)
      s.bind(this.o.port, this.o.bind ?? '0.0.0.0', () => {
        s.removeListener('error', fail)
        s.on('error', (err) => this.o.log?.(`udp forwarder: ${err.message}`))
        s.on('message', (msg, rinfo) => this.inbound(msg, rinfo))
        this.listener = s
        const idle = this.o.idleMs ?? 60_000
        this.sweep = setInterval(() => this.expire(idle), Math.max(250, Math.floor(idle / 2)))
        this.sweep.unref()
        this.o.log?.(`udp forwarder: ${this.o.bind ?? '0.0.0.0'}:${this.o.port} → ${this.target.host}:${this.target.port}`)
        resolve()
      })
    })
  }

  /** Closes the listener and every entry. */
  stop(): void {
    if (this.sweep) clearInterval(this.sweep)
    this.sweep = null
    for (const e of this.entries.values()) this.closeEntry(e)
    this.entries.clear()
    if (this.listener) {
      try {
        this.listener.close()
      } catch {
        /* already closed */
      }
    }
    this.listener = null
  }

  private key(address: string, port: number): string {
    return `${address}:${port}`
  }

  /** A packet from a remote peer: its own socket towards the target carries it. */
  private inbound(msg: Buffer, rinfo: dgram.RemoteInfo): void {
    const k = this.key(rinfo.address, rinfo.port)
    let e = this.entries.get(k)
    if (!e) {
      const max = this.o.maxEntries ?? 1024
      if (this.entries.size >= max) {
        let oldest: [string, Entry] | undefined
        for (const pair of this.entries) if (!oldest || pair[1].lastSeen < oldest[1].lastSeen) oldest = pair
        if (oldest) {
          this.closeEntry(oldest[1])
          this.entries.delete(oldest[0])
        }
      }
      e = this.open(rinfo)
      this.entries.set(k, e)
    }
    e.lastSeen = Date.now()
    this.forwarded++
    if (!e.ready) {
      if (e.queued.length < 64) e.queued.push(Buffer.from(msg))
      return
    }
    e.socket.send(msg, this.target.port, this.target.host)
  }

  /** The socket towards the target for one remote peer; what comes back on it goes to that peer from the listening port. */
  private open(remote: { address: string; port: number }): Entry {
    const socket = dgram.createSocket('udp4')
    const e: Entry = { socket, remote: { address: remote.address, port: remote.port }, lastSeen: Date.now(), ready: false, queued: [] }
    socket.on('error', (err) => this.o.log?.(`udp forwarder: ${remote.address}:${remote.port}: ${err.message}`))
    socket.on('message', (reply) => {
      e.lastSeen = Date.now()
      this.returned++
      this.listener?.send(reply, e.remote.port, e.remote.address)
    })
    socket.bind(0, '0.0.0.0', () => {
      e.ready = true
      for (const q of e.queued) socket.send(q, this.target.port, this.target.host)
      e.queued = []
    })
    return e
  }

  private closeEntry(e: Entry): void {
    try {
      e.socket.close()
    } catch {
      /* already closed */
    }
  }

  private expire(idleMs: number): void {
    const cutoff = Date.now() - idleMs
    for (const [k, e] of this.entries) {
      if (e.lastSeen < cutoff) {
        this.closeEntry(e)
        this.entries.delete(k)
      }
    }
  }
}

type Interfaces = Record<string, Array<{ address: string; family: string | number; internal: boolean }> | undefined>

/**
 * The Windows machine's own IPv4 address on the LAN, the one a forwarder
 * listens on and the server advertises: the first non-internal IPv4 of an
 * interface that is not Hyper-V's (its names start with vEthernet: the WSL
 * and Default Switch adapters) and not a tunnel or loopback. Empty when
 * there is none.
 */
export function windowsLanAddress(ifaces: Interfaces = networkInterfaces() as Interfaces): string {
  const skip = /^(vEthernet|Loopback|Teredo|isatap|Bluetooth)/i
  const candidates: string[] = []
  for (const [name, addrs] of Object.entries(ifaces)) {
    if (!addrs || skip.test(name)) continue
    for (const a of addrs) {
      const v4 = a.family === 'IPv4' || a.family === 4
      if (!v4 || a.internal || a.address.startsWith('169.254.')) continue
      candidates.push(a.address)
    }
  }
  // A private LAN address first, so a VPN's or a public one does not win by accident.
  const priv = candidates.find((ip) => /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(ip))
  return priv ?? candidates[0] ?? ''
}
