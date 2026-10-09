import { describe, expect, it } from 'vitest'
import { cursorStyleFor, isVisual, keptByConductor, keyToNvim, MAX_NVIM_INPUT, modeWords, textToNvim } from './nvimKeys'

const k = (key: string, mods: Partial<Pick<KeyboardEvent, 'ctrlKey' | 'altKey' | 'metaKey' | 'shiftKey'>> = {}) => ({ key, ctrlKey: false, altKey: false, metaKey: false, shiftKey: false, ...mods })

describe('keyToNvim', () => {
  it('sends printable keys as they are, with < escaped', () => {
    expect(keyToNvim(k('d'))).toBe('d')
    expect(keyToNvim(k('A', { shiftKey: true }))).toBe('A')
    expect(keyToNvim(k(':', { shiftKey: true }))).toBe(':')
    expect(keyToNvim(k('<', { shiftKey: true }))).toBe('<lt>')
    expect(keyToNvim(k('é'))).toBe('é')
  })
  it('names the special keys and chords the way Neovim does', () => {
    expect(keyToNvim(k('Escape'))).toBe('<Esc>')
    expect(keyToNvim(k('Enter'))).toBe('<CR>')
    expect(keyToNvim(k('Backspace'))).toBe('<BS>')
    expect(keyToNvim(k('Tab', { shiftKey: true }))).toBe('<S-Tab>')
    expect(keyToNvim(k('ArrowDown'))).toBe('<Down>')
    expect(keyToNvim(k(' '))).toBe('<Space>')
    expect(keyToNvim(k('F5'))).toBe('<F5>')
    expect(keyToNvim(k('r', { ctrlKey: true }))).toBe('<C-r>')
    expect(keyToNvim(k('x', { altKey: true }))).toBe('<M-x>')
    expect(keyToNvim(k('v', { ctrlKey: true, altKey: true }))).toBe('<C-M-v>')
    expect(keyToNvim(k('Enter', { ctrlKey: true }))).toBe('<C-CR>')
    expect(keyToNvim(k('w', { metaKey: true }))).toBe('<C-w>')
  })
  it('sends nothing for a lone modifier or a dead key', () => {
    for (const key of ['Shift', 'Control', 'Alt', 'Meta', 'Dead', 'Unidentified', '']) expect(keyToNvim(k(key))).toBeNull()
  })
  it('leaves the tab cycle to the editor area', () => {
    expect(keptByConductor(k('Tab', { ctrlKey: true }))).toBe(true)
    expect(keptByConductor(k('Tab', { ctrlKey: true, shiftKey: true }))).toBe(true)
    expect(keptByConductor(k('w', { ctrlKey: true }))).toBe(false)
    expect(keptByConductor(k('Tab'))).toBe(false)
  })
})

describe('the mode', () => {
  it('picks the cursor style', () => {
    expect(cursorStyleFor('n')).toBe('block')
    expect(cursorStyleFor('i')).toBe('line')
    expect(cursorStyleFor('insert')).toBe('line')
    expect(cursorStyleFor('c')).toBe('line')
    expect(cursorStyleFor('R')).toBe('underline')
    expect(cursorStyleFor('v')).toBe('block')
  })
  it('words the status line', () => {
    expect(modeWords('n')).toBe('')
    expect(modeWords('i')).toBe('-- INSERT --')
    expect(modeWords('V')).toBe('-- VISUAL LINE --')
    expect(modeWords('\x16')).toBe('-- VISUAL BLOCK --')
    expect(modeWords('R')).toBe('-- REPLACE --')
  })
  it('knows the visual modes', () => {
    expect(isVisual('v')).toBe(true)
    expect(isVisual('V')).toBe(true)
    expect(isVisual('n')).toBe(false)
  })
})

describe('text with no key press (round 13, G4)', () => {
  it('is typed as it is, with < named and line breaks and tabs as keys', () => {
    expect(textToNvim('café')).toEqual(['café'])
    expect(textToNvim('日本語')).toEqual(['日本語'])
    expect(textToNvim('a<b\tc\r\nd\x07')).toEqual(['a<lt>b<Tab>c<CR>d'])
    expect(textToNvim('')).toEqual([])
  })
  it('comes in pieces under the bound, never splitting a character or a key name', () => {
    const pieces = textToNvim('é'.repeat(200) + '<'.repeat(100))
    const enc = new TextEncoder()
    expect(pieces.length).toBeGreaterThan(1)
    for (const p of pieces) {
      expect(enc.encode(p).length).toBeLessThanOrEqual(MAX_NVIM_INPUT)
      expect(p).not.toMatch(/<l$|<lt?$/)
    }
    expect(pieces.join('')).toBe('é'.repeat(200) + '<lt>'.repeat(100))
  })
})
