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
