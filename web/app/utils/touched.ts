import type { ActivityEntry } from '~/utils/protocol'

/**
 * The Touched section (design 4e): the files the agent created, edited,
 * read or deleted, newest first, each with the tool, the agent and the
 * time, from the session's `file` activity entries; and the dots the
 * Explorer shows beside touched files. Pure.
 */
export interface TouchedRow {
  /** The path as the agent named it, made absolute under the working directory when relative. */
  abs: string
  dir: string
  name: string
  op: 'read' | 'edit' | 'write' | 'delete'
  tool: string
  by: string
  at: string
}

export function touchedRows(entries: readonly ActivityEntry[], cwd: string): TouchedRow[] {
  const out: TouchedRow[] = []
  for (let i = entries.length - 1; i >= 0; i--) {
    const e = entries[i]!
    if (e.type !== 'file' || !e.path || !e.op) continue
    const abs = e.path.startsWith('/') ? e.path : `${cwd.replace(/\/+$/, '')}/${e.path.replace(/^\.\//, '')}`
    const k = abs.lastIndexOf('/')
    out.push({ abs, dir: k >= 0 ? abs.slice(cwd.replace(/\/+$/, '').length + 1, k + 1) : '', name: abs.slice(k + 1), op: e.op, tool: e.tool ?? '', by: e.byName ?? '', at: e.at })
  }
  return out
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

/** "Edit · codex · 08:33:12": the tool as the agent calls it, the agent, the time. */
export function touchedWords(r: TouchedRow): string {
  const time = new Date(r.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
  return [r.tool || r.op, r.by, time].filter(Boolean).join(' · ')
}
