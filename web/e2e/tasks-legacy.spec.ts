import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from './fixtures'

// A crew saved before a rule it would break now (#334: a task needing
// another twice, refused as a crew is saved since): the server still reads
// and launches it, so its page does too while it is unchanged. Changed, a
// launch saves it first, so the rule blocks the launch and the save, in
// the server's words.
let crewId = ''

test.afterEach(async ({ api }) => {
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
})

test('an unchanged crew saved before a save-only rule launches; changed, the rule blocks the launch', async ({ page, api, state }) => {
  const body = {
    name: 'e2e legacy',
    goal: 'prove an old crew still launches',
    cwd: '',
    where: 'server',
    isolation: 'worktree',
    openAfterLaunch: false,
    yolo: null,
    members: [{ name: 'builder', agentId: 'worker', prompt: '', start: { when: 'task' } }],
    tasks: [
      { id: 'build', member: 'builder', kind: 'work', instructions: 'Build it.' },
      { id: 'docs', member: 'builder', kind: 'work', instructions: 'Document it.', needs: ['build'] },
    ],
  }
  crewId = (await api.ok<{ crew: { id: string } }>('POST', '/api/crews', body)).crew.id
  // The file as an older server saved it: docs needs build twice.
  const file = join(state.data, 'crews', `${crewId}.json`)
  const saved = JSON.parse(readFileSync(file, 'utf8')) as { tasks: Array<{ id: string; needs?: string[] }> }
  saved.tasks.find((t) => t.id === 'docs')!.needs = ['build', 'build']
  writeFileSync(file, JSON.stringify(saved, null, 2) + '\n')
  expect((await api.crew(crewId)).tasks?.find((t) => t.id === 'docs')?.needs).toEqual(['build', 'build'])

  await page.goto(`/crews/${encodeURIComponent(crewId)}`)
  await expect(page.locator('[data-crew-kind]')).toHaveText('Saved crew')
  const launch = page.locator('[data-launch]')
  await expect(launch).toBeEnabled()
  // The row says what a save would be refused for.
  await expect(page.locator('[data-crew-task]').nth(1).locator('[data-task-error]')).toContainText('needs build twice')

  // Changed: a launch would save it first, and the save is refused.
  await page.getByLabel('Goal').fill('prove an old crew still launches, changed')
  const save = page.locator('[data-unsaved-bar]').getByRole('button', { name: 'Save' })
  await expect(save).toBeDisabled()
  await expect(save).toHaveAttribute('data-save-problem', /needs build twice/)
  await expect(launch).toBeDisabled()
})
