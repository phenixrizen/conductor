import { expect, test } from './fixtures'

// A crew with tasks (#334, row 4): the editor's Coordination select turns the
// crew managed; the tasks table takes a task per thing a member delivers; a
// cycle in the needs blocks Save with its words; a check's quoted argument
// is one array element; the crew is saved and cannot be launched yet.
let crewId = ''
test.afterEach(async ({ api }) => {
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
})

test('a crew with tasks: a cycle blocks Save, a quoted argument is one element, Launch waits for a later Conductor', async ({ page, api }) => {
  await page.goto('/crews')
  await page.locator('[data-new-crew]').click()
  await expect(page).toHaveURL(/\/crews\/new$/)
  await expect(page.locator('[data-crew-kind]')).toHaveText('New crew')
  await page.getByLabel('Crew name').fill('e2e tasks')
  await page.getByLabel('Goal').fill('ship the thing')

  await page.locator('[data-crew-coordination]').click()
  await page.getByRole('option', { name: /by tasks/ }).click()
  await expect(page.locator('[data-crew-tasks-section]')).toBeVisible()
  await expect(page.locator('[data-member-start]')).toHaveCount(0)

  // A second member, the reviewer; the first is the default "lead".
  await page.locator('[data-add-member]').click()
  const members = page.locator('[data-crew-member]')
  await expect(members).toHaveCount(2)
  await members.nth(1).getByLabel(/^Name of/).fill('reviewer')

  // Two tasks: lead's build, reviewer's review of it.
  await page.locator('[data-add-task]').click()
  await page.locator('[data-add-task]').click()
  const tasks = page.locator('[data-crew-task]')
  await expect(tasks).toHaveCount(2)
  await tasks.nth(0).getByLabel(/^Id of/).fill('build')
  await tasks.nth(0).getByLabel(/^Instructions for/).fill('Build it.')
  await tasks.nth(1).getByLabel(/^Id of/).fill('review')
  await tasks.nth(1).locator('[data-task-member]').click()
  await page.getByRole('option', { name: 'reviewer' }).click()
  await tasks.nth(1).locator('[data-task-kind]').click()
  await page.getByRole('option', { name: 'review' }).click()
  await tasks.nth(1).locator('[data-task-needs]').click()
  await page.getByRole('option', { name: 'build' }).click()
  await page.keyboard.press('Escape')
  await tasks.nth(1).getByLabel(/^Instructions for/).fill('Review it.')

  const bar = page.locator('[data-unsaved-bar]')
  const save = bar.getByRole('button', { name: 'Save' })
  await expect(save).not.toHaveAttribute('data-save-problem', /./)
  await expect(save).toBeEnabled()

  // The lead's name cleared and typed again: its task follows the name.
  const leadName = members.nth(0).getByLabel(/^Name of/)
  await leadName.fill('')
  await expect(save).toBeDisabled()
  await leadName.fill('lead')
  await expect(tasks.nth(0).locator('[data-task-member]')).toContainText('lead')
  await expect(save).not.toHaveAttribute('data-save-problem', /./)

  // A cycle: build needs review, which needs build. Save is blocked, and
  // the row says why.
  await tasks.nth(0).locator('[data-task-needs]').click()
  await page.getByRole('option', { name: 'review' }).click()
  await page.keyboard.press('Escape')
  await expect(tasks.nth(0).locator('[data-task-error]')).toContainText('cycle')
  await expect(save).toBeDisabled()
  await expect(save).toHaveAttribute('data-save-problem', /cycle/)
  await tasks.nth(0).locator('[data-task-needs]').click()
  await page.getByRole('option', { name: 'review' }).click()
  await page.keyboard.press('Escape')
  await expect(tasks.nth(0).locator('[data-task-error]')).toHaveCount(0)

  // A check whose argument holds a space: one chip, one array element.
  await tasks.nth(0).locator('[data-task-checks]').click()
  await page.locator('[data-add-check]').click()
  await page.getByLabel('Id of check 1').fill('unit')
  // The chip input: the quoted argument becomes one chip on Enter.
  const argv = page.getByLabel('Command of check 1').locator('input')
  await argv.fill('sh -c "echo hi there"')
  await argv.press('Enter')
  await expect(page.getByLabel('Command of check 1').getByText('echo hi there', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(save).toBeEnabled()
  await save.click()
  await expect(page.locator('[data-crew-kind]')).toHaveText('Saved crew')
  crewId = page.url().split('/crews/')[1]!
  const crew = await api.crew(crewId)
  expect(crew.tasks?.map((t) => [t.id, t.member, t.kind, t.needs ?? []])).toEqual([
    ['build', 'lead', 'work', []],
    ['review', 'reviewer', 'review', ['build']],
  ])
  expect(crew.tasks?.[0]?.checks).toEqual([{ id: 'unit', argv: ['sh', '-c', 'echo hi there'] }])
  expect(crew.members.map((m) => m.start?.when)).toEqual(['task', 'task'])

  // Not launched yet: the page says why, and the server refuses too.
  await expect(page.locator('[data-launch]')).toBeDisabled()
  const refused = await api.launchCrewRaw(crewId)
  expect(refused.status).toBe(400)
  expect(refused.body.error.message).toContain('cannot be launched yet')
})
