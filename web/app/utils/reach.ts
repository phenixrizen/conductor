import type { ReachInfo } from '~/composables/useSessions'

/** Whether a URL's host is one only this machine or its network can reach: localhost, a private, link-local or loopback address, a `.local` name. */
export function isPrivateHost(url: string): boolean {
  let host: string
  try {
    host = new URL(url).hostname.toLowerCase()
  } catch {
    return true
  }
  if (host === 'localhost' || host.endsWith('.localhost') || host.endsWith('.local') || host === '') return true
  const v6 = host.startsWith('[') ? host.slice(1, -1) : host.includes(':') ? host : null
  if (v6 !== null) {
    if (v6 === '::1' || v6 === '::') return true
    const first = parseInt(v6.split(':')[0] || '0', 16)
    return (first & 0xfe00) === 0xfc00 || (first & 0xffc0) === 0xfe80 // fc00::/7, fe80::/10
  }
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(host)
  if (!m) return false
  const [a, b] = [Number(m[1]), Number(m[2])]
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254) || a === 0
}

export type LinkReach = { level: 'public' | 'pending' | 'local' | 'unknown'; title: string; text: string }

/**
 * What a freshly created link reaches, from its URL and the server's reach report: `public` for a link on an address outside the network
 * (the server cannot check it from inside; a phone on mobile data can), `pending` while the router port is mapped but no certificate is
 * ready yet (links stay on the network's address until then), `local` for a link that only works on this network, `unknown` without a report.
 */
export function linkReach(url: string, reach: ReachInfo | null | undefined): LinkReach {
  const local = isPrivateHost(url)
  if (!local) {
    return {
      level: 'public',
      title: 'Reachable from outside your network',
      text: reach?.mapped
        ? 'The server cannot check that from inside the network; open the link once from a phone on mobile data.'
        : 'Anyone who can reach this address can open it.',
    }
  }
  if (reach && reach.mode !== 'off' && (reach.mapped || reach.method === 'manual') && reach.tls && !reach.tls.ready) {
    return {
      level: 'pending',
      title: 'Waiting for the certificate',
      text: `Port ${reach.externalPort ?? ''} on ${reach.externalIp ?? 'the public address'} is ready, but links are not public until the certificate is issued; this one works on your network only.`,
    }
  }
  if (!reach) return { level: 'unknown', title: 'Works on your network', text: 'Whoever opens it must reach the server at this address.' }
  let why = 'Anyone outside needs the server reachable: a TLS listener with a certificate and a mapped port, a public address, or a proxy (see Sharing in the README).'
  if (reach.error) why = `${reach.error}. ${why}`
  else if (reach.mode === 'off') why = `Reach is off. ${why}`
  else if (reach.mapped && !reach.tls) why = `Port ${reach.externalPort ?? ''} is mapped on the router, but the server has no TLS listener, so nothing is advertised outside. ${why}`
  return { level: 'local', title: 'Works on your network only', text: why }
}
