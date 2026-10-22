import { describe, expect, it, vi } from 'vitest'
import { editorRows, type EditorPaletteState } from './editorRows'

describe("the editor's rows in the palette (#321)", () => {
  const acts = { setKeymap: vi.fn(), save: vi.fn(), close: vi.fn(), askAgent: vi.fn() }
  const base: EditorPaletteState = { activeId: 'tab-1', keymap: 'default', nvimOffered: true, nvimWhy: '', switchBlocked: '', saveable: true, saveWhy: '', canAsk: true, hasSelection: true }

  it('is nothing without a file in front', () => {
    expect(editorRows(null, acts)).toEqual([])
    expect(editorRows({ ...base, activeId: null }, acts)).toEqual([])
  })

  it('lists the four rows, each running its act', () => {
    const rows = editorRows(base, acts)
    expect(rows.map((r) => r.key)).toEqual(['page:keys', 'page:save', 'page:close-file', 'page:ask-selection'])
    expect(rows.map((r) => r.disabled ?? false)).toEqual([false, false, false, false])
    expect(rows[0]!.label).toBe('Keys → Neovim')
    rows[0]!.run()
    expect(acts.setKeymap).toHaveBeenCalledWith('nvim')
    rows[1]!.run()
    expect(acts.save).toHaveBeenCalled()
    rows[2]!.run()
    expect(acts.close).toHaveBeenCalledWith('tab-1')
    rows[3]!.run()
    expect(acts.askAgent).toHaveBeenCalled()
    expect(rows[1]!.kbds).toEqual(['ctrl', 'S'])
    expect(rows[2]!.kbds).toEqual(['ctrl', 'W'])
  })

  it('Keys → default from Neovim; blocked while a tab is unsaved, or when Neovim is not offered, with the editor’s words', () => {
    expect(editorRows({ ...base, keymap: 'nvim' }, acts)[0]).toMatchObject({ label: 'Keys → default', disabled: false })
    const blocked = editorRows({ ...base, switchBlocked: 'Save or discard the changes first: the other keymap would not have them' }, acts)[0]!
    expect(blocked.disabled).toBe(true)
    expect(blocked.detail).toBe('Save or discard the changes first: the other keymap would not have them')
    const notOffered = editorRows({ ...base, nvimOffered: false, nvimWhy: 'Neovim is not offered on this page' }, acts)[0]!
    expect(notOffered).toMatchObject({ disabled: true, detail: 'Neovim is not offered on this page' })
    // Back to Monaco's keys needs no Neovim.
    expect(editorRows({ ...base, keymap: 'nvim', nvimOffered: false, nvimWhy: 'gone' }, acts)[0]!.disabled).toBe(false)
  })

  it('Save and Ask the agent say why they will not act', () => {
    const rows = editorRows({ ...base, saveable: false, saveWhy: 'Nothing to save', canAsk: false, hasSelection: false }, acts)
    expect(rows[1]).toMatchObject({ disabled: true, detail: 'Nothing to save' })
    expect(rows[3]).toMatchObject({ disabled: true, detail: 'The agent cannot be asked from here' })
    expect(editorRows({ ...base, hasSelection: false }, acts)[3]).toMatchObject({ disabled: true, detail: 'Select some lines first' })
  })
})
