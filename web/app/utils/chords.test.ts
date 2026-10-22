import { describe, expect, it } from 'vitest'
import { CAROUSEL_SHORTCUTS, TERMINAL_SHORTCUTS, WALL_SHORTCUTS } from '../composables/useShortcuts'
import { chordLabel, detectSystem, rowsFor, systemNotes } from './chords'

describe('the system the page runs on', () => {
  it('is read from the user agent, then the platform', () => {
    expect(detectSystem('Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36')).toBe('mac')
    expect(detectSystem('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36')).toBe('windows')
    expect(detectSystem('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36')).toBe('linux')
    expect(detectSystem('', 'MacIntel')).toBe('mac')
    expect(detectSystem('', 'Win32')).toBe('windows')
    expect(detectSystem('', 'Linux x86_64')).toBe('linux')
    // The platform wins over the user agent: the suite's Chromium says Windows in its user agent on a Linux platform.
    expect(detectSystem('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36', 'Linux x86_64')).toBe('linux')
    expect(detectSystem('Mozilla/5.0 (X11; Linux x86_64)', 'MacIntel')).toBe('mac')
  })
})

describe('the chords per system', () => {
  it('leaves the terminal with Alt+Q everywhere (Windows takes Alt+Esc), the row marked', () => {
    for (const g of [WALL_SHORTCUTS, CAROUSEL_SHORTCUTS]) {
      const own = g.rows.find((r) => r.chord === 'leaveTerminal')
      expect(own?.terminal).toEqual(['alt', 'Q'])
      for (const system of ['mac', 'linux', 'windows'] as const) {
        const row = rowsFor(g, system).rows.find((r) => r.chord === 'leaveTerminal')
        expect(row?.terminal).toEqual(['alt', 'Q'])
        expect(row?.keys).toEqual(['escape'])
      }
    }
  })
  it('shows the terminal’s copy and paste as the native pair on a Mac and Ctrl+Shift elsewhere', () => {
    const mac = rowsFor(TERMINAL_SHORTCUTS, 'mac').rows
    // `command`, which UKbd shows as ⌘ on every user agent (an iPhone's lacks the `Macintosh;` UKbd reads `meta` by).
    expect(mac.find((r) => r.chord === 'copy')?.terminal).toEqual(['command', 'C'])
    expect(mac.find((r) => r.chord === 'paste')?.terminal).toEqual(['command', 'V'])
    for (const system of ['linux', 'windows'] as const) {
      const rows = rowsFor(TERMINAL_SHORTCUTS, system).rows
      expect(rows.find((r) => r.chord === 'copy')?.terminal).toEqual(['ctrl', 'shift', 'C'])
      expect(rows.find((r) => r.chord === 'paste')?.terminal).toEqual(['ctrl', 'shift', 'V'])
    }
    // A row without a chord is as it is, on every system.
    expect(rowsFor(TERMINAL_SHORTCUTS, 'mac').rows.filter((r) => !r.chord)).toEqual(TERMINAL_SHORTCUTS.rows.filter((r) => !r.chord))
  })
  it('names what each system takes, the browser-only notes on the browser alone', () => {
    expect(systemNotes('windows', 'app').map((n) => n.chord)).toEqual(['Alt+Esc', 'Win+B'])
    expect(systemNotes('windows', 'browser').map((n) => n.chord)).toEqual(['Alt+Esc', 'Win+B', 'Ctrl+B', 'Ctrl+Tab, Ctrl+W'])
    expect(systemNotes('mac', 'browser').map((n) => n.chord)).toEqual(['Option+letter', '⌘W, ⌃Tab', '⌃⌘F'])
    expect(systemNotes('mac', 'app').map((n) => n.chord)).toEqual(['Option+letter', '⌃⌘F'])
    expect(systemNotes('linux', 'browser').some((n) => n.browser === 'firefox')).toBe(true)
    expect(systemNotes('linux', 'app').some((n) => n.browser === 'firefox')).toBe(false)
  })
  it('labels a chord as the system shows it', () => {
    expect(chordLabel(['meta', 'K'], 'mac')).toBe('⌘K')
    expect(chordLabel(['meta', 'K'], 'linux')).toBe('Ctrl+K')
    expect(chordLabel(['alt', 'escape'], 'windows')).toBe('Alt+Esc')
    expect(chordLabel(['G', 'Y'], 'mac')).toBe('GY')
    expect(chordLabel(['command', 'C'], 'mac')).toBe('⌘C')
  })
})
