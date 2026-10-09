import { describe, expect, it } from 'vitest'
import { confirmChoices, confirmQuestion } from './nvimConfirm'

describe("Neovim's confirm question", () => {
  it('reads the choices, their keys and the default from the last line', () => {
    const text = 'Save changes to "notes.txt"?\n[Y]es, (N)o, (C)ancel: '
    expect(confirmQuestion(text)).toBe('Save changes to "notes.txt"?')
    expect(confirmChoices(text)).toEqual([
      { label: 'Yes', key: 'y', default: true },
      { label: 'No', key: 'n', default: false },
      { label: 'Cancel', key: 'c', default: false },
    ])
  })
  it('takes a key in the middle of a word, and finds none in plain words', () => {
    expect(confirmChoices('W11: Warning\n[O]K, (L)oad File, Load (A)ll')).toEqual([
      { label: 'OK', key: 'o', default: true },
      { label: 'Load File', key: 'l', default: false },
      { label: 'Load All', key: 'a', default: false },
    ])
    expect(confirmChoices('written')).toEqual([])
  })
})
