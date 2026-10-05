import { expect, logged, member, test } from './fixtures'

// Codex's hidden title thread (by-hand item 6 of round 4): the stub reports
// a title-thread turn with a higher thread id before the user's turn. The
// member never shows as needing input or done early, and the session id
// captured is the user's thread, resumable.
test('a Codex member keeps the user thread as its session and reports no false state for the title thread', async ({ api }) => {
  const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
    name: 'e2e title thread',
    goal: 'title',
    cwd: '',
    where: 'server',
    isolation: 'none',
    openAfterLaunch: false,
    yolo: true,
    members: [{ name: 'solo', agentId: 'codex', prompt: 'say hello', start: { when: 'immediately' } }],
  })
  const run = await api.launchCrew(crew.crew.id)
  try {
    let sawNeedsInput = false
    let doneCount = 0
    let lastMessage = ''
    await expect
      .poll(async () => {
        const r = await api.run(run.id)
        const m = member(r, 'solo')
        if (m.needsInput) sawNeedsInput = true
        if (!m.sessionId) return false
        const s = await api.session(m.sessionId)
        if (s.attention?.state === 'needs_input') sawNeedsInput = true
        if (s.attention?.state === 'done' && s.attention.message !== lastMessage) {
          doneCount++
          lastMessage = s.attention.message ?? ''
        }
        return logged(r, "typed solo's prompt") && (s.attention?.message ?? '').includes('got: say hello')
      }, { timeout: 60_000, intervals: [250] })
      .toBe(true)
    expect(sawNeedsInput, 'no needs_input from launch to done').toBe(false)
    expect(doneCount, 'one done, the user turn\'s').toBe(1)
    const solo = await api.session(member(await api.run(run.id), 'solo').sessionId ?? '')
    expect(solo.agentSession?.id ?? '').toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/)
    expect(solo.agentSession?.id?.startsWith('ffffffff-'), 'not the title thread').toBe(false)
    expect(solo.agentSession?.resumable).toBe(true)
    expect(solo.agentSession?.source).toBe('hook')
    expect(solo.command ?? []).not.toContain('--session-id')
  } finally {
    await api.stopRun(run.id)
    await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
  }
})
