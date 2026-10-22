import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The editor and the terminal side by side as well as stacked (#309): the
// switch in the editor's tab strip, the bar between them draggable either
// way and moved by the arrow keys, the terminal keeping a minimum width
// side by side, the choice and each layout's split kept per browser and
// surviving a reload; a narrow window stacks whatever is kept.
test('the editor goes beside the terminal and back, the split drags and steps, and both outlive a reload', async ({ page, api, state }) => {
  const cwd = join(state.root, 'editor-layout')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'alpha\nbeta\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e editor layout', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] [data-rail-item="explorer"]').click()
    await page.locator(`[data-file-node="${cwd}/notes.txt"]`).click()
    const column = page.locator('[data-editor-column]')
    const area = page.locator('[data-editor-area]')
    const slot = page.locator('[data-terminal-slot]')
    const handle = page.locator('[data-editor-split]')
    await expect(area).toHaveAttribute('data-editor-area', 'open')
    await expect(column).toHaveAttribute('data-editor-layout', 'stacked')
    await expect(handle).toHaveAttribute('aria-orientation', 'horizontal')
    // Stacked: the editor above the terminal.
    let a = (await area.boundingBox())!
    let t = (await slot.boundingBox())!
    expect(a.y + a.height).toBeLessThanOrEqual(t.y + 1)
    // The switch puts the editor beside the terminal, on the left; the bar turns vertical.
    await area.locator('[data-editor-layout-switch]').click()
    await expect(column).toHaveAttribute('data-editor-layout', 'side')
    await expect(handle).toHaveAttribute('aria-orientation', 'vertical')
    a = (await area.boundingBox())!
    t = (await slot.boundingBox())!
    expect(a.x + a.width).toBeLessThanOrEqual(t.x + 1)
    expect(Math.abs(a.y - t.y)).toBeLessThan(2)
    // A drag to the right widens the editor; dragged past the terminal's minimum, the terminal keeps its width.
    const h = (await handle.boundingBox())!
    const before = a.width
    await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2)
    await page.mouse.down()
    await page.mouse.move(h.x + 120, h.y + h.height / 2, { steps: 6 })
    await page.mouse.up()
    a = (await area.boundingBox())!
    expect(a.width).toBeGreaterThan(before + 40)
    const c = (await column.boundingBox())!
    await page.mouse.move(a.x + a.width + 4, h.y + h.height / 2)
    await page.mouse.down()
    await page.mouse.move(c.x + c.width - 10, h.y + h.height / 2, { steps: 8 })
    await page.mouse.up()
    t = (await slot.boundingBox())!
    expect(t.width).toBeGreaterThanOrEqual(319)
    // The arrow keys move the bar: Left gives the editor less, End as far as it goes.
    await handle.focus()
    const wide = (await area.boundingBox())!.width
    await page.keyboard.press('ArrowLeft')
    await page.keyboard.press('ArrowLeft')
    expect((await area.boundingBox())!.width).toBeLessThan(wide)
    await page.keyboard.press('Home')
    const narrow = (await area.boundingBox())!.width
    await page.keyboard.press('End')
    expect((await area.boundingBox())!.width).toBeGreaterThan(narrow)
    // Kept per browser: the layout and its split come back after a reload; the stacked split is its own.
    const kept = (await area.boundingBox())!.width
    await page.reload()
    await page.locator('[data-inspector] [data-rail-item="explorer"]').click()
    await page.locator(`[data-file-node="${cwd}/notes.txt"]`).click()
    await expect(column).toHaveAttribute('data-editor-layout', 'side')
    expect(Math.abs((await area.boundingBox())!.width - kept)).toBeLessThan(4)
    // Back to stacked with the same switch; the stacked split is the one kept for it.
    await area.locator('[data-editor-layout-switch]').click()
    await expect(column).toHaveAttribute('data-editor-layout', 'stacked')
    await expect(handle).toHaveAttribute('aria-orientation', 'horizontal')
    await area.locator('[data-editor-layout-switch]').click()
    await expect(column).toHaveAttribute('data-editor-layout', 'side')
    // A narrow window stacks the two whatever is kept, and the switch is not offered.
    await page.setViewportSize({ width: 900, height: 900 })
    await expect(column).toHaveAttribute('data-editor-layout', 'stacked')
    await expect(area.locator('[data-editor-layout-switch]')).toHaveCount(0)
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect(column).toHaveAttribute('data-editor-layout', 'side')
    // A wide window whose column is too narrow for both panes (beside wide panels, as a join page's Files and Chat leave it)
    // stacks too, and offers no switch: the column's own width decides, not the viewport's (Codex's round on #309).
    await column.evaluate((el) => ((el as HTMLElement).style.maxWidth = '400px'))
    await expect(column).toHaveAttribute('data-editor-layout', 'stacked')
    await expect(area.locator('[data-editor-layout-switch]')).toHaveCount(0)
    await column.evaluate((el) => ((el as HTMLElement).style.maxWidth = ''))
    await expect(column).toHaveAttribute('data-editor-layout', 'side')
  } finally {
    await api.stopSession(s.id)
  }
})
