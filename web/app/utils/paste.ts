/**
 * Paste invites: a viewer's offer and a session's answer as blobs people
 * send through any messenger, with no server between the two once the data
 * channel is open. A blob is `cpi1.` and the base64url of the deflated JSON
 * `{type, sdp}` with every ICE candidate gathered (no trickle), the same
 * format as the server's (internal/hostagent/paste.go).
 */

export const PASTE_PREFIX = 'cpi1.'
/** A blob as pasted is at most this long. */
export const MAX_PASTE_BLOB = 64 << 10

const B64 = /^[A-Za-z0-9_-]+$/

function toBase64Url(bytes: Uint8Array): string {
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function fromBase64Url(text: string): Uint8Array {
  const padded = text.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (text.length % 4)) % 4)
  const bin = atob(padded)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

async function pipe(bytes: Uint8Array, stream: CompressionStream | DecompressionStream): Promise<Uint8Array> {
  const out = new Blob([bytes as BlobPart]).stream().pipeThrough(stream)
  return new Uint8Array(await new Response(out).arrayBuffer())
}

/** encodePasteBlob packs an SDP of the type into a blob. */
export async function encodePasteBlob(type: 'offer' | 'answer', sdp: string): Promise<string> {
  const raw = new TextEncoder().encode(JSON.stringify({ type, sdp }))
  const packed = await pipe(raw, new CompressionStream('deflate-raw'))
  return PASTE_PREFIX + toBase64Url(packed)
}

/** decodePasteBlob unpacks a blob of the type wanted; it throws, in words, for anything else. */
export async function decodePasteBlob(blob: string, want: 'offer' | 'answer'): Promise<string> {
  const text = blob.trim()
  if (text.length > MAX_PASTE_BLOB) throw new Error('the invite is too long')
  if (!text.startsWith(PASTE_PREFIX)) throw new Error(`not a Conductor invite (it starts with ${PASTE_PREFIX})`)
  const body = text.slice(PASTE_PREFIX.length)
  if (!body || !B64.test(body)) throw new Error('the invite is damaged')
  let parsed: { type?: string; sdp?: string }
  try {
    const raw = await pipe(fromBase64Url(body), new DecompressionStream('deflate-raw'))
    parsed = JSON.parse(new TextDecoder().decode(raw)) as { type?: string; sdp?: string }
  } catch {
    throw new Error('the invite is damaged')
  }
  if (!parsed.sdp || typeof parsed.sdp !== 'string') throw new Error('the invite is damaged')
  if (parsed.type !== want) throw new Error(`this is an ${parsed.type ?? 'unknown blob'}, not an ${want}`)
  return parsed.sdp
}

/** isPasteBlob says whether text looks like a blob, before it is decoded. */
export function isPasteBlob(text: string): boolean {
  const t = text.trim()
  return t.startsWith(PASTE_PREFIX) && t.length > PASTE_PREFIX.length && t.length <= MAX_PASTE_BLOB
}
