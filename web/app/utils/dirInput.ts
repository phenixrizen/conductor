import type { GitCheck, PathGit } from '~/composables/useSessions'

/** How long the picker waits after a keystroke before asking the server. */
export const DIR_DEBOUNCE_MS = 150

/** The prefix sent for what is typed: the text trimmed; empty means the server's default directory. */
export function dirQuery(text: string): string {
  return text.trim()
}

/** The mark beside a listed directory: whether a crew with worktrees could use it. */
export function gitMark(git: PathGit): { label: string; tone: 'success' | 'warning' | 'neutral' } {
  if (git.repo && git.commits) return { label: 'git', tone: 'success' }
  if (git.repo) return { label: 'git, no commit', tone: 'warning' }
  return { label: '', tone: 'neutral' }
}

/** A git check, or the failure of one (`error`: the message is the request's error). */
export type GitCheckView = GitCheck & { error?: boolean }

/**
 * What the crew editor says under the working directory. With worktrees the
 * line explains a launch the server would refuse (`blocks`, for the launch
 * button's tooltip); without them it only informs. The launch's 409 stays
 * the authority: the button is never disabled by this.
 */
export function gitCheckLine(check: GitCheckView | null, isolation: 'none' | 'worktree'): { text: string; tone: 'success' | 'warning' | 'neutral'; blocks: boolean } {
  if (!check) return { text: '', tone: 'neutral', blocks: false }
  if (check.error) return { text: check.message, tone: 'warning', blocks: false }
  if (isolation === 'worktree') {
    if (check.inRepo && check.hasCommit) return { text: `Git repository at ${check.toplevel}: worktrees can be made.`, tone: 'success', blocks: false }
    return { text: `${check.message}. Launch would be refused (not_a_repo).`, tone: 'warning', blocks: true }
  }
  if (check.inRepo) return { text: `Git repository at ${check.toplevel}.`, tone: 'neutral', blocks: false }
  return { text: 'Not a git repository; fine with a shared working directory.', tone: 'neutral', blocks: false }
}
