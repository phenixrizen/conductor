import { describe, expect, it } from 'vitest'
import { sayLine, sayWords } from './sayWords'

describe('the words after a line typed into a session (#322)', () => {
  it('typed, when the owner said the line was entered; the draft goes', () => {
    const r = sayWords('docs', 'looks good', { typed: true })
    expect(r.toast).toEqual({ title: 'Typed into docs', description: "looks good · entered, the session's owner says", color: 'success' })
    expect(r.keepDraft).toBe(false)
  })

  it('sent, to an older owner that says nothing', () => {
    const r = sayWords('docs', 'looks good', { typed: false })
    expect(r.toast.title).toBe('Sent to docs')
    expect(r.toast.description).toContain('nothing confirms it landed')
    expect(r.toast.color).toBe('neutral')
    expect(r.keepDraft).toBe(false)
  })

  it('not sent, with why, the line kept for the field', () => {
    const r = sayWords('docs', 'looks good', { refused: 'waiting on a prompt' })
    expect(r.toast).toEqual({ title: 'docs: not sent', description: "waiting on a prompt · the line waits in the row's field", color: 'warning' })
    expect(r.keepDraft).toBe(true)
  })

  it('cuts a long line in the toast', () => {
    const long = 'x'.repeat(100)
    expect(sayLine(long)).toBe(`${'x'.repeat(80)}…`)
    expect(sayWords('docs', long, { typed: true }).toast.description.startsWith(`${'x'.repeat(80)}…`)).toBe(true)
  })
})
