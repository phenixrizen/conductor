import { describe, expect, it } from 'vitest'
import { CAROUSEL_DEFAULTS, dropIndex, keptOrder, keyMove, moveInOrder, normalizeSettings, orderSessions } from './carousel'

const s = (id: string) => ({ id })

describe("the Roundhouse's strip", () => {
  it('keeps the settings over the defaults, each field checked', () => {
    expect(normalizeSettings(null)).toEqual(CAROUSEL_DEFAULTS)
    expect(normalizeSettings({ intervalMs: 5000, autoplay: false, follow: false, order: ['b', 'a', 3], strip: 'thumbs' })).toEqual({ intervalMs: 5000, autoplay: false, follow: false, order: ['b', 'a'], strip: 'thumbs' })
    expect(normalizeSettings({ intervalMs: 'x', strip: 'tiles', order: 'a' })).toEqual(CAROUSEL_DEFAULTS)
    expect(normalizeSettings({ intervalMs: 10 })).toEqual(CAROUSEL_DEFAULTS)
  })

  it('orders the sessions as kept, a new one at the end, an ended one gone', () => {
    const active = [s('a'), s('b'), s('c'), s('d')]
    expect(orderSessions(active, ['c', 'a']).map((x) => x.id)).toEqual(['c', 'a', 'b', 'd'])
    expect(orderSessions(active, ['c', 'gone', 'a', 'c']).map((x) => x.id)).toEqual(['c', 'a', 'b', 'd'])
    expect(orderSessions(active, []).map((x) => x.id)).toEqual(['a', 'b', 'c', 'd'])
    expect(keptOrder(orderSessions([s('b'), s('d')], ['c', 'a', 'd']))).toEqual(['d', 'b'])
  })

  it('moves an item within the order, and leaves it for an index out of the list', () => {
    expect(moveInOrder(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd'])
    expect(moveInOrder(['a', 'b', 'c', 'd'], 3, 0)).toEqual(['d', 'a', 'b', 'c'])
    expect(moveInOrder(['a', 'b'], 1, 1)).toEqual(['a', 'b'])
    expect(moveInOrder(['a', 'b'], 5, 0)).toEqual(['a', 'b'])
  })

  it('lands a drop before or after the chip under the pointer', () => {
    expect(dropIndex(2, 0.2, 0)).toBe(1)
    expect(dropIndex(2, 0.8, 0)).toBe(2)
    expect(dropIndex(0, 0.2, 3)).toBe(0)
    expect(dropIndex(0, 0.8, 3)).toBe(1)
    expect(dropIndex(1, 0.5, 1)).toBe(1)
  })

  it('picks a chip up with Space, moves it with the arrows, drops it with Space, puts it back with Escape', () => {
    const ids = ['a', 'b', 'c']
    let st = keyMove(ids, 0, null, ' ')
    expect(st).toEqual({ ids, grabbed: 0, focused: 0 })
    st = keyMove(st.ids, st.focused, st.grabbed, 'ArrowRight')
    expect(st).toEqual({ ids: ['b', 'a', 'c'], grabbed: 1, focused: 1 })
    st = keyMove(st.ids, st.focused, st.grabbed, 'ArrowRight')
    expect(st.ids).toEqual(['b', 'c', 'a'])
    st = keyMove(st.ids, st.focused, st.grabbed, 'ArrowRight')
    expect(st.ids).toEqual(['b', 'c', 'a'])
    st = keyMove(st.ids, st.focused, st.grabbed, 'Enter')
    expect(st).toEqual({ ids: ['b', 'c', 'a'], grabbed: null, focused: 2, done: 'dropped' })
    // Nothing picked up: the arrows move nothing, Escape does nothing.
    expect(keyMove(ids, 1, null, 'ArrowLeft')).toEqual({ ids, grabbed: null, focused: 1 })
    expect(keyMove(ids, 1, null, 'Escape')).toEqual({ ids, grabbed: null, focused: 1 })
    // Escape while picked: dropped, where the caller restores the order from before the pick-up.
    expect(keyMove(['b', 'a', 'c'], 1, 1, 'Escape')).toEqual({ ids: ['b', 'a', 'c'], grabbed: null, focused: 1, done: 'cancelled' })
  })
})
