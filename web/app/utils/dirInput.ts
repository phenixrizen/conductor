import type { GitCheck, PathGit } from '~/composables/useSessions'

/** How long the picker waits after a keystroke before asking the server. */
export const DIR_DEBOUNCE_MS = 150

/** The prefix sent for what is typed: the text trimmed; empty means the server's default directory. */
export function dirQuery(text: string): string {
  return text.trim()
}

/**
 * The entries still shown while the listing of `text` is on its way: those
 * the listing could hold, directories directly under what is typed (none for
 * an empty text, the server's default listed anew). An entry the text has
 * moved past, or moved back above (shortened across a slash), can never be
 * highlighted, so Enter cannot put it in place of what was typed.
 */
export function matchingEntries<T extends { path: string }>(entries: T[], text: string): T[] {
  const q = dirQuery(text)
  return q ? entries.filter((e) => e.path.startsWith(q) && !e.path.slice(q.length).includes('/')) : []
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
 * The git check's message when a crew with worktrees can launch: the
 * server's msgCanWorktree (internal/crew/worktree.go, whose test reads this
 * line). Any other message is the launch's refusal, whatever inRepo and
 * hasCommit say (a .conductor that is a symbolic link is refused in a
 * repository with a commit).
 */
export const GIT_CAN_WORKTREE = 'a git repository with a commit: a crew with worktrees can launch here'

/**
 * What the crew editor says under the working directory. With worktrees the
 * line is the server's verdict in its words and explains a launch it would
 * refuse (`blocks`, for the launch button's tooltip); without a message (an
 * older server) inRepo and hasCommit decide. Without worktrees it only
 * informs. The launch's answer stays the authority: the button is never
 * disabled by this.
 */
export function gitCheckLine(check: GitCheckView | null, isolation: 'none' | 'worktree'): { text: string; tone: 'success' | 'warning' | 'neutral'; blocks: boolean } {
  if (!check) return { text: '', tone: 'neutral', blocks: false }
  if (check.error) return { text: check.message, tone: 'warning', blocks: false }
  if (isolation === 'worktree') {
    const canLaunch = check.message ? check.message === GIT_CAN_WORKTREE : check.inRepo && check.hasCommit
    if (canLaunch) return { text: check.message || 'A git repository with a commit: worktrees can be made.', tone: 'success', blocks: false }
    return { text: `${check.message || 'Not a git repository with a commit'}. Launch would be refused.`, tone: 'warning', blocks: true }
  }
  if (check.inRepo) return { text: check.toplevel ? `Git repository at ${check.toplevel}.` : 'In a git repository.', tone: 'neutral', blocks: false }
  return { text: 'Not a git repository; fine with a shared working directory.', tone: 'neutral', blocks: false }
}
