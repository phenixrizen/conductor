import { expect, test } from './fixtures'

// Whether this Chromium draws WebGL2. Chromium 1117 headless draws it with
// SwiftShader, no flags needed, when it has no X display to reach: CI's
// runner reports true, and so does WSL with DISPLAY unset. A DISPLAY naming
// an X server that is not running (WSL's, often) makes ANGLE fail, and the
// terminals fall back to the DOM renderer. The test records which, and
// tiles.spec.ts holds the terminals to it. Whichever it is, a spec reads
// what a terminal shows some other way than its DOM text, which a WebGL
// screen (a canvas) does not have: the files the program writes, the API,
// the page's data-* state.
test('records whether WebGL2 is available to the tiles', async ({ page }, testInfo) => {
  await page.goto('/agents')
  const webgl2 = await page.evaluate(() => !!document.createElement('canvas').getContext('webgl2'))
  testInfo.annotations.push({ type: 'webgl2', description: String(webgl2) })
  console.log(`webgl2 available: ${webgl2}`)
  expect(typeof webgl2).toBe('boolean')
})
