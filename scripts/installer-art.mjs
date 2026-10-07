// The Windows installer's bitmaps, from scripts/installer-art.html: the sidebar
// of the welcome and finish pages (164 × 314) with the Conductor lockup and the
// "Sponsored by RockSolid Labs" badge at its foot (the sponsor kit's badge, on
// the brand's forest), and the header of the other pages (150 × 57) with the
// lockup on white. Rendered headless in Chromium at 1:1, then written as
// 24-bit BMPs for NSIS with ImageMagick:
//   node scripts/installer-art.mjs
// Inter comes from Google Fonts at render time only; the bitmaps are committed.
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
// Playwright is the web app's dev dependency: resolve it from there wherever this runs.
const { chromium } = createRequire(resolve(here, '../web/package.json'))('@playwright/test')
const out = resolve(here, '../desktop/build')
const tmp = mkdtempSync(join(tmpdir(), 'installer-art-'))
const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 420, height: 400 }, deviceScaleFactor: 1 })
await page.goto(`file://${join(here, 'installer-art.html')}`)
await page.evaluate(() => document.fonts.ready)
await page.waitForTimeout(300)
for (const [id, name, size, bg] of [['sidebar', 'installerSidebar', '164x314', '#263D35'], ['header', 'installerHeader', '150x57', '#ffffff']]) {
  const png = join(tmp, `${name}.png`)
  await page.locator(`#${id}`).screenshot({ path: png, omitBackground: false })
  // BMP3: the plain 24-bit header NSIS reads; no alpha, no compression.
  execFileSync('convert', [png, '-background', bg, '-alpha', 'remove', '-alpha', 'off', '-type', 'TrueColor', '-compress', 'none', `BMP3:${join(out, `${name}.bmp`)}`])
  const got = execFileSync('identify', ['-format', '%wx%h %[bit-depth]', join(out, `${name}.bmp`)]).toString()
  if (!got.startsWith(size)) throw new Error(`${name}.bmp is ${got}, not ${size}`)
  console.log(`${name}.bmp ${got}`)
}
await browser.close()
rmSync(tmp, { recursive: true, force: true })
