import type { RunInfo, RunTask } from '~/composables/useSessions'

// A managed run's tasks on the run page (#334, row 7): the counts its
// header says, a task's state in words, which actions apply to it, and the
// commands a person sends with receipts (docs/protocol.md, "Commands with
// receipts"): the browser mints the id, retries a lost reply once with the
// same id, and reads a refusal from the receipt it got.

/** A command's receipt as the server answers it. */
export interface Receipt {
  commandId: string
  intent: string
  revision: number
  repeat?: boolean
  status: 'done' | 'failed'
  error?: string
  refusal?: string
  /** The task the command changed, as the receipt keeps it; the run has the rest. */
  task?: ReceiptTask
  paused?: boolean
}

/** A task as a receipt keeps it: what the command did to it. */
export interface ReceiptTask {
  id: string
  state: string
  reason?: string
  attempts: number
  result?: { attempt: number, commit?: string }
}

/** The body every command takes. */
export interface CommandBody {
  commandId: string
  expectedRevision?: number
}

export type TaskCommand = 'cancel' | 'retry' | 'accept'

export interface TaskCounts {
  total: number
  done: number
  failed: number
  cancelled: number
  running: number
  checking: number
  waiting: number
}

/** How many tasks are in each state: done (succeeded), failed, cancelled, running, checking (verifying), waiting (blocked or ready). */
export function taskCounts(tasks: readonly RunTask[] | undefined): TaskCounts {
  const c: TaskCounts = { total: 0, done: 0, failed: 0, cancelled: 0, running: 0, checking: 0, waiting: 0 }
  for (const t of tasks ?? []) {
    c.total++
    switch (t.state) {
      case 'succeeded':
        c.done++
        break
      case 'failed':
        c.failed++
        break
      case 'cancelled':
        c.cancelled++
        break
      case 'running':
        c.running++
        break
      case 'verifying':
        c.checking++
        break
      default:
        c.waiting++
    }
  }
  return c
}

export type BadgeColor = 'success' | 'warning' | 'error' | 'neutral' | 'info'

/** The header's badges for a managed run: `3/7 tasks`, then what is wrong or moving (`1 failed`, `2 running`, `1 checking`), and `paused`. */
export function taskBadges(run: Pick<RunInfo, 'tasks' | 'paused' | 'outcome'>): Array<{ label: string; color: BadgeColor }> {
  const c = taskCounts(run.tasks)
  const out: Array<{ label: string; color: BadgeColor }> = [{ label: `${c.done}/${c.total} tasks`, color: run.outcome === 'succeeded' ? 'success' : 'neutral' }]
  if (c.failed) out.push({ label: `${c.failed} failed`, color: 'error' })
  if (c.cancelled) out.push({ label: `${c.cancelled} cancelled`, color: 'neutral' })
  if (c.running) out.push({ label: `${c.running} running`, color: 'success' })
  if (c.checking) out.push({ label: `${c.checking} checking`, color: 'info' })
  if (run.paused) out.push({ label: 'paused', color: 'warning' })
  return out
}

export type Tone = 'warning' | 'error' | 'muted' | 'default' | 'success'

