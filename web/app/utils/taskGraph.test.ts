import { describe, expect, it } from 'vitest'
import type { RunTask } from '~/composables/useSessions'
import { GAP_X, GAP_Y, NODE_H, NODE_W } from './crewGraph'
import { taskDepths, taskLayout, taskListRows } from './taskGraph'

const task = (id: string, needs?: string[]): RunTask => ({ id, member: 'lead', kind: 'work', state: 'ready', attempts: 0, needs })

describe('taskDepths', () => {
  it('puts a task one column past the deepest task it needs', () => {
    const { depth, warnings } = taskDepths([task('build'), task('test', ['build']), task('deploy', ['test', 'build']), task('docs')])
    expect([...depth]).toEqual([
      ['build', 0],
      ['test', 1],
      ['deploy', 2],
      ['docs', 0],
    ])
    expect(warnings.size).toBe(0)
  })
  it('draws a task whose need is not in the run, or a cycle, as a root with a warning', () => {
    const { depth, warnings } = taskDepths([task('a', ['zzz']), task('b', ['c']), task('c', ['b'])])
    expect(depth.get('a')).toBe(0)
    expect(warnings.get('a')).toBe('needs zzz, which the run has no task for')
    expect(depth.get('b')).toBe(0)
    expect(depth.get('c')).toBe(0)
    expect(warnings.get('b') || warnings.get('c')).toBe('needs itself, through the tasks it needs')
  })
})

describe('taskLayout', () => {
  const tasks = [task('build'), task('test', ['build']), task('deploy', ['test', 'build']), task('docs')]
  it('lays the columns out left to right with the rows down, and top to bottom the other way', () => {
    const lr = taskLayout(tasks, 'LR')
    expect(lr.nodes.map((n) => [n.id, n.depth, n.row, n.x, n.y])).toEqual([
      ['build', 0, 0, 0, 0],
      ['test', 1, 0, NODE_W + GAP_X, 0],
      ['deploy', 2, 0, 2 * (NODE_W + GAP_X), 0],
      ['docs', 0, 1, 0, NODE_H + GAP_Y],
    ])
    const tb = taskLayout(tasks, 'TB')
    expect(tb.nodes.find((n) => n.id === 'test')).toMatchObject({ x: 0, y: NODE_W + GAP_X })
    expect(tb.nodes.find((n) => n.id === 'docs')).toMatchObject({ x: NODE_H + GAP_Y, y: 0 })
  })
  it('draws a needs edge from each task to each task that needs it, none into a task with a warning', () => {
    expect(taskLayout(tasks).edges).toEqual([
      { id: 'needs:build-test', from: 'build', to: 'test', kind: 'needs' },
      { id: 'needs:test-deploy', from: 'test', to: 'deploy', kind: 'needs' },
      { id: 'needs:build-deploy', from: 'build', to: 'deploy', kind: 'needs' },
    ])
    expect(taskLayout([task('a', ['zzz']), task('b', ['a'])]).edges).toEqual([{ id: 'needs:a-b', from: 'a', to: 'b', kind: 'needs' }])
  })
  it('lists a task after the tasks it needs', () => {
    expect(taskListRows(taskLayout([task('deploy', ['test']), task('test', ['build']), task('build'), task('docs')])).map((n) => n.id)).toEqual(['build', 'docs', 'test', 'deploy'])
  })
})
