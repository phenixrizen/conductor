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
 * `http://` (or `ws://`) server that is not localhost or a loopback address
 * (config.PlainOffMachine on the server side, the host's rule). What passes
 * between the host and such a server, the session's output and its viewers'
 * keys among it, travels in the clear and unverified. Any name but
 * `localhost` counts as another machine, as it does for the host, which does
 * not resolve it before deciding.
 */
export function plainOffMachine(server: string): boolean {
  let u: URL
  try {
    u = new URL(server)
  } catch {
    return false
  }
  if (u.protocol !== 'http:' && u.protocol !== 'ws:') return false
  const h = u.hostname.toLowerCase().replace(/^\[|\]$/g, '')
  if (h === 'localhost') return false
  return !isLoopbackAddress(h)
}

/**
 * An IP address of the loopback range: 127.0.0.0/8, ::1, or 127.x.x.x mapped
 * into IPv6 (::ffff:127.0.0.1, which the URL parser writes ::ffff:7f00:1).
 */
function isLoopbackAddress(h: string): boolean {
  if (/^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(h)) return true
  if (h === '::1' || /^(0{1,4}:){7}0{0,3}1$/.test(h)) return true
  return /^::ffff:(127\.\d{1,3}\.\d{1,3}\.\d{1,3}|7f[0-9a-f]{2}:[0-9a-f]{1,4})$/.test(h)
}
