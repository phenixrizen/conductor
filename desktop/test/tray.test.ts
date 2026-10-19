import { describe, expect, it } from 'vitest'
import { trayItems } from '../src/tray'

describe('the tray', () => {
  const calls: string[] = []
  const d = {
    show: () => {
      calls.push('show')
      return { focus: () => {} } as unknown as import('electron').BrowserWindow
    },
    openInBrowser: () => calls.push('browser'),
    restart: () => calls.push('restart'),
    showLog: () => calls.push('log'),
    quit: () => calls.push('quit'),
    installUpdate: () => calls.push('install'),
  }

  it('offers the window, the browser, the server, its log and quit', () => {
    expect(trayItems(d, '').map((i) => i.label ?? i.type)).toEqual(['Open Conductor', 'Open in browser', 'separator', 'Restart server', 'Server log', 'separator', 'Quit'])
  })

  it('offers Restart to update first while an update waits (#290)', () => {
    const items = trayItems(d, '0.7.0-rc.12')
    expect(items.map((i) => i.label ?? i.type)).toEqual(['Restart to update to 0.7.0-rc.12', 'separator', 'Open Conductor', 'Open in browser', 'separator', 'Restart server', 'Server log', 'separator', 'Quit'])
    ;(items[0]!.click as () => void)()
    expect(calls).toEqual(['install'])
  })
})
