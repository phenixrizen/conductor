import { mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { ensureStableBinary, stableBinaryPath } from '../src/install'

describe('stable binary', () => {
  it('lives under the user\'s data directory per platform', () => {
    expect(stableBinaryPath('linux', '/home/me')).toBe('/home/me/.local/share/conductor/bin/conductor')
    expect(stableBinaryPath('darwin', '/Users/me')).toBe('/Users/me/Library/Application Support/Conductor/bin/conductor')
  })
  it('copies once per version, executable, and again when the version changes', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-install-'))
    const src = join(dir, 'src')
    writeFileSync(src, '#!/bin/sh\necho hi\n')
    const dst = join(dir, 'stable', 'bin', 'conductor')
    expect(ensureStableBinary(src, dst, 'v1')).toBe(true)
    expect(statSync(dst).mode & 0o111).toBe(0o111)
    expect(readFileSync(dst, 'utf8')).toContain('echo hi')
    expect(ensureStableBinary(src, dst, 'v1')).toBe(false)
    writeFileSync(src, '#!/bin/sh\necho hello\n')
    expect(ensureStableBinary(src, dst, 'v2')).toBe(true)
    expect(readFileSync(dst, 'utf8')).toContain('echo hello')
  })
})
