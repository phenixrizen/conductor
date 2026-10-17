import { describe, expect, it } from 'vitest'
import { SettingsSaver } from '../src/save'
import { defaultSettings, type DesktopSettings } from '../src/settings'

/** An app's settings store and supervisor for the saver: what is stored, and each restart asked. */
function app(restart: () => Promise<void> = async () => {}) {
  let stored = defaultSettings('/home/me', '/home/me/.config/conductor-desktop')
  const restarts: number[] = []
  return {
    restarts,
    stored: () => stored,
    d: {
      get: () => stored,
      set: (s: DesktopSettings) => {
        stored = s
      },
      restart: () => {
        restarts.push(restarts.length + 1)
        return restart()
      },
      posix: false,
    },
  }
}

describe('SettingsSaver', () => {
  it('saves a change the server does not read without a restart, and says saved, once', async () => {
    const s = new SettingsSaver()
    const a = app()
    await s.save(a.d, { closeToTray: false })
    expect(a.restarts).toHaveLength(0)
    expect(a.stored().closeToTray).toBe(false)
    expect(s.take()).toBe('saved')
    expect(s.take()).toBe('')
  })

  it('restarts the server for a server setting and says restarted while the restart is under way, once', async () => {
    let release!: () => void
    const s = new SettingsSaver()
    const a = app(() => new Promise<void>((r) => (release = r)))
    const saving = s.save(a.d, { yolo: true })
    // The window follows the restarted server as soon as it runs, before the restart resolves: the page that follows asks then.
    expect(a.restarts).toHaveLength(1)
    expect(s.take()).toBe('restarted')
    expect(s.take()).toBe('')
    release()
    expect((await saving).yolo).toBe(true)
    expect(s.take()).toBe('')
  })

  it('takes the outcome back when the restart fails, keeps the settings, and the retry says saved without restarting', async () => {
    let fails = true
    const s = new SettingsSaver()
    const a = app(async () => {
      if (fails) throw new Error('the server did not start')
    })
    await expect(s.save(a.d, { yolo: true })).rejects.toThrow('did not start')
    expect(a.stored().yolo).toBe(true)
    expect(s.take()).toBe('')
    fails = false
    // The same form again: nothing the server reads differs from what was kept, so nothing restarts, and the toast must not say so.
    await s.save(a.d, { yolo: true })
    expect(a.restarts).toHaveLength(1)
    expect(s.take()).toBe('saved')
  })

  it('refuses invalid settings before changing anything, and keeps the app\'s zoom level', async () => {
    const s = new SettingsSaver()
    const a = app()
    await expect(s.save(a.d, { dataDir: 'relative' })).rejects.toThrow('absolute')
    expect(a.restarts).toHaveLength(0)
    expect(a.stored().dataDir).not.toBe('relative')
    expect(s.take()).toBe('')
    await s.save(a.d, { closeToTray: false, zoomLevel: 3 } as Partial<DesktopSettings>)
    expect(a.stored().zoomLevel).toBe(0)
  })
})
