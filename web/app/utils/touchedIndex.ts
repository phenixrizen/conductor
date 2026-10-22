import type { TouchedFile } from './protocol'

/**
 * The palette's Touched group across sessions (#304): the server's index of
 * the files every session's agent touched (`GET /api/touched`, read after
 * the palette's first paint and cached), with the feed's live `file`
 * entries laid over it (the feed is newer than the cache by up to a read),
 * one row per session and file, the most recently touched first. Pure.
 */

/** One session of the server's reply: who it is, where it runs, its files the most recently touched first. */
export interface TouchedSession {
  id: string
  name: string
  agentId: string
  kind: string
  status: string
  cwd: string
  touched: TouchedFile[]
  /** The session's list was cut (the per-session limit, or the reply's size). */
  truncated?: boolean
}

export interface TouchedReply {
  sessions: TouchedSession[]
  /** The reply's size cut a session short or left sessions out. */
  truncated?: boolean
}

/** A live `file` entry of the feed, with the session it is of (its working directory when the page knows it). */
export interface FeedFile {
  sessionId: string
  sessionName: string
  cwd?: string
  path: string
  op: string
  tool?: string
  by?: string
  at: string
}

/** A row of the group: one session's touch of one file. */
export interface TouchedHit {
  sessionId: string
  sessionName: string
  cwd: string
  /** The path as the index keys it: absolute, the session's own. */
  path: string
  /** From the working directory when inside it, else the whole path: the row's label. */
  rel: string
  op: TouchedFile['op']
  tool: string
  by: string
  /** The latest touch. */
  at: string
  count: number
}

/** The rows the group lists at most: the index's own cap per session. */
export const TOUCHED_ROWS_MAX = 2000

const ms = (t: string) => {
  const n = Date.parse(t)
  return Number.isNaN(n) ? 0 : n
}

function isFileOp(op: string): op is TouchedFile['op'] {
  return op === 'read' || op === 'edit' || op === 'write' || op === 'delete'
}

/** A Windows working directory (a drive letter or a UNC prefix) keeps its own separator: a hosted session's. */
function windowsPath(p: string): boolean {
  return /^[A-Za-z]:[\\/]/.test(p) || p.startsWith('\\\\')
}

/** p made absolute under cwd when relative, with `.`, `..` and repeated separators resolved, as the server keys the index. */
export function absUnder(cwd: string, p: string): string {
  if (windowsPath(p)) return p
  if (windowsPath(cwd)) {
    const root = cwd.replace(/[\\/]+$/, '')
    return p.startsWith('/') || p.startsWith('\\') ? p : `${root}\\${p.replace(/\//g, '\\')}`
  }
  const full = p.startsWith('/') ? p : `${cwd.replace(/\/+$/, '')}/${p}`
  const out: string[] = []
  for (const part of full.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') out.pop()
    else out.push(part)
  }
  return `/${out.join('/')}`
}

/** abs from the working directory when inside it, else as it is. */
export function relUnder(cwd: string, abs: string): string {
  const root = cwd.replace(/[\\/]+$/, '')
  if (!root) return abs
  const sep = windowsPath(root) ? '\\' : '/'
  const under = abs.startsWith(root + sep) || (sep === '\\' && abs.startsWith(root + '/'))
  return under ? abs.slice(root.length + 1) : abs
}

/**
 * The rows: the index's files (one row per session and path), with the feed's
 * entries laid over it. An entry no newer than the row's latest touch is in
 * the index already and is not counted again; one of a file the index lacks
 * (newer than the cache, or past its cut) is a row of its own. The most
 * recently touched first, ties by path, at most `max` rows.
 */
export function mergeTouched(index: readonly TouchedSession[], feed: readonly FeedFile[], max = TOUCHED_ROWS_MAX): TouchedHit[] {
  const rows = new Map<string, TouchedHit>()
  const cwds = new Map<string, string>()
  for (const s of index) {
    cwds.set(s.id, s.cwd)
    for (const f of s.touched) {
      const path = absUnder(s.cwd, f.path)
      rows.set(`${s.id}\n${path}`, { sessionId: s.id, sessionName: s.name, cwd: s.cwd, path, rel: relUnder(s.cwd, path), op: f.op, tool: f.tool ?? '', by: f.by ?? '', at: f.last, count: Math.max(1, f.count) })
    }
  }
  for (const e of feed) {
    if (!e.path || !isFileOp(e.op)) continue
    const cwd = e.cwd ?? cwds.get(e.sessionId) ?? ''
    const path = cwd ? absUnder(cwd, e.path) : e.path
    const key = `${e.sessionId}\n${path}`
    const r = rows.get(key)
    if (!r) {
      rows.set(key, { sessionId: e.sessionId, sessionName: e.sessionName, cwd, path, rel: relUnder(cwd, path), op: e.op, tool: e.tool ?? '', by: e.by ?? '', at: e.at, count: 1 })
      continue
    }
    if (ms(e.at) <= ms(r.at)) continue
    r.count++
    r.op = e.op
    r.tool = e.tool ?? ''
    r.by = e.by ?? ''
    r.at = e.at
  }
  return [...rows.values()].sort((a, b) => ms(b.at) - ms(a.at) || (a.path < b.path ? -1 : a.path > b.path ? 1 : 0)).slice(0, max)
}