/** A task's state in words: what it waits for, which attempt runs, how it ended; on an interrupted run, what its continuation takes up. */
export function taskWords(t: RunTask, run: Pick<RunInfo, 'stoppedAt' | 'paused'> & { state?: string }): { text: string; tone: Tone } {
  const n = t.attempt?.n ?? t.attempts
  switch (t.state) {
    case 'blocked':
      return { text: t.reason || (t.needs?.length ? `needs ${t.needs.join(', ')}` : 'waiting'), tone: t.reason?.includes('will not') ? 'error' : 'muted' }
    case 'ready': {
      const why = t.reason ? `${t.reason}; ` : ''
      if (run.state === 'interrupted') return { text: `${why}taken up when the run is continued`, tone: 'warning' }
      if (run.stoppedAt) return { text: `${why}ready, the run is stopped`, tone: 'muted' }
      if (run.paused) return { text: `${why}ready, scheduling is paused`, tone: 'warning' }
      return { text: `${why}ready to start`, tone: 'default' }
    }
    case 'running':
      if (t.blocker) return { text: `attempt ${n} blocked by ${t.blocker.by}: ${t.blocker.reason}`, tone: 'warning' }
      return { text: `attempt ${n} running`, tone: 'default' }
    case 'verifying':
      return { text: `attempt ${n}: checking the result`, tone: 'default' }
    case 'succeeded': {
      const r = t.result
      if (!r) return { text: 'succeeded', tone: 'success' }
      const kept = t.importedFrom ? ' (kept from the run continued)' : ''
      if (t.kind === 'review') return { text: `${r.verdict === 'needs_changes' ? `asked for changes (${r.findings?.length ?? 0} findings)` : `approved${r.findings?.length ? ` with ${r.findings.length} notes` : ''}`}${kept}`, tone: 'success' }
      return { text: `succeeded by attempt ${r.attempt}${kept}${r.summary ? `: ${r.summary}` : ''}`, tone: 'success' }
    }
    case 'failed':
      return { text: t.reason || 'failed', tone: 'error' }
    case 'cancelled':
      return { text: 'cancelled', tone: 'muted' }
  }
  return { text: t.state, tone: 'muted' }
}

/** A task's state as a dot: running green, checking blue, failed red, done the brand colour, waiting hollow, cancelled grey. */
export function taskDot(state: RunTask['state']): string {
  switch (state) {
    case 'running':
      return 'bg-success'
    case 'verifying':
      return 'bg-info'
    case 'failed':
      return 'bg-error'
    case 'succeeded':
      return 'bg-primary'
    case 'blocked':
      return 'bg-transparent ring-1 ring-neutral-400'
  }
  return 'bg-neutral-400'
}

export interface TaskActionSet {
  retry: boolean
  accept: boolean
  cancel: boolean
  open: boolean
  copyBranch: boolean
  showResult: boolean
}

/** Which actions apply to a task: Retry on a failed or cancelled task, Accept anyway on a failed task whose last attempt submitted a commit, Cancel on a live one. None that changes a run continued: its continuation owns its tasks (`409 run_continued`). */
export function taskActions(t: RunTask, run: Pick<RunInfo, 'stoppedAt' | 'record' | 'resumedBy'>): TaskActionSet {
  const live = !run.stoppedAt && !run.record && !run.resumedBy
  const terminal = t.state === 'succeeded' || t.state === 'failed' || t.state === 'cancelled'
  return {
    retry: live && (t.state === 'failed' || t.state === 'cancelled'),
    accept: live && t.state === 'failed' && t.kind === 'work' && !!t.attempt?.result,
    cancel: live && !terminal,
    open: !!t.attempt?.sessionId,
    copyBranch: !!t.attempt?.branch,
    showResult: !!t.result,
  }
}

/** A command id the browser mints: 16 hex characters, from the platform's random bytes where there are any. */
export function mintCommandId(): string {
  const bytes = new Uint8Array(8)
  const c = (globalThis as { crypto?: { getRandomValues?: (b: Uint8Array) => void } }).crypto
  if (c?.getRandomValues) c.getRandomValues(bytes)
  else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/** A command the run refused: the receipt says why (its `error`), and nothing changed. */
export class CommandRefused extends Error {
  constructor(
    message: string,
    public receipt: Receipt,
  ) {
    super(message)
  }
}

/**
 * Sends a command with a minted id. A reply lost to the network is asked for once more with the same id, which the server answers as the
 * first (the receipt); a second loss is the error. A refusal (a 409 carrying the receipt) is a CommandRefused with the server's words.
 */
export async function sendCommand(send: (body: CommandBody) => Promise<{ receipt: Receipt }>, expectedRevision?: number): Promise<Receipt> {
  const body: CommandBody = { commandId: mintCommandId() }
  if (expectedRevision) body.expectedRevision = expectedRevision
  for (let attempt = 1; ; attempt++) {
    try {
      return (await send(body)).receipt
    } catch (e) {
      const err = e as { status?: number; message?: string; body?: { receipt?: Receipt } }
      if (err.status === 0 && attempt === 1) continue
      if (err.body?.receipt) throw new CommandRefused(err.body.receipt.error || err.message || 'refused', err.body.receipt)
      throw e
    }
  }
}
