import { describe, expect, it } from 'vitest'
import { NEWLINE_IN_PROMPT, newlineChord } from './terminalKeys'

const key = (k: string, mods: Partial<Pick<KeyboardEvent, 'shiftKey' | 'ctrlKey' | 'altKey' | 'metaKey'>> = {}) => ({ key: k, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, ...mods })

describe('the newline chord', () => {
  it('is Shift+Enter or Ctrl+Enter, with neither Alt nor Meta', () => {
    expect(newlineChord(key('Enter', { shiftKey: true }))).toBe(true)
    expect(newlineChord(key('Enter', { ctrlKey: true }))).toBe(true)
    expect(newlineChord(key('Enter', { shiftKey: true, ctrlKey: true }))).toBe(true)
    expect(newlineChord(key('Enter'))).toBe(false)
    expect(newlineChord(key('Enter', { altKey: true }))).toBe(false)
    expect(newlineChord(key('Enter', { metaKey: true }))).toBe(false)
    expect(newlineChord(key('Enter', { shiftKey: true, metaKey: true }))).toBe(false)
    expect(newlineChord(key('a', { shiftKey: true }))).toBe(false)
  })

  it('sends ESC CR, what Claude Code and Codex read as a newline', () => {
    expect(NEWLINE_IN_PROMPT).toBe('\u001b\r')
  })
})
