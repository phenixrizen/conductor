import { expect, test } from './fixtures'

// Whether this Chromium draws WebGL2: Chromium 1117 headless on a runner
// without a GPU does not, whatever flags it is given (tried: --use-gl=angle
// with --use-angle=swiftshader, --enable-unsafe-swiftshader, --use-gl=egl),
// so the tiles are measured with the DOM renderer here and the WebGL look
// stays a by-hand check (docs/features.md). The test records which, and
// tiles.spec.ts holds the terminals to it.
test('records whether WebGL2 is available to the tiles', async ({ page }, testInfo) => {
  await page.goto('/agents')
  const webgl2 = await page.evaluate(() => !!document.createElement('canvas').getContext('webgl2'))
  testInfo.annotations.push({ type: 'webgl2', description: String(webgl2) })
  console.log(`webgl2 available: ${webgl2}`)
  expect(typeof webgl2).toBe('boolean')
})
