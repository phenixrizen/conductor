import { describe, expect, it } from 'vitest'
import { EventEmitter } from 'node:events'
import { UpdateNotice, compareVersions, failedAtStart, installFailed, installVia, restartToInstall, updateChannel, watchUpdater } from '../src/updater'

describe('update channel', () => {
  it('updates itself on macOS, Windows and AppImage, points at the releases for deb and rpm, and never in development', () => {
    expect(updateChannel('darwin', false, true)).toBe('auto')
    expect(updateChannel('win32', false, true)).toBe('auto')
    expect(updateChannel('linux', true, true)).toBe('auto')
    expect(updateChannel('linux', false, true)).toBe('link')
    expect(updateChannel('linux', true, false)).toBe('none')
  })
})

/** A notice over a store of the version said last, as the settings keep it across starts. */
function notice(store: { announced: string }) {
  return new UpdateNotice(
    () => store.announced,
    (v) => (store.announced = v),
  )
}

describe('the update notice (#290)', () => {
  it('says an update once, to the first page that asks, however many checks find it', () => {
    const store = { announced: '' }
    const n = notice(store)
    expect(n.take()).toEqual({ ready: '', announce: false })
    n.downloaded('0.7.0-rc.12')
    expect(n.take()).toEqual({ ready: '0.7.0-rc.12', announce: true })
    expect(store.announced).toBe('0.7.0-rc.12')
    // Another page, and the checks six hours apart that find the same download.
    expect(n.take()).toEqual({ ready: '0.7.0-rc.12', announce: false })
    for (let i = 0; i < 4; i++) n.downloaded('0.7.0-rc.12')
    expect(n.take()).toEqual({ ready: '0.7.0-rc.12', announce: false })
  })

  it('says it to a page that asks later when none was there to ask, and never again after a restart', () => {
    const store = { announced: '' }
    const first = notice(store)
    first.downloaded('0.7.0-rc.12')
    first.downloaded('0.7.0-rc.12')
    expect(store.announced).toBe('')
    // The app quit before a page asked: the next start finds the download and owes it still.
    const next = notice(store)
    next.downloaded('0.7.0-rc.12')
    expect(next.take().announce).toBe(true)
    const after = notice(store)
    after.downloaded('0.7.0-rc.12')
    expect(after.take()).toEqual({ ready: '0.7.0-rc.12', announce: false })
  })

  it('says a newer version again', () => {
    const store = { announced: '0.7.0-rc.12' }
    const n = notice(store)
    n.downloaded('0.7.0-rc.12')
    expect(n.take().announce).toBe(false)
    n.downloaded('0.7.0')
    expect(n.take()).toEqual({ ready: '0.7.0', announce: true })
    expect(store.announced).toBe('0.7.0')
  })

  it('offers nothing once the update downloaded is gone, and says the next one', () => {
    const store = { announced: '' }
    const n = notice(store)
    n.downloaded('0.7.0-rc.12')
    expect(n.ready()).toBe('0.7.0-rc.12')
    n.withdraw()
    expect(n.ready()).toBe('')
    expect(n.take()).toEqual({ ready: '', announce: false })
    expect(store.announced).toBe('')
    n.downloaded('0.7.0-rc.13')
    expect(n.take()).toEqual({ ready: '0.7.0-rc.13', announce: true })
  })

  it('knows an install that did not take: the version downloaded is newer than the one running', () => {
    expect(installFailed('0.7.0-rc.12', '0.7.0-rc.11')).toBe(true)
    expect(installFailed('0.7.0-rc.12', '0.7.0-rc.12')).toBe(false)
    expect(installFailed('0.7.0-rc.12', '0.7.0')).toBe(false)
    expect(installFailed('', '0.7.0-rc.11')).toBe(false)
    expect(installFailed('not a version', '0.7.0-rc.11')).toBe(false)
  })

  it('says at the start an update downloaded that did not install, told or not, once', () => {
    const s = { updateDownloaded: '0.7.0-rc.12', updateFailedNoticed: '' }
    // Never announced (no page asked: the window hidden), tried from the tray, and the app came back as the version before.
    expect(failedAtStart(s, '0.7.0-rc.11')).toBe('0.7.0-rc.12')
    expect(failedAtStart({ ...s, updateFailedNoticed: '0.7.0-rc.12' }, '0.7.0-rc.11')).toBe('')
    expect(failedAtStart(s, '0.7.0-rc.12')).toBe('')
    // Announced, then gone (a check found a newer one, or a download failed): the updater held nothing to install, so nothing failed.
    expect(failedAtStart({ updateDownloaded: '', updateFailedNoticed: '' }, '0.7.0-rc.11')).toBe('')
  })

  it('orders versions as semver does', () => {
    const order = ['0.6.9', '0.7.0-rc.2', '0.7.0-rc.9', '0.7.0-rc.11', '0.7.0-rc.12', '0.7.0', 'v0.7.1', '1.0.0-alpha', '1.0.0-alpha.1', '1.0.0-beta', '1.0.0']
    for (let i = 0; i + 1 < order.length; i++) {
      expect(compareVersions(order[i]!, order[i + 1]!), `${order[i]} before ${order[i + 1]}`).toBeLessThan(0)
      expect(compareVersions(order[i + 1]!, order[i]!)).toBeGreaterThan(0)
      expect(compareVersions(order[i]!, order[i]!)).toBe(0)
    }
    expect(compareVersions('0.7.0+build.5', '0.7.0')).toBe(0)
  })
})

