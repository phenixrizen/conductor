import { describe, expect, it } from 'vitest'
import { filesItems, inspectorItems, itemTooltip, pickItem, pillText, pollInterval } from './inspector'

describe('the inspector rail', () => {
  it('lists its items in order, Chat only when the owner takes chat', () => {
    const counts = { viewers: 2, changes: 379, commits: 9, unread: 2, chat: true }
    expect(inspectorItems(counts).map((i) => `${i.id}${i.count !== undefined ? ':' + i.count : ''}`)).toEqual(['people:2', 'explorer', 'changes:379', 'commits:9', 'activity', 'chat:2'])
    expect(inspectorItems({ ...counts, chat: false }).map((i) => i.id)).toEqual(['people', 'explorer', 'changes', 'commits', 'activity'])
    expect(filesItems({ changes: 1, commits: 0 }).map((i) => `${i.id}:${i.count}`)).toEqual(['explorer:undefined', 'changes:1', 'commits:0'])
  })

  it('caps a pill at 99+ and shows none for nothing', () => {
    expect(pillText(0)).toBe('')
    expect(pillText(undefined)).toBe('')
    expect(pillText(9)).toBe('9')
    expect(pillText(99)).toBe('99')
    expect(pillText(379)).toBe('99+')
  })

  it('names a tooltip by the view, its sub-line or its count', () => {
    const [people, explorer, changes, commits, activity, chat] = inspectorItems({ viewers: 2, changes: 379, commits: 9, unread: 2, chat: true, subs: { commits: '9 on feat/scoping-suggestions', people: '2 here now · 1 link' } })
    expect(itemTooltip(commits!)).toBe('Commits · 9 on feat/scoping-suggestions')
    expect(itemTooltip(people!)).toBe('People · 2 here now · 1 link')
    expect(itemTooltip(changes!)).toBe('Changes · 379')
    expect(itemTooltip(chat!)).toBe('Chat · 2 unread')
    expect(itemTooltip(explorer!)).toBe('Explorer')
    expect(itemTooltip(activity!)).toBe('Activity')
  })

  it('polls fast for the view shown and slowly otherwise', () => {
    expect(pollInterval('changes', 'changes')).toBe(5000)
    expect(pollInterval('commits', 'commits')).toBe(10_000)
    expect(pollInterval('changes', 'commits')).toBe(30_000)
    expect(pollInterval('commits', null)).toBe(30_000)
  })

  it('collapses on the active item and opens on any other; a rail that cannot collapse re-picks', () => {
    expect(pickItem('commits', 'commits', true)).toEqual({ view: 'commits', open: false })
    expect(pickItem('commits', 'commits', false)).toEqual({ view: 'commits', open: true })
    expect(pickItem('chat', 'commits', true)).toEqual({ view: 'chat', open: true })
    expect(pickItem('commits', 'commits', true, false)).toEqual({ view: 'commits', open: true })
  })
})
