import type { FileChange, FileHeader } from '~/utils/protocol'

/**
 * The Changes section (design 4d): git status with each file's lines, as
 * rows the pane lists, the marks the Explorer shows beside changed files,
 * and the words of the footer. Pure.
 */
export interface ChangeRow {
  /** The path from the tree's top, and absolute. */
  path: string
  abs: string
  /** The folders above the name, with a trailing slash, or empty. */
  dir: string
  name: string
  status: FileChange['status']
  added: number
  removed: number
  binary: boolean
}

export function joinTop(top: string, path: string): string {
  return `${top.replace(/\/+$/, '')}/${path}`
}

/** The rows, in the order git listed them. */
export function changeRows(top: string, changes: readonly FileChange[]): ChangeRow[] {
  return changes.map((c) => {
    const i = c.path.lastIndexOf('/')
    return { path: c.path, abs: joinTop(top, c.path), dir: i >= 0 ? c.path.slice(0, i + 1) : '', name: i >= 0 ? c.path.slice(i + 1) : c.path, status: c.status, added: c.added ?? 0, removed: c.removed ?? 0, binary: !!c.binary }
  })
}

/** The Explorer's marks: a changed file's absolute path to its status letter (R shows as M). */
export function changeMarks(top: string, changes: readonly FileChange[]): Map<string, FileChange['status']> {
  const out = new Map<string, FileChange['status']>()
  for (const c of changes) out.set(joinTop(top, c.path), c.status === 'R' ? 'M' : c.status)
  return out
}

/** The colour of a status letter: modified amber, added green, deleted red, untracked neutral. */
export function statusTone(status: FileChange['status']): 'warning' | 'success' | 'error' | 'neutral' {
  switch (status) {
    case 'A':
      return 'success'
    case 'D':
      return 'error'
    case '?':
      return 'neutral'
    default:
      return 'warning'
  }
}

/** The letter shown: untracked files as A (they are additions), the rest as git says. */
export function statusLetter(status: FileChange['status']): string {
  return status === '?' ? 'A' : status
}

/** "4 files", "1 file", "no changes". */
export function changesTitle(n: number): string {
  return n === 0 ? 'no changes' : n === 1 ? '1 file' : `${n} files`
}

/** "just now", "8s ago", "2m ago", "3h ago". */
export function agoWords(ms: number): string {
  if (ms < 2000) return 'just now'
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  return `${Math.round(m / 60)}h ago`
}

/** What a diff is against: "working directory vs HEAD", or "<branch> vs <base> @ <id>" for a crew member's run base. */
export function diffAgainst(header: Pick<FileHeader, 'branch' | 'base'> | null, base?: string): string {
  if (base && base !== 'HEAD') return `${header?.branch || 'working directory'} vs ${base}${header?.base ? ` @ ${header.base}` : ''}`
  return 'working directory vs HEAD'
}
