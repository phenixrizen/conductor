import type { ShortcutGroup, ShortcutRow } from '../composables/useShortcuts'

/**
 * The chords per system (#312): what each system shows and binds where
 * the systems differ, and what a system or a browser takes before the
 * page sees it. The `?` modal and the palette's `?` rows show the chord
 * of the system the page runs on; the pages bind every chord of a row
 * (the Alt+Esc that Windows takes is bound too, for the systems that
 * leave it). Pure.
 */
export type System = 'mac' | 'windows' | 'linux'

/**
 * The system the page runs on: the browser's platform first (a headless Chromium may carry another system's user agent while its
 * platform tells the truth), then the user agent (as Nuxt UI's kbd reads `Macintosh;`).
 */
export function detectSystem(userAgent: string, platform = ''): System {
  if (/^Mac|iPhone|iPad/.test(platform)) return 'mac'
  if (/^Win/.test(platform)) return 'windows'
  if (/Linux|X11|BSD/.test(platform)) return 'linux'
  if (/Macintosh;|Mac OS X|iPhone|iPad/.test(userAgent)) return 'mac'
  if (/Windows|Win32|Win64/.test(userAgent)) return 'windows'
  return 'linux'
}

/** The actions whose chord differs by system, or whose chord a system takes: the row names its action by `chord`. */
export type Chord = 'leaveTerminal' | 'copy' | 'paste' | 'sidebar'

/** Per system, the keys a chord's row shows: `terminal` while typing into an agent, `keys` outside. Absent: the row's own. */
const CHORDS: Record<Chord, Partial<Record<System, Pick<ShortcutRow, 'keys' | 'terminal'>>>> = {
  // Alt+Esc cycles windows on Windows and never reaches the app: Alt+Q, a free Alt letter, leaves the terminal everywhere, and
  // Alt+Esc still does where the system leaves it.
  leaveTerminal: {
    mac: { keys: ['escape'], terminal: ['alt', 'Q'] },
    linux: { keys: ['escape'], terminal: ['alt', 'Q'] },
    windows: { keys: ['escape'], terminal: ['alt', 'Q'] },
  },
  // The terminal's copy and paste: the native pair on a Mac.
  // `command`, not `meta`: UKbd shows `meta` as ⌘ by the user agent's `Macintosh;` alone, which an iPhone's or iPad's lacks.
  copy: { mac: { keys: [], terminal: ['command', 'C'] } },
  paste: { mac: { keys: [], terminal: ['command', 'V'] } },
  // The sidebar: ⌘B on a Mac, Ctrl+B elsewhere (UKbd shows `meta` so); Win+B and Super+B are the system's and are not bound.
  sidebar: {},
}

/** A group's rows with the chords of `system` in place of the rows' own, where the table has them. */
export function rowsFor(group: ShortcutGroup, system: System): ShortcutGroup {
  return {
    ...group,
    rows: group.rows.map((r) => {
      const c = r.chord ? CHORDS[r.chord][system] : undefined
      return c ? { ...r, keys: c.keys, terminal: c.terminal } : r
    }),
  }
}

/** What a system or a browser takes before the page sees it, said in the modal for the system the page runs on. */
export interface SystemNote {
  system: System
  /** Where it holds: a browser tab, the desktop app, or both; a browser named when one alone. */
  surface: 'browser' | 'app' | 'both'
  browser?: 'firefox'
  chord: string
  note: string
}

export const SYSTEM_NOTES: readonly SystemNote[] = [
  { system: 'windows', surface: 'both', chord: 'Alt+Esc', note: 'Windows cycles its windows with it: Alt+Q leaves the terminal instead.' },
  { system: 'windows', surface: 'both', chord: 'Win+B', note: 'the system’s (the tray): the sidebar is Ctrl+B.' },
  { system: 'linux', surface: 'both', chord: 'Super+B', note: 'some desktops bind it: the sidebar is Ctrl+B.' },
  { system: 'linux', surface: 'browser', browser: 'firefox', chord: 'Ctrl+B', note: 'Firefox opens its bookmarks sidebar: collapse the sidebar with its button, or Alt+B from a terminal.' },
  { system: 'windows', surface: 'browser', browser: 'firefox', chord: 'Ctrl+B', note: 'Firefox opens its bookmarks sidebar: collapse the sidebar with its button, or Alt+B from a terminal.' },
  { system: 'linux', surface: 'browser', browser: 'firefox', chord: 'Alt+letter', note: 'Firefox may open its menu bar on a plain Alt: the chords from a terminal are read first, so they reach the app.' },
  { system: 'mac', surface: 'both', chord: 'Option+letter', note: 'types a special character unless the page takes the chord first: the chords from a terminal are taken.' },
  { system: 'mac', surface: 'browser', chord: '⌘W, ⌃Tab', note: 'the browser’s tabs: in the editor use its tab strip; the desktop app’s Window menu binds no Close, so the editor keeps them there.' },
  { system: 'linux', surface: 'browser', chord: 'Ctrl+Tab, Ctrl+W', note: 'the browser’s tabs: in the editor use its tab strip; the desktop app’s Window menu binds no Close, so the editor keeps them there.' },
  { system: 'windows', surface: 'browser', chord: 'Ctrl+Tab, Ctrl+W', note: 'the browser’s tabs: in the editor use its tab strip; the desktop app’s Window menu binds no Close, so the editor keeps them there.' },
  { system: 'mac', surface: 'both', chord: '⌃⌘F', note: 'the Mac’s fullscreen: F toggles the app’s.' },
]

/** The notes for one system, on one surface (`both` always). */
export function systemNotes(system: System, surface: 'browser' | 'app'): SystemNote[] {
  return SYSTEM_NOTES.filter((n) => n.system === system && (n.surface === 'both' || n.surface === surface))
}

/** The chord as the system shows it, for a table or a label: `⌘` `⌥` `⌃` on a Mac, `Ctrl` `Alt` elsewhere. */
export function chordLabel(keys: readonly string[], system: System): string {
  const names: Record<string, string> = system === 'mac' ? { meta: '⌘', command: '⌘', ctrl: '⌃', alt: '⌥', shift: '⇧', enter: '↵', escape: 'Esc' } : { meta: 'Ctrl', command: 'Cmd', ctrl: 'Ctrl', alt: 'Alt', shift: 'Shift', enter: 'Enter', escape: 'Esc' }
  return keys.map((k) => names[k] ?? (k.length === 1 ? k.toUpperCase() : k)).join(system === 'mac' ? '' : '+')
}
