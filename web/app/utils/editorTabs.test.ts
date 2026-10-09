import { describe, expect, it } from 'vitest'
import { activateTab, closeAllTabs, closeTab, cycleTab, editorHeight, emptyTabs, openTab, readSplit, SPLIT_DEFAULT, SPLIT_KEY, tabTitle, takeLine, toggleFold, writeSplit } from './editorTabs'

describe('the editor tabs', () => {
  it('opens one tab per file, brings an open one to the front at the line asked for, and unfolds', () => {
    let s = openTab(emptyTabs(), 'file', '/r/internal/api/users.go', 14)
    expect(s.tabs.map((t) => t.title)).toEqual(['users.go'])
    expect(s.active).toBe('file:/r/internal/api/users.go')
    s = openTab(s, 'file', '/r/README.md')
    s = toggleFold(s)
    expect(s.folded).toBe(true)
    s = openTab(s, 'file', '/r/internal/api/users.go', 20)
    expect(s.tabs).toHaveLength(2)
    expect(s.active).toBe('file:/r/internal/api/users.go')
    expect(s.tabs[0]!.line).toBe(20)
    expect(s.folded).toBe(false)
    expect(takeLine(s, s.active!).tabs[0]!.line).toBeUndefined()
    const d = openTab(emptyTabs(), 'diff', '/r/a.go', undefined, { status: 'M', added: 3, removed: 1, against: 'working directory vs HEAD' })
    expect(d.tabs[0]).toMatchObject({ id: 'diff:/r/a.go', kind: 'diff', title: 'a.go', meta: { status: 'M', added: 3, removed: 1 } })
    expect(openTab(d, 'diff', '/r/a.go').tabs[0]!.meta?.added).toBe(3)
    expect(tabTitle('url', 'http://localhost:3000/v1/users?limit=5')).toBe('localhost:3000')
    expect(tabTitle('url', 'not a url')).toBe('not a url')
  })

  it('closes a tab and gives its place to the next one, else the one before; the last close empties the area', () => {
    let s = openTab(openTab(openTab(emptyTabs(), 'file', '/a'), 'file', '/b'), 'file', '/c')
    s = activateTab(s, 'file:/b')
    s = closeTab(s, 'file:/b')
    expect(s.tabs.map((t) => t.path)).toEqual(['/a', '/c'])
    expect(s.active).toBe('file:/c')
    s = closeTab(s, 'file:/c')
    expect(s.active).toBe('file:/a')
    s = closeTab(s, 'file:/zzz')
    expect(s.tabs).toHaveLength(1)
    s = closeTab(s, 'file:/a')
    expect(s).toEqual(emptyTabs())
    expect(closeAllTabs()).toEqual(emptyTabs())
    expect(activateTab(s, 'file:/nope')).toBe(s)
  })

  it('cycles with Ctrl+Tab round the end, both ways', () => {
    let s = openTab(openTab(openTab(emptyTabs(), 'file', '/a'), 'file', '/b'), 'file', '/c')
    s = cycleTab(s)
    expect(s.active).toBe('file:/a')
    s = cycleTab(s, -1)
    expect(s.active).toBe('file:/c')
    expect(cycleTab(openTab(emptyTabs(), 'file', '/a')).active).toBe('file:/a')
    expect(toggleFold(emptyTabs()).folded).toBe(false)
  })

  it('keeps the split per browser within its ends, and leaves the terminal its six lines', () => {
    const store = new Map<string, string>()
    const storage = { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) }
    expect(readSplit(storage)).toBe(SPLIT_DEFAULT)
    writeSplit(storage, 0.95)
    expect(store.get(SPLIT_KEY)).toBe('0.85')
    writeSplit(storage, 0.05)
    expect(readSplit(storage)).toBe(0.2)
    expect(readSplit(null)).toBe(SPLIT_DEFAULT)
    // 900 px tall, 20 px lines, 60 px reserved: 60 % is 540 and fits; 85 % does not, the terminal keeps 120.
    expect(editorHeight(900, 0.6, 20, 60)).toBe(540)
    expect(editorHeight(900, 0.85, 20, 60)).toBe(720)
    expect(editorHeight(200, 0.6, 20, 60)).toBe(20)
  })
})

describe('the keymap setting', () => {
  it('reads nvim or default and writes what was chosen', async () => {
    const { readKeymap, writeKeymap, KEYMAP_KEY, nvimUnavailableWords } = await import('./editorTabs')
    const store = new Map<string, string>()
    const storage = { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v) }
    expect(readKeymap(storage)).toBe('default')
    writeKeymap(storage, 'nvim')
    expect(store.get(KEYMAP_KEY)).toBe('nvim')
    expect(readKeymap(storage)).toBe('nvim')
    store.set(KEYMAP_KEY, 'vim')
    expect(readKeymap(storage)).toBe('default')
    expect(readKeymap(null)).toBe('default')
    expect(readKeymap({ getItem: () => { throw new Error('no') } })).toBe('default')
    expect(nvimUnavailableWords({ welcome: true, nvim: true, fileEdit: true })).toBe('')
    expect(nvimUnavailableWords({ welcome: true, nvim: false, fileEdit: true, machine: 'lan-box' })).toBe('Neovim is not installed on lan-box')
    expect(nvimUnavailableWords({ welcome: true, nvim: true, fileEdit: false })).toMatch(/View only/)
    expect(nvimUnavailableWords({ welcome: false, nvim: true, fileEdit: true })).toBe('Not connected')
  })
})
