import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from './fixtures'

// A folder with more subfolders than a listing shows (50): the Launch
// dialog's working directory field and its folder picker say how many were
// left out and that typing narrows it (round 14: the owner read a cut list
// as the whole folder, its faint note missed); typing the start of a name
// narrows on the server, so the cut list becomes whole.
test('a big folder says how many more it holds, and typing narrows it to the whole list', async ({ page, state }) => {
  const big = join(state.root, 'bigdir')
  for (let i = 0; i < 80; i++) mkdirSync(join(big, `project-${String(i).padStart(3, '0')}`), { recursive: true })
  await page.goto('/')
  await page.getByRole('button', { name: 'Launch agent' }).first().click()
  const dialog = page.getByRole('dialog')
  const field = dialog.getByRole('combobox').first()
  await field.click()
  await field.fill(big + '/')
  const more = page.locator('[data-dir-more]').first()
  await expect(more).toHaveText('30 more folders here: type the start of a name to narrow the list.', { timeout: 15_000 })
  await expect(page.getByRole('option')).toHaveCount(50)
  // Typing the start of a name: the seven project-07x, project-070 to 079, ten in all, whole.
  await field.fill(big + '/project-07')
  await expect(page.getByRole('option')).toHaveCount(10, { timeout: 15_000 })
  await expect(page.locator('[data-dir-more]')).toHaveCount(0)
  // The folder picker says the same of the big folder.
  await page.keyboard.press('Escape')
  await dialog.locator('[data-dir-input-pick]').first().click()
  const picker = page.getByRole('dialog', { name: 'Where the session runs' })
  const pickerField = picker.getByRole('textbox').first()
  await pickerField.fill(big + '/')
  await expect(picker.locator('[data-dir-more]')).toHaveText('30 more folders here: type the start of a name to narrow the list.', { timeout: 15_000 })
})

// A folder chosen in the launch dialog's working-directory field, from its list (a click, or Enter on the highlighted entry) or
// through the folder picker's "Use this folder", leaves the field focused with the caret at the end of the path and the field
// scrolled to its end, so that the folder just chosen, at the end of a long path, shows and typing goes on from there (#167).
test('a chosen folder leaves the working-directory field focused, its caret and view at the end of the path', async ({ page, state }) => {
  const long = join(state.root, 'a-very-long-folder-name-that-overflows-the-field', 'and-another-long-folder-name-below-it', 'projects')
  for (const name of ['alpha-service', 'beta-service']) mkdirSync(join(long, name), { recursive: true })
  mkdirSync(join(long, 'alpha-service', 'inner'), { recursive: true }) // what the picker lists once it is on alpha-service
  await page.goto('/')
  await page.getByRole('button', { name: 'Launch agent' }).first().click()
  const dialog = page.getByRole('dialog')
  const field = dialog.getByRole('combobox').first()
  // The end state, within a moment: the pick lands in the field a tick after the choice, and its listing comes back after that.
  const atEnd = async (path: string) => {
    await expect(field).toHaveValue(path)
    await expect(field).toBeFocused()
    await expect(async () => {
      const m = await field.evaluate((el: HTMLInputElement) => ({ start: el.selectionStart, len: el.value.length, left: el.scrollLeft, client: el.clientWidth, scroll: el.scrollWidth }))
      expect(m.start, `the caret at the end: ${JSON.stringify(m)}`).toBe(m.len)
      expect(m.left + m.client, `scrolled to the end: ${JSON.stringify(m)}`).toBeGreaterThanOrEqual(m.scroll - 1)
    }).toPass({ timeout: 3_000 })
  }
  // From the list, by a click.
  await field.click()
  await field.fill(long + '/')
  await page.getByRole('option', { name: 'alpha-service' }).click()
  await atEnd(join(long, 'alpha-service'))
  // From the list, by Enter on the highlighted entry.
  await field.fill(long + '/')
  await expect(page.getByRole('option', { name: 'beta-service' })).toBeVisible({ timeout: 15_000 })
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await atEnd(join(long, 'beta-service'))
  // Through the folder picker. The pick's listing reopens the list on the folder's children: closed first, so that Escape takes the
  // list and not the dialog.
  await expect(field).toHaveAttribute('aria-expanded', 'true', { timeout: 15_000 })
  await page.keyboard.press('Escape')
  await expect(field).toHaveAttribute('aria-expanded', 'false')
  await expect(dialog).toBeVisible()
  await dialog.locator('[data-dir-input-pick]').first().click()
  const picker = page.getByRole('dialog', { name: 'Where the session runs' })
  await picker.getByRole('textbox').first().fill(join(long, 'alpha-service'))
  await picker.getByRole('textbox').first().press('Enter') // lists the typed folder at once
  await expect(picker.locator('[data-dir-picker-entry="inner"]')).toBeVisible({ timeout: 15_000 })
  await picker.locator('[data-dir-picker-use]').click()
  await expect(picker).toHaveCount(0)
  await atEnd(join(long, 'alpha-service'))
})
