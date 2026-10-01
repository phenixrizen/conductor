import { describe, expect, it } from 'vitest'
import { DIR_DEBOUNCE_MS, dirQuery, gitCheckLine, gitMark } from './dirInput'

describe('dirInput', () => {
  it('sends the text trimmed, and empty for the server default', () => {
    expect(dirQuery('  /srv/work/a ')).toBe('/srv/work/a')
    expect(dirQuery('   ')).toBe('')
  })

  it('marks a repository with a commit, warns on one without, says nothing otherwise', () => {
    expect(gitMark({ repo: true, commits: true })).toEqual({ label: 'git', tone: 'success' })
    expect(gitMark({ repo: true, commits: false })).toEqual({ label: 'git, no commit', tone: 'warning' })
    expect(gitMark({ repo: false, commits: false })).toEqual({ label: '', tone: 'neutral' })
  })

  it('explains the launch with worktrees, and only informs without them', () => {
    const ok = { inRepo: true, toplevel: '/srv/work', hasCommit: true, message: 'a git repository with a commit: a crew with worktrees can launch here' }
    const fresh = { inRepo: true, toplevel: '/srv/work', hasCommit: false, message: 'the working directory is a git repository without a commit: a worktree needs one to branch from' }
    const plain = { inRepo: false, hasCommit: false, message: 'the working directory is not in a git repository' }
    expect(gitCheckLine(null, 'worktree')).toEqual({ text: '', tone: 'neutral', blocks: false })
    expect(gitCheckLine(ok, 'worktree')).toEqual({ text: 'Git repository at /srv/work: worktrees can be made.', tone: 'success', blocks: false })
    expect(gitCheckLine(fresh, 'worktree')).toEqual({ text: `${fresh.message}. Launch would be refused (not_a_repo).`, tone: 'warning', blocks: true })
    expect(gitCheckLine(plain, 'worktree')).toEqual({ text: `${plain.message}. Launch would be refused (not_a_repo).`, tone: 'warning', blocks: true })
    expect(gitCheckLine(plain, 'none')).toEqual({ text: 'Not a git repository; fine with a shared working directory.', tone: 'neutral', blocks: false })
    expect(gitCheckLine(ok, 'none')).toEqual({ text: 'Git repository at /srv/work.', tone: 'neutral', blocks: false })
    expect(gitCheckLine({ ...plain, error: true, message: 'working directory is outside the allowed roots' }, 'none')).toEqual({ text: 'working directory is outside the allowed roots', tone: 'warning', blocks: false })
  })

  it('debounces for longer than a keystroke and shorter than a pause', () => {
    expect(DIR_DEBOUNCE_MS).toBeGreaterThanOrEqual(100)
    expect(DIR_DEBOUNCE_MS).toBeLessThanOrEqual(300)
  })
})
