import { describe, expect, it } from 'vitest'
import { decodeFile, encodeFileWrite, FrameType, MAX_WRITE_PART, writeParts } from './protocol'

describe('a save from the editor', () => {
  it('goes in parts of at most 32 KiB, one part for an empty file', () => {
    expect(writeParts(0)).toEqual([[0, 0]])
    expect(writeParts(10)).toEqual([[0, 10]])
    expect(writeParts(MAX_WRITE_PART)).toEqual([[0, MAX_WRITE_PART]])
    expect(writeParts(MAX_WRITE_PART + 1)).toEqual([[0, MAX_WRITE_PART], [MAX_WRITE_PART, MAX_WRITE_PART + 1]])
    expect(writeParts(100, 40)).toEqual([[0, 40], [40, 80], [80, 100]])
  })
  it('lays a part out as the FILE frame does', () => {
    const part = new TextEncoder().encode('package api\n')
    const frame = encodeFileWrite({ reqId: 'w1', path: 'users.go', offset: 0, total: part.length, baseSha256: 'ab' }, part)
    expect(frame[0]).toBe(FrameType.FileWrite)
    const back = decodeFile(frame.subarray(1))
    expect(back?.header).toMatchObject({ reqId: 'w1', path: 'users.go', offset: 0, total: part.length, baseSha256: 'ab' })
    expect(new TextDecoder().decode(back!.body)).toBe('package api\n')
    expect(new TextDecoder().decode(frame.subarray(1, 3))).toBe('w1')
  })
})
