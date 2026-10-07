import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// The Windows installer's own artwork (scripts/installer-art.mjs renders it):
// the sidebar of the welcome and finish pages and the header of the others,
// as the plain 24-bit bitmaps NSIS reads, named by electron-builder.yml.
const root = join(__dirname, '..')

function bmp(name: string): { width: number; height: number; bits: number; compression: number } {
  const b = readFileSync(join(root, 'build', name))
  expect(b.subarray(0, 2).toString('ascii')).toBe('BM')
  return { width: b.readInt32LE(18), height: Math.abs(b.readInt32LE(22)), bits: b.readUInt16LE(28), compression: b.readUInt32LE(30) }
}

describe('the installer art', () => {
  it('is a 164 × 314 sidebar and a 150 × 57 header, 24-bit, uncompressed', () => {
    expect(bmp('installerSidebar.bmp')).toEqual({ width: 164, height: 314, bits: 24, compression: 0 })
    expect(bmp('installerHeader.bmp')).toEqual({ width: 150, height: 57, bits: 24, compression: 0 })
  })

  it('is what the installer config names, for the uninstaller too', () => {
    const config = readFileSync(join(root, 'electron-builder.yml'), 'utf8')
    expect(config).toContain('installerSidebar: build/installerSidebar.bmp')
    expect(config).toContain('uninstallerSidebar: build/installerSidebar.bmp')
    expect(config).toContain('installerHeader: build/installerHeader.bmp')
  })
})
