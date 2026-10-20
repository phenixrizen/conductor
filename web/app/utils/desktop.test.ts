import { describe, expect, it } from 'vitest'
import { desktopBridge, noticeWords, PLAIN_SWITCHYARD, SWITCHYARD_EXTRA, switchyardProblem, tokenFromFragment, withoutTokenFragment, restartWords, updateBusyWords, updateFailedWords, updateGoneWords, updateReadyWords } from './desktop'

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

// The Settings page's check before a save: the words the app's validate
// says (desktop/src/settings.ts PLAIN_SWITCHYARD, SWITCHYARD_EXTRA), for the
// same switchyards.
describe('switchyardProblem', () => {
  const words = 'the switchyard must be https:// unless it runs on this machine (localhost): over plain http:// what is published there travels in the clear and unverified'
  it('refuses a plain switchyard on another machine with the words why', () => {
    expect(PLAIN_SWITCHYARD).toBe(words)
    for (const server of ['http://yard.example:8080', 'http://192.168.1.10:8080', 'HTTP://yard.example', 'http://localhost.yard.example', 'http://127.0.0.1.yard.example', 'http://127.1:8080', 'http://0x7f.0.0.1', 'http://127.000.0.1', 'http://0.0.0.0:8080', 'http://[fe80::1]:8080', 'http://LOCALHOST:8080', 'http://LocalHost:8080/']) {
      expect(switchyardProblem({ switchyardEnabled: true, switchyardServer: server }), server).toBe(words)
    }
  })
  it('keeps one on this machine, an https one anywhere, and anything while publishing is off', () => {
    for (const server of ['', 'http://localhost:8080', 'http://localhost:8080/', 'http://127.0.0.1:8080', 'http://127.20.30.40', 'http://[::1]:8080', 'http://[::ffff:127.0.0.1]:8080', 'https://yard.example', 'https://192.168.1.10:8443', 'not a url']) {
      expect(switchyardProblem({ switchyardEnabled: true, switchyardServer: server }), server).toBe('')
    }
    expect(switchyardProblem({ switchyardEnabled: false, switchyardServer: 'http://yard.example' })).toBe('')
  })
  it('refuses one named with more than its address, before the plain http rule, and nothing of it while publishing is off', () => {
    const extra = 'the switchyard must be named by its address alone, with no user name, password, query or fragment; a host token has a field of its own'
    expect(SWITCHYARD_EXTRA).toBe(extra)
    for (const server of ['https:///me:pw@yard.example', 'https://me:pw@yard.example', 'https://me@yard.example', 'https://:pw@yard.example', 'https://@yard.example', 'https://:@yard.example', 'https://yard.example/?token=t', 'https://yard.example?', 'https://yard.example/#f', 'https://yard.example#', 'http://me:pw@localhost:9090', 'http://:pw@yard.example', 'http://127.0.0.1\\@yard.example']) {
      expect(switchyardProblem({ switchyardEnabled: true, switchyardServer: server }), server).toBe(extra)
      expect(switchyardProblem({ switchyardEnabled: false, switchyardServer: server }), server).toBe('')
    }
    for (const server of ['https://yard.example/prefix/', 'https://yard.example/team@two']) {
      expect(switchyardProblem({ switchyardEnabled: true, switchyardServer: server }), server).toBe('')
    }
    // One the server would not read as written is the app's validate's to name as it saves: an extra slash, a space, a backslash.
    for (const server of ['https:///yard.example', ' http://yard.example', 'http://yard.example ', 'http:\\\\yard.example']) {
      expect(switchyardProblem({ switchyardEnabled: true, switchyardServer: server }), server).toBe('')
    }
  })
})

describe('noticeWords', () => {
  it('words the publishing notice and nothing else', () => {
    const words = noticeWords('publishing')
    expect(words?.title).toBe('Sessions are published to a switchyard')
    expect(words?.description).toContain('switchyard.rslabs.net')
    expect(words?.description).toContain('whose operator you trust')
    expect(words?.description).toContain('Settings → Switchyard')
    expect(noticeWords('')).toBeNull()
    expect(noticeWords('later')).toBeNull()
  })
})

describe('the update the desktop app downloaded (#290)', () => {
  it('says it is ready, and that it installs on quit or by a restart', () => {
    expect(updateReadyWords('0.7.0-rc.12')).toEqual({ title: 'Conductor 0.7.0-rc.12 is ready', description: 'It installs when you quit Conductor. Restart to update now, or later.' })
  })

  it('says an update that did not install, and where to look', () => {
    const w = updateFailedWords('0.7.0-rc.12')
    expect(w.title).toBe('Conductor 0.7.0-rc.12 did not install')
    expect(w.description).toContain("The app's log says why")
  })

  it('says an update offered that the app no longer holds, and that nothing restarted', () => {
    const w = updateGoneWords('0.7.0-rc.12')
    expect(w.title).toBe('Conductor 0.7.0-rc.12 is no longer ready')
    expect(w.description).toContain('nothing restarted')
  })

  it('says a restart put off while the app checks for updates, and that nothing restarted', () => {
    expect(updateBusyWords()).toEqual({ title: 'Conductor is checking for updates', description: 'Nothing restarted. Restart to update again in a moment.' })
  })

  it('says what a restart to update ends: the server, and the sessions running on this computer', () => {
    expect(restartWords('0.7.0-rc.12', 0)).toBe('Conductor quits to install 0.7.0-rc.12, and its server stops. No session runs on this computer, so none ends.')
    expect(restartWords('0.7.0-rc.12', 1)).toBe('Conductor quits to install 0.7.0-rc.12, and its server stops. The session running on this computer ends.')
    expect(restartWords('0.7.0-rc.12', 3)).toBe('Conductor quits to install 0.7.0-rc.12, and its server stops. The 3 sessions running on this computer end.')
    // Not counted is never none.
    expect(restartWords('0.7.0-rc.12', -1)).toBe('Conductor quits to install 0.7.0-rc.12, and its server stops. The sessions running on this computer, if any, end: they could not be counted.')
  })
})
