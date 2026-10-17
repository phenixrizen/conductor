/**
 * Whether the launch dialog asks where the agent runs. There are two answers
 * only when the workbench is served from another machine: in the desktop
 * app, or on a workbench opened at this computer's own address, "this
 * server" and "this computer" are the same place, and the question is noise.
 */
export function showsRunsOn(isDesktop: boolean, hostname: string): boolean {
  if (isDesktop) return false
  return !isLoopbackHost(hostname)
}

/** A hostname that names the computer the browser runs on. */
export function isLoopbackHost(hostname: string): boolean {
  const h = hostname.trim().toLowerCase().replace(/^\[|\]$/g, '')
  return h === '' || h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\.\d+\.\d+\.\d+$/.test(h)
}

/**
 * Whether `conductor host` refuses `server` without --insecure: a plain
 * `http://` (or `ws://`) server that is not `localhost` or a loopback address
 * (config.PlainOffMachine on the server side, the host's rule, whose test
 * table launch.test.ts repeats). What passes between the host and such a
 * server, the session's output and its viewers' keys among it, travels in
 * the clear and unverified. The host reads the string as given, so the host
 * part is read raw here, not through the URL parser, which writes LOCALHOST
 * and 127.1 as localhost and 127.0.0.1, names the rule exempts and the host
 * does not: `localhost` alone is this machine by name, and an address is one
 * the host parses (netip.ParseAddr). Any other name is another machine, as
 * it is for the host, which does not resolve it before deciding.
 */
export function plainOffMachine(server: string): boolean {
  const m = /^([a-z][a-z0-9+.-]*):\/\/(?:[^/?#@]*@)?(\[[^\]]*\]|[^/?#:]*)/i.exec(server)
  if (!m) return false
  const scheme = (m[1] ?? '').toLowerCase()
  if (scheme !== 'http' && scheme !== 'ws') return false
  const host = m[2] ?? ''
  if (host === 'localhost') return false
  return !isLoopbackIP(host.startsWith('[') ? host.slice(1, -1) : host)
}

/**
 * Whether h is a loopback address as the host parses one (netip.ParseAddr,
 * then Unmap): four decimal octets without leading zeros, the first 127, or
 * an IPv6 address that is ::1 or 127.x.x.x mapped into IPv6 (the zone of a
 * link-local form set aside, as the host's check sets it aside). Anything
 * else, 127.1 or 127.0.0.01 among it, is no address to the host.
 */
function isLoopbackIP(h: string): boolean {
  const v4 = /^(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})$/.exec(h)
  if (v4) return v4.slice(1).every((o) => Number(o) <= 255) && v4[1] === '127'
  if (!h.includes(':')) return false
  let canonical: string
  try {
    canonical = new URL(`http://[${h.replace(/%.*$/, '')}]/`).hostname.replace(/^\[|\]$/g, '')
  } catch {
    return false
  }
  return canonical === '::1' || /^::ffff:7f[0-9a-f]{2}:[0-9a-f]{1,4}$/.test(canonical)
}
