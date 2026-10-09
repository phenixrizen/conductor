import type { CommitInfo, FileChange, FileHeader } from '~/utils/protocol'
import { changeRows, joinTop, type ChangeRow } from './changes'

/**
 * The Commits section (design 4e): the commits on the branch since the
 * session started (or since a crew member's run base), each opening to its
 * files, a file opening as its diff against the commit's parent. Pure.
 */
export interface CommitRow {
  sha: string
  short: string
  subject: string
  author: string
  at: string
  atMs: number
  parent: string
}

export function commitRows(header: FileHeader | null): CommitRow[] {
  if (header?.kind !== 'log') return []
  return (header.commits ?? []).map((c: CommitInfo) => ({ sha: c.sha, short: c.short || c.sha.slice(0, 7), subject: c.subject || '(no subject)', author: c.author || '', at: c.at, atMs: Date.parse(c.at) || 0, parent: c.parent || '' }))
}

/** A commit's files as the Changes rows are drawn, each with a rename's old path made absolute. */
export interface CommitChangeRow extends ChangeRow {
  fromAbs?: string
}

export function commitChangeRows(header: FileHeader | null): CommitChangeRow[] {
  if (header?.kind !== 'commit') return []
  const changes = header.changes ?? []
  return changeRows(header.path, changes).map((r, i) => {
    const from = (changes[i] as FileChange).from
    return from ? { ...r, fromAbs: joinTop(header.path, from) } : r
  })
}

/** The head's words: "3 commits on main since 08:31", "1 commit on crew/core since main @ 3f2a1c4", "no commits since 08:31". */
export function commitsHead(n: number, header: FileHeader | null, base?: string): string {
  const count = n === 0 ? 'no commits' : n === 1 ? '1 commit' : `${n} commits`
  const on = header?.branch ? ` on ${header.branch}` : ''
  if (base && base !== 'HEAD') return `${count}${on} since ${base}${header?.base ? ` @ ${header.base}` : ''}`
  const since = header?.since ? clock(header.since) : ''
  return `${count}${on}${since ? ` since ${since}` : ' since the session started'}`
}

/** "08:31" in the viewer's time, or "" for a time that does not parse. */
export function clock(iso: string): string {
  const t = Date.parse(iso)
  if (!t) return ''
  return new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
}

/** When a commit was made, from now: "just now", "5m ago", "3h ago", "2d ago". */
export function commitAgo(atMs: number, now: number): string {
  const s = Math.max(0, Math.round((now - atMs) / 1000))
  if (s < 45) return 'just now'
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  if (h < 48) return `${h}h ago`
  return `${Math.round(h / 24)}d ago`
}

/** What a commit's diff is against: "3f2a1c4 vs its parent 9e8d7c6", or "3f2a1c4, the first commit". */
export function commitAgainst(short: string, parent: string): string {
  return parent ? `${short} vs its parent ${parent.slice(0, 7)}` : `${short}, the first commit`
}
