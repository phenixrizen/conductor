import { describe, expect, it } from 'vitest'
import { decodePasteBlob, encodePasteBlob, isPasteBlob, MAX_PASTE_BLOB, PASTE_PREFIX } from './paste'

const sdp = 'v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n' + 'a=candidate:1 1 udp 2130706431 192.168.1.20 7877 typ host\r\n'.repeat(20)

describe('paste blobs', () => {
  it('round-trip, compress, and carry their type', async () => {
    const blob = await encodePasteBlob('offer', sdp)
    expect(blob.startsWith(PASTE_PREFIX)).toBe(true)
    expect(blob.length).toBeLessThan(sdp.length)
    expect(/^[A-Za-z0-9_.-]+$/.test(blob)).toBe(true)
    expect(await decodePasteBlob(` ${blob}\n`, 'offer')).toBe(sdp)
    await expect(decodePasteBlob(blob, 'answer')).rejects.toThrow('this is an offer, not an answer')
    expect(isPasteBlob(blob)).toBe(true)
  })

  it('refuses what is not a blob, in words', async () => {
    await expect(decodePasteBlob('', 'offer')).rejects.toThrow('not a Conductor invite')
    await expect(decodePasteBlob('hello', 'offer')).rejects.toThrow('not a Conductor invite')
    await expect(decodePasteBlob('cpi1.', 'offer')).rejects.toThrow('damaged')
    await expect(decodePasteBlob('cpi1.!!!', 'offer')).rejects.toThrow('damaged')
    await expect(decodePasteBlob('cpi1.AAAA', 'offer')).rejects.toThrow('damaged')
    await expect(decodePasteBlob(PASTE_PREFIX + 'A'.repeat(MAX_PASTE_BLOB), 'offer')).rejects.toThrow('too long')
    expect(isPasteBlob('cpi1.')).toBe(false)
    expect(isPasteBlob('nope')).toBe(false)
  })

  it('reads a blob the server made', async () => {
    // internal/hostagent: EncodePasteBlob("answer", "v=0\r\ns=-\r\n") with Go's flate at best compression.
    const fromGo = 'cpi1.qlYqqSxIVbJSSswrLk8tUtJRKk4pULJSKrM1iCmKySu21QVRSrWAAAAA__8'
    await expect(decodePasteBlob(fromGo, 'answer')).resolves.toBe('v=0\r\ns=-\r\n')
  })
})
