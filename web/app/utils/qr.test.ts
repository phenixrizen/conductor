import { describe, expect, it } from 'vitest'
import { encodeQr, penaltyOf, qrCapacity, qrSvg } from './qr'
import { fixtures } from './qr.fixtures'

const rowsOf = (modules: boolean[][]) => modules.map((row) => row.map((m) => (m ? '1' : '0')).join(''))

describe('the QR encoder (#144)', () => {
  it('fits a text into the smallest version from 1 to 10 and gives up past 213 bytes', () => {
    expect([1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map(qrCapacity)).toEqual([14, 26, 42, 62, 84, 106, 122, 152, 180, 213])
    expect(encodeQr('')?.version).toBe(1)
    expect(encodeQr('x'.repeat(14))?.version).toBe(1)
    expect(encodeQr('x'.repeat(15))?.version).toBe(2)
    expect(encodeQr('x'.repeat(213))?.version).toBe(10)
    expect(encodeQr('x'.repeat(214))).toBeNull()
    expect(encodeQr('é'.repeat(106))?.version, 'bytes, not characters').toBe(10)
    expect(encodeQr('é'.repeat(107)), 'bytes, not characters').toBeNull()
  })

  for (const f of fixtures) {
    it(`draws qrencode's code for a ${new TextEncoder().encode(f.text).length}-byte text (version ${f.version})`, () => {
      const code = encodeQr(f.text)!
      expect(code.version).toBe(f.version)
      expect(code.size).toBe(f.rows.length)
      // Under the mask qrencode chose, the whole code is qrencode's: data, error correction, placement, format and version bits.
      const masks = [0, 1, 2, 3, 4, 5, 6, 7]
      const theirs = masks.filter((m) => rowsOf(encodeQr(f.text, m)!.modules).join('\n') === f.rows.join('\n'))
      expect(theirs, "qrencode's mask").toHaveLength(1)
      // The mask chosen here is one of the lowest penalty by the standard's rules; qrencode scores finder-like patterns its own way
      // and picks another for the shortest text, which reads the same.
      const scores = masks.map((m) => penaltyOf(encodeQr(f.text, m)!.modules))
      expect(scores[code.mask]).toBe(Math.min(...scores))
      if (f.version > 2) expect(code.mask).toBe(theirs[0])
    })
  }

  it('draws one square per dark module inside a quiet zone of four', () => {
    const text = 'https://c.example/j/ab'
    const code = encodeQr(text)!
    const svg = qrSvg(text)!
    const dark = code.modules.flat().filter(Boolean).length
    expect(svg.path.match(/h1v1h-1z/g)).toHaveLength(dark)
    expect(svg.size).toBe(code.size + 8)
    expect(svg.viewBox).toBe(`0 0 ${code.size + 8} ${code.size + 8}`)
    expect(svg.path.startsWith('M4,4h1v1h-1z'), 'the first module of the finder, four in').toBe(true)
    expect(qrSvg('x'.repeat(300))).toBeNull()
  })
})
