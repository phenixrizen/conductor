import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const desktop = join(__dirname, '..')

describe('packaging', () => {
  it('ships the licence, the notice and the third-party notices beside the app', () => {
    const yml = readFileSync(join(desktop, 'electron-builder.yml'), 'utf8').replace(/\r\n/g, '\n')
    for (const f of ['LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES']) {
      expect(yml).toContain(`  - from: ../${f}\n    to: ${f}\n`)
      expect(existsSync(join(desktop, '..', f))).toBe(true)
    }
    // Chromium's notices, which electron-builder leaves out of a macOS app on its own.
    expect(yml).toContain('  - from: node_modules/electron/dist/LICENSES.chromium.html\n    to: LICENSES.chromium.html\n')
    expect(existsSync(join(desktop, 'node_modules/electron/dist/LICENSES.chromium.html'))).toBe(true)
    // The notices name the app's own runtime, as scripts/notices.py writes them.
    const notices = readFileSync(join(desktop, '..', 'THIRD_PARTY_NOTICES'), 'utf8')
    expect(notices).toContain('The desktop app: Electron and its packages')
    expect(notices).toMatch(/\n {2}electron-updater \d/)
  })
})
