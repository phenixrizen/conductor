import { describe, expect, it } from 'vitest'
import { noInstallText } from './integrations'

describe('noInstallText', () => {
  it('says there is nothing to install for an adapter without a file, home or not', () => {
    expect(noInstallText({ installable: false, launchInjection: true }, false)).toMatch(/^Nothing to install: this server wires it at launch/)
    expect(noInstallText({ installable: false, launchInjection: false }, true)).toBe('Nothing to install: paste the snippet.')
  })
  it('blames the missing home only for an adapter that has a file', () => {
    expect(noInstallText({ installable: true, launchInjection: false }, false)).toMatch(/no home directory/)
  })
})