/**
 * An updater as electron-updater's BaseUpdater behaves: quitAndInstall either quits the app (here: says so) or, when the
 * install fails, emits error and returns, throwing nothing.
 */
class FakeUpdater extends EventEmitter {
  constructor(private readonly outcome: 'quits' | 'fails' | 'fails later' | 'hangs') {
    super()
  }
  quits = 0
  quitAndInstall(): void {
    if (this.outcome === 'quits') this.quits++
    else if (this.outcome === 'fails') this.emit('error', new Error('EACCES: permission denied, open the installer'))
    else if (this.outcome === 'fails later') setTimeout(() => this.emit('error', new Error('Could not get code signature for running application')), 10)
  }
}

describe('installing through the updater (#290)', () => {
  it('rejects with the error the updater emits, at once or later, and never with a listener left behind', async () => {
    for (const outcome of ['fails', 'fails later'] as const) {
      const u = new FakeUpdater(outcome)
      await expect(installVia(u, 1000)).rejects.toThrow(/EACCES|code signature/)
      expect(u.listenerCount('error')).toBe(0)
    }
  })

  it('rejects an install that neither quits nor fails in time', async () => {
    const u = new FakeUpdater('hangs')
    await expect(installVia(u, 30)).rejects.toThrow('the app did not quit to install within 0 s')
    expect(u.listenerCount('error')).toBe(0)
  })

  it('settles nothing for an install that quits the app', async () => {
    const u = new FakeUpdater('quits')
    const settled = await Promise.race([installVia(u, 1000).then(() => 'settled', () => 'settled'), new Promise((r) => setTimeout(() => r('pending'), 50))])
    expect(settled).toBe('pending')
    expect(u.quits).toBe(1)
  })
})

/** An updater's events as electron-updater emits them: a check that finds a version, its download done, an error. */
class Events extends EventEmitter {
  found(version: string) {
    this.emit('update-available', { version })
  }
  downloaded(version: string) {
    this.emit('update-downloaded', { version })
  }
  failed(message: string) {
    this.emit('error', new Error(message))
  }
}

