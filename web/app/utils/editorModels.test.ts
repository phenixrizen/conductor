import { describe, expect, it } from 'vitest'
import { closeWords, conflictWords, forgetModel, hasModel, isDirty, markSaved } from './editorModels'

describe('the editor\'s saved versions', () => {
  it('knows an unsaved model by its version', () => {
    expect(isDirty('/r/a.go', 3)).toBe(false)
    markSaved('/r/a.go', 1)
    expect(hasModel('/r/a.go')).toBe(true)
    expect(isDirty('/r/a.go', 1)).toBe(false)
    expect(isDirty('/r/a.go', 2)).toBe(true)
    markSaved('/r/a.go', 2)
    expect(isDirty('/r/a.go', 2)).toBe(false)
    forgetModel('/r/a.go')
    expect(hasModel('/r/a.go')).toBe(false)
    expect(isDirty('/r/a.go', 9)).toBe(false)
  })
})

describe('the words', () => {
  it('say what changed the file on disk', () => {
    expect(conflictWords({ at: '2026-10-09T08:34:10Z', by: 'codex', tool: 'Edit', exists: true })).toMatch(/^Changed on disk since you opened it · \d{2}:\d{2}:\d{2} · codex \(Edit\)\. Saving would overwrite that\.$/)
    expect(conflictWords({ mtime: 'garbage', exists: true })).toBe('Changed on disk since you opened it. Saving would overwrite that.')
    expect(conflictWords({ exists: false })).toBe('Deleted on disk since you opened it. Saving would write it again.')
    expect(conflictWords(null)).toBe('')
  })
  it('ask before closing unsaved tabs', () => {
    expect(closeWords(['users.go'])).toEqual({ title: 'Save users.go?', words: "Your changes are lost if you don't save them." })
    expect(closeWords(['a.go', 'b.go']).title).toBe('Close 2 files with unsaved changes?')
  })
})
