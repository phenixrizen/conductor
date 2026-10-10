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
      const sent: Array<{ inv: Invite; id: number }> = []
      const loaded: Invite[] = []
      const timers: Array<{ f: () => void; ms: number; cleared: boolean }> = []
      const state = { loading: false, asked: 0 }
      const d = new InviteDelivery({
        send: (inv, id) => sent.push({ inv, id }),
        load: (inv) => loaded.push(inv),
        ask: () => state.asked++,
        loading: () => state.loading,
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
      const live = () => timers.filter((t) => !t.cleared)
      return { d, sent, loaded, timers, fire, live, state }
    }

    it('sends to a page that listens, at once and without a load, and is done when the page takes it', () => {
      const h = harness()
      h.d.pageReady()
      expect(h.d.deliver(a)).toBe('sent')
      expect(h.sent.map((s) => s.inv)).toEqual([a])
      expect(h.live()).toHaveLength(1)
      h.d.pageTook(h.sent[0]!.id)
      expect(h.live()).toHaveLength(0)
      h.fire()
      expect(h.loaded).toEqual([])
    })

    it('keeps an invite for a page still loading and sends it when the page says it listens', () => {
      const h = harness()
      expect(h.d.deliver(a)).toBe('waiting')
      expect(h.sent).toEqual([])
      h.d.pageReady()
      expect(h.sent.map((s) => s.inv)).toEqual([a])
      h.d.pageTook(h.sent[0]!.id)
      h.fire()
      expect(h.loaded).toEqual([])
    })

    it('stops sending once a navigation starts, until the next page says it listens; the latest invite wins', () => {
      const h = harness()
      h.d.pageReady()
      h.d.pageLeft()
      expect(h.d.deliver(a)).toBe('waiting')
      expect(h.d.deliver(b)).toBe('waiting')
      expect(h.live()).toHaveLength(1)
      h.d.pageReady()
      expect(h.sent.map((s) => s.inv)).toEqual([b])
      // The first invite's id is no longer the one pending.
      h.d.pageTook(h.sent[0]!.id - 1)
      expect(h.live()).toHaveLength(1)
      h.d.pageTook(h.sent[0]!.id)
      expect(h.live()).toHaveLength(0)
    })

    it('counts nothing a page says while the main frame loads, and asks again once it has loaded', () => {
      const h = harness()
      h.d.pageReady()
      h.d.pageLeft()
      h.state.loading = true
      h.d.deliver(a)
      // The page being replaced says it listens late: nothing goes to it.
      h.d.pageReady()
      expect(h.sent).toEqual([])
      h.state.loading = false
      h.d.settled()
      expect(h.state.asked).toBe(1)
      h.d.pageReady()
      expect(h.sent.map((s) => s.inv)).toEqual([a])
    })

    it('keeps an invite the outgoing page took while the next one loaded, and sends it to the new page', () => {
      const h = harness()
      h.d.pageReady()
      h.d.deliver(a)
      expect(h.sent).toHaveLength(1)
      // A navigation starts before the page answers; its "took" arrives while the next document loads.
      h.d.pageLeft()
      h.state.loading = true
      h.d.pageTook(h.sent[0]!.id)
      expect(h.live()).toHaveLength(1)
      h.state.loading = false
      h.d.settled()
      h.d.pageReady()
      expect(h.sent).toHaveLength(2)
      expect(h.sent[1]!.inv).toEqual(a)
      h.d.pageTook(h.sent[1]!.id)
      h.fire()
      expect(h.loaded).toEqual([])
    })

    it('loads the join page for an invite the page received but never took (it refused it)', () => {
      const h = harness()
      h.d.pageReady()
      h.d.deliver(a)
      expect(h.sent).toHaveLength(1)
      expect(h.timers[0]?.ms).toBe(INVITE_ACK_MS)
      h.fire()
      expect(h.loaded).toEqual([a])
    })

    it('loads the join page when the page never says it listens, and asks nobody once nothing is pending', () => {
      const h = harness()
      h.d.deliver(a)
      h.fire()
      expect(h.loaded).toEqual([a])
      expect(h.sent).toEqual([])
      // Nothing left over for the loaded page.
      h.d.settled()
      expect(h.state.asked).toBe(0)
      h.d.pageReady()
      expect(h.sent).toEqual([])
    })
  })
})