describe('what the updater holds to install (#290)', () => {
  function watched() {
    const u = new Events()
    const heard: string[] = []
    watchUpdater(u, { downloaded: (v) => heard.push(`downloaded ${v}`), gone: (why) => heard.push(`gone: ${why}`) }, (l) => heard.push(`log: ${l}`))
    return { u, heard }
  }

  it('holds the update downloaded through the checks that find it cached again', () => {
    const { u, heard } = watched()
    u.found('0.7.0-rc.12')
    u.downloaded('0.7.0-rc.12')
    for (let i = 0; i < 3; i++) {
      u.found('0.7.0-rc.12')
      u.downloaded('0.7.0-rc.12')
    }
    expect(heard).toEqual(Array(4).fill('downloaded 0.7.0-rc.12'))
  })

  // electron-updater empties its cache as a newer version's download begins: the one held is gone whether that download
  // succeeds or fails.
  it('lets the update go once a check finds a newer one, and holds the newer one downloaded', () => {
    const { u, heard } = watched()
    u.found('0.7.0-rc.12')
    u.downloaded('0.7.0-rc.12')
    u.found('0.7.0-rc.13')
    expect(heard).toEqual(['downloaded 0.7.0-rc.12', 'gone: a check found 0.7.0-rc.13'])
    u.failed('net::ERR_CONNECTION_RESET')
    expect(heard.slice(2)).toEqual(['log: the download failed: net::ERR_CONNECTION_RESET'])
    u.found('0.7.0-rc.13')
    u.downloaded('0.7.0-rc.13')
    expect(heard.slice(3)).toEqual(['downloaded 0.7.0-rc.13'])
  })

  it('lets the update go when downloading it again fails, and holds on through a check or an install that fails', () => {
    const { u, heard } = watched()
    u.found('0.7.0-rc.12')
    u.downloaded('0.7.0-rc.12')
    // A check that fails, and an install, emit error with no download under way.
    u.failed('getaddrinfo ENOTFOUND github.com')
    u.failed('EACCES: permission denied, open the installer')
    expect(heard).toEqual(['downloaded 0.7.0-rc.12'])
    // The cache gone (cleaned away), the same version is downloaded again, and that fails.
    u.found('0.7.0-rc.12')
    u.failed('sha512 checksum mismatch')
    expect(heard.slice(1)).toEqual(['log: the download failed: sha512 checksum mismatch', 'gone: its download failed'])
  })

  it('says a failed download with none held, and lets nothing go', () => {
    const { u, heard } = watched()
    u.found('0.7.0-rc.12')
    u.failed('net::ERR_INTERNET_DISCONNECTED')
    expect(heard).toEqual(['log: the download failed: net::ERR_INTERNET_DISCONNECTED'])
  })
})

describe('restart to install (#290)', () => {
  function app(install: () => Promise<void>, ready = '0.7.0-rc.12') {
    const steps: string[] = []
    return {
      steps,
      d: {
        ready: () => ready,
        letGo: () => void steps.push('let go'),
        stopServer: async () => void steps.push('stop server'),
        install: () => {
          steps.push('install')
          return install()
        },
        quit: () => void steps.push('quit'),
        log: (l: string) => steps.push(l),
      },
    }
  }

  it('stops the server, lets the windows close and the quit through, then installs', async () => {
    const a = app(() => new Promise(() => {}))
    expect(restartToInstall('0.7.0-rc.12', a.d).started).toBe(true)
    await new Promise((r) => setTimeout(r, 20))
    expect(a.steps).toEqual(['updates: restarting to install 0.7.0-rc.12', 'let go', 'stop server', 'install'])
  })

  // A check that found a newer version, or a download that failed, took the update offered away: a restart would stop the
  // server and its sessions and quit with nothing to install.
  it('stops nothing for a version the updater no longer holds', async () => {
    for (const [held, asked, why] of [
      ['', '0.7.0-rc.12', 'no update is ready'],
      ['0.7.0-rc.13', '0.7.0-rc.12', 'the update ready is 0.7.0-rc.13'],
      ['0.7.0-rc.12', '', 'the update ready is 0.7.0-rc.12'],
    ] as const) {
      const a = app(() => Promise.reject(new Error('never asked')), held)
      const r = restartToInstall(asked, a.d)
      await r.done
      expect(r.started).toBe(false)
      expect(a.steps).toEqual([`updates: no restart to update to ${asked || 'no version'}: ${why}`])
    }
  })

  // Never a server started again, which what the updater still has queued (a quit, a late install) would stop unasked: the
  // app quits, and the next start says the update did not install.
  it('quits all the same when the updater emits an error, or the install neither quits nor fails in time', async () => {
    const failed = app(() => installVia(new FakeUpdater('fails'), 1000))
    await restartToInstall('0.7.0-rc.12', failed.d).done
    expect(failed.steps).toEqual(['updates: restarting to install 0.7.0-rc.12', 'let go', 'stop server', 'install', 'updates: the install did not go through: EACCES: permission denied, open the installer; quitting', 'quit'])
    const stuck = app(() => installVia(new FakeUpdater('hangs'), 30))
    await restartToInstall('0.7.0-rc.12', stuck.d).done
    expect(stuck.steps).toEqual(['updates: restarting to install 0.7.0-rc.12', 'let go', 'stop server', 'install', 'updates: the install did not go through: the app did not quit to install within 0 s; quitting', 'quit'])
  })
})
