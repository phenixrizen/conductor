import { describe, expect, it } from 'vitest'
import { UpdateNotice, compareVersions, installFailed, updateChannel } from '../src/updater'

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

  it('knows an install that did not take: the version announced is newer than the one running', () => {
    expect(installFailed('0.7.0-rc.12', '0.7.0-rc.11')).toBe(true)
    expect(installFailed('0.7.0-rc.12', '0.7.0-rc.12')).toBe(false)
    expect(installFailed('0.7.0-rc.12', '0.7.0')).toBe(false)
    expect(installFailed('', '0.7.0-rc.11')).toBe(false)
    expect(installFailed('not a version', '0.7.0-rc.11')).toBe(false)
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
