import { describe, expect, it } from 'vitest'
import { noInstallText, snippetLeft } from './integrations'

describe('noInstallText', () => {
  it('says there is nothing to install for an adapter without a file, home or not', () => {
    expect(noInstallText({ installable: false, launchInjection: true }, false)).toMatch(/^Nothing to install: this server wires it at launch/)
    expect(noInstallText({ installable: false, launchInjection: false }, true)).toBe('Nothing to install: paste the snippet.')
  })
  it('blames the missing home only for an adapter that has a file', () => {
    expect(noInstallText({ installable: true, launchInjection: false }, false)).toMatch(/no home directory/)
  })
})

describe('snippetLeft', () => {
  it('opens the snippet when the reply to an install carries it', () => {
    expect(snippetLeft({ code: 'no_file_route', details: { snippet: '{"hooks":{}}' } })).toBe(true)
  })
  it('keeps it shut for a skill file left by hand, which comes without one, or for another error', () => {
    expect(snippetLeft({ code: 'no_file_route', details: {} })).toBe(false)
    expect(snippetLeft({ code: 'no_file_route', details: { snippet: '' } })).toBe(false)
    expect(snippetLeft({ code: 'install_failed', details: { snippet: 'x' } })).toBe(false)
  })
})
