import { describe, expect, it } from 'vitest'
import { INVITE_ACK_MS, InviteDelivery, inviteInArgv, invitePath, parseInvite, type Invite } from '../src/invite'

const tok = 'MGzwgDXWv1sbiMNn3L9Lk7ejmTGy6M2u-5WkRjcyvdA'

describe('invite', () => {
  it('parses an invite, finds one in argv and maps it to the join page', () => {
    expect(parseInvite(`conductor://switchyard.example.net/join/${tok}`)).toEqual({ server: 'https://switchyard.example.net', token: tok })
    expect(parseInvite(`conductor://127.0.0.1:18424/join/${tok}?http=1`)).toEqual({ server: 'http://127.0.0.1:18424', token: tok })
    expect(parseInvite(`conductor://switchyard.example.net/join/${tok}?http=1`)).toBeNull()
    expect(parseInvite('conductor://x/join/%ZZ')).toBeNull()
    expect(inviteInArgv(['C:\\Conductor\\Conductor.exe', '--allow-file-access', `conductor://switchyard.example.net/join/${tok}`])).toEqual({ server: 'https://switchyard.example.net', token: tok })
    expect(inviteInArgv(['Conductor.exe'])).toBeNull()
    expect(invitePath({ server: 'https://switchyard.example.net', token: tok })).toBe(`/join/${tok}?server=https%3A%2F%2Fswitchyard.example.net`)
  })

  describe('delivery to the open window', () => {
    const a: Invite = { server: 'https://switchyard.example.net', token: tok }
    const b: Invite = { server: 'https://other.example.net', token: tok.replace('M', 'N') }
    function harness() {
      const sent: Invite[] = []
      const loaded: Invite[] = []
      const timers: Array<{ f: () => void; ms: number; cleared: boolean }> = []
      const d = new InviteDelivery({
        send: (inv) => sent.push(inv),
        load: (inv) => loaded.push(inv),
        setTimer: (f, ms) => {
          const t = { f, ms, cleared: false }
          timers.push(t)
          return t
        },
        clearTimer: (t) => {
          ;(t as { cleared: boolean }).cleared = true
        },
      })
      const fire = () => timers.filter((t) => !t.cleared).forEach((t) => t.f())
      return { d, sent, loaded, timers, fire }
    }

    it('sends to a page that listens, at once and without a load', () => {
      const h = harness()
      h.d.pageReady()
      expect(h.d.deliver(a)).toBe('sent')
      expect(h.sent).toEqual([a])
      expect(h.loaded).toEqual([])
      expect(h.timers).toHaveLength(0)
    })

    it('keeps an invite for a page still loading and sends it when the page says it listens', () => {
      const h = harness()
      expect(h.d.deliver(a)).toBe('waiting')
      expect(h.sent).toEqual([])
      h.d.pageReady()
      expect(h.sent).toEqual([a])
      expect(h.timers[0]?.cleared).toBe(true)
      h.fire()
      expect(h.loaded).toEqual([])
    })

    it('stops sending once a navigation starts, until the next page says it listens; the latest invite wins', () => {
      const h = harness()
      h.d.pageReady()
      h.d.pageLeft()
      expect(h.d.deliver(a)).toBe('waiting')
      expect(h.d.deliver(b)).toBe('waiting')
      expect(h.timers).toHaveLength(1)
      h.d.pageReady()
      expect(h.sent).toEqual([b])
    })

    it('loads the join page when the page never says it listens', () => {
      const h = harness()
      h.d.deliver(a)
      expect(h.timers[0]?.ms).toBe(INVITE_ACK_MS)
      h.fire()
      expect(h.loaded).toEqual([a])
      expect(h.sent).toEqual([])
      // Nothing left over for the loaded page.
      h.d.pageReady()
      expect(h.sent).toEqual([])
    })
  })
})
