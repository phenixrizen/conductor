import type { ActivityEntry, TouchedFile } from '~/utils/protocol'
import { fileToolWords } from './events'

/**
 * The Touched section (design 4e): the files the agent created, edited,
 * read or deleted, one row per file (round 13, G1), the most recently
 * touched first, each with its latest op, tool, agent and time and how many
 * times it was touched; and the dots the Explorer shows beside touched
 * files. The session's own index (a `touched` file request) gives the whole
 * session; the `file` activity entries the page sees add what came after.
 * Pure.
 */
type Op = 'read' | 'edit' | 'write' | 'delete'

export interface TouchedRow {
  /** The path as the agent named it, made absolute under the working directory when relative, cleaned. */
  abs: string
  /** The folder: from the working directory when inside it (with a trailing slash), else absolute. */
  dir: string
  name: string
  op: Op
  ops: Op[]
  count: number
  tool: string
  by: string
  first: string
  /** The latest touch. */
  at: string
}

/** p made absolute under cwd when relative, with `.`, `..` and repeated slashes resolved, as the server's index keys it. */
export function touchedAbs(p: string, cwd: string): string {
  const full = p.startsWith('/') ? p : `${cwd.replace(/\/+$/, '')}/${p}`
  const out: string[] = []
  for (const part of full.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') out.pop()
    else out.push(part)
  }
  return `/${out.join('/')}`
}

function where(abs: string, cwd: string): { dir: string; name: string } {
  const root = touchedAbs(cwd, '/')
  const k = abs.lastIndexOf('/')
  const name = abs.slice(k + 1)
  const folder = abs.slice(0, k + 1)
  if (root === '/' || !`${folder}`.startsWith(`${root}/`)) return { dir: folder, name }
  return { dir: folder.slice(root.length + 1), name }
}

const ms = (t: string) => {
  const n = Date.parse(t)
  return Number.isNaN(n) ? 0 : n
}

/**
 * The rows, one per file: the session's index (server, when the page has it) with the activity entries laid over it. An entry no newer
 * than the index's latest touch of its file is in the index already and is not counted again.
 */
export function touchedRows(entries: readonly ActivityEntry[], cwd: string, server?: readonly TouchedFile[] | null): TouchedRow[] {
  const rows = new Map<string, TouchedRow>()
  for (const f of server ?? []) {
    const abs = touchedAbs(f.path, cwd)
    rows.set(abs, { abs, ...where(abs, cwd), op: f.op, ops: [...(f.ops?.length ? f.ops : [f.op])], count: Math.max(1, f.count), tool: f.tool ?? '', by: f.by ?? '', first: f.first, at: f.last })
  }
  for (const e of entries) {
    if (e.type !== 'file' || !e.path || !e.op) continue
    const abs = touchedAbs(e.path, cwd)
    const r = rows.get(abs)
    if (!r) {
      rows.set(abs, { abs, ...where(abs, cwd), op: e.op, ops: [e.op], count: 1, tool: e.tool ?? '', by: e.byName ?? '', first: e.at, at: e.at })
      continue
    }
    if (ms(e.at) <= ms(r.at)) continue
    r.count++
    r.op = e.op
    r.tool = e.tool ?? ''
    r.by = e.byName ?? ''
    r.at = e.at
    if (!r.ops.includes(e.op)) r.ops.push(e.op)
  }
  return [...rows.values()].sort((a, b) => ms(b.at) - ms(a.at) || (a.abs < b.abs ? -1 : 1))
}

/** The files touched, by absolute path, for the Explorer's dots. */
export function touchedPaths(rows: readonly TouchedRow[]): Set<string> {
  return new Set(rows.map((r) => r.abs))
}

/** The icon of an op. */
export function opIcon(op: TouchedRow['op']): string {
  switch (op) {
    case 'read':
      return 'i-lucide-eye'
    case 'write':
      return 'i-lucide-file-plus'
    case 'delete':
      return 'i-lucide-trash-2'
    default:
      return 'i-lucide-pencil'
  }
}

/** "Edit · codex · 08:33:12 · 4 times": the latest touch's tool as the agent calls it ("seen by git" for a file git saw), the agent, the time, and the count past one. */
export function touchedWords(r: Pick<TouchedRow, 'tool' | 'op' | 'by' | 'at' | 'count'>): string {
  const time = new Date(r.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
  return [fileToolWords(r.tool) || r.op, r.by, time, r.count > 1 ? `${r.count} times` : ''].filter(Boolean).join(' · ')
}
