import { serverAffecting, validate, type DesktopSettings } from './settings'

/** What became of the last save from the Settings page, for its toast: handed to the workbench once (SettingsSaver.take). */
export type SaveOutcome = '' | 'saved' | 'restarted'

export interface SaveDeps {
  get: () => DesktopSettings
  set: (s: DesktopSettings) => void
  /** Restarts the server; what it resolves with (the supervisor's handshake) is not the saver's business. */
  restart: () => Promise<unknown>
  /** Windows: the settings are the WSL distribution's paths. */
  posix: boolean
}

/**
 * SettingsSaver applies a save from the Settings page and keeps what became of
 * it for the page to ask once. A restarted server listens on a fresh port and
 * the window follows it, on the same page, as soon as the server runs: the
 * page that saved is gone before its save resolves, and the page that followed
 * asks (#214). So the outcome is written before the restart is awaited; a
 * restart that fails takes it back, and the failure reaches the page that
 * saved, which is still there. A save that changes nothing the server reads,
 * the retry after a failed restart among them (the settings were kept), says
 * saved, never restarted.
 */
export class SettingsSaver {
  private pending: SaveOutcome = ''

  async save(d: SaveDeps, patch: Partial<DesktopSettings>): Promise<DesktopSettings> {
    const cur = d.get()
    // The zoom is the app's own (Ctrl+= and Ctrl+-): a form saved with an older level leaves the current one.
    const next = { ...cur, ...patch, zoomLevel: cur.zoomLevel } as DesktopSettings
    const problems = validate(next, d.posix)
    if (problems.length) throw new Error(problems.join('; '))
    d.set(next)
    if (!serverAffecting(cur, next)) {
      this.pending = 'saved'
      return next
    }
    this.pending = 'restarted'
    try {
      await d.restart()
    } catch (e) {
      this.pending = ''
      throw e
    }
    return next
  }

  /** The last save's outcome, once: '' after, and when none is owed. */
  take(): SaveOutcome {
    const o = this.pending
    this.pending = ''
    return o
  }
}
