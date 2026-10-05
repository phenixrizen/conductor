/** The line `conductor serve --print-listen` writes once it listens. */
export interface Handshake {
  listen: string
  publicUrl: string
  pid: number
  version: string
  workbenchToken?: string
  tlsListen?: string
}

/** parseHandshake reads one line as the handshake, or returns null for any other line (a shell's greeting on WSL, a warning). */
export function parseHandshake(line: string): Handshake | null {
  const text = line.trim()
  if (!text.startsWith('{') || !text.endsWith('}')) return null
  let v: unknown
  try {
    v = JSON.parse(text)
  } catch {
    return null
  }
  if (!v || typeof v !== 'object') return null
  const o = v as Record<string, unknown>
  if (typeof o.listen !== 'string' || typeof o.publicUrl !== 'string' || typeof o.pid !== 'number') return null
  if (!/^https?:\/\//.test(o.publicUrl)) return null
  const h: Handshake = { listen: o.listen, publicUrl: o.publicUrl, pid: o.pid, version: typeof o.version === 'string' ? o.version : '' }
  // workbenchToken, or adminToken from a server older than the rename.
  const tok = typeof o.workbenchToken === 'string' && o.workbenchToken ? o.workbenchToken : typeof o.adminToken === 'string' ? o.adminToken : ''
  if (tok) h.workbenchToken = tok
  if (typeof o.tlsListen === 'string' && o.tlsListen) h.tlsListen = o.tlsListen
  return h
}

/**
 * HandshakeReader feeds the server's stdout, chunk by chunk, and hands back the handshake once a whole line holds one. Lines before it
 * (noise from a login shell) are kept for the log; at most 64 KiB is held while waiting.
 */
export class HandshakeReader {
  private buffer = ''
  readonly noise: string[] = []
  found: Handshake | null = null

  feed(chunk: string): Handshake | null {
    if (this.found) return this.found
    this.buffer += chunk
    let at: number
    while ((at = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, at)
      this.buffer = this.buffer.slice(at + 1)
      const h = parseHandshake(line)
      if (h) {
        this.found = h
        return h
      }
      if (line.trim()) this.noise.push(line.replace(/\r$/, ''))
    }
    if (this.buffer.length > 65536) this.buffer = this.buffer.slice(-65536)
    return null
  }
}
