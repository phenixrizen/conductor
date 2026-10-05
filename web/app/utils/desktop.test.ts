import { describe, expect, it } from 'vitest'
import { desktopBridge, noticeWords, tokenFromFragment, withoutTokenFragment } from './desktop'

describe('desktop bridge', () => {
  it('is absent in a browser', () => {
    expect(desktopBridge()).toBeNull()
  })
  it('reads a token out of the fragment and drops it from the URL', () => {
    expect(tokenFromFragment('#token=abcdefghijklmnop0123456789')).toBe('abcdefghijklmnop0123456789')
    expect(tokenFromFragment('#other=1&token=abcdefghijklmnop0123456789')).toBe('abcdefghijklmnop0123456789')
    expect(tokenFromFragment('#token=short')).toBe('')
    expect(tokenFromFragment('')).toBe('')
    expect(tokenFromFragment('#token=../evil')).toBe('')
    expect(withoutTokenFragment('http://127.0.0.1:4312/#token=abcdefghijklmnop0123456789')).toBe('http://127.0.0.1:4312/')
    expect(withoutTokenFragment('http://127.0.0.1:4312/wall#other=1&token=abcdefghijklmnop0123456789')).toBe('http://127.0.0.1:4312/wall#other=1')
  })
})

describe('noticeWords', () => {
  it('words the publishing notice and nothing else', () => {
    expect(noticeWords('publishing')?.description).toContain('Settings → Switchyard')
    expect(noticeWords('')).toBeNull()
    expect(noticeWords('later')).toBeNull()
  })
})
