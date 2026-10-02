import { describe, expect, it } from 'vitest'
import { effectiveYolo, hasYoloRecipe, resumeLabel, yoloChoice, yoloFromChoice, yoloSummary } from './yolo'

describe('the crew editor yolo select', () => {
  it('round-trips the default, on and off', () => {
    for (const v of [undefined, true, false]) expect(yoloFromChoice(yoloChoice(v))).toBe(v)
    expect(yoloChoice(undefined)).toBe('default')
  })
})

describe('effectiveYolo', () => {
  it('is the launch or crew choice, else the server default', () => {
    expect(effectiveYolo(undefined, true)).toBe(true)
    expect(effectiveYolo(false, true)).toBe(false)
    expect(effectiveYolo(true, false)).toBe(true)
  })
})

describe('yoloSummary', () => {
  const claude = { name: 'Claude Code', command: ['claude'], yolo: { args: ['--dangerously-skip-permissions'] } }
  const copilot = { name: 'Copilot CLI', command: ['copilot'], yolo: { args: ['--yolo'], env: { COPILOT_ALLOW_ALL: 'true' } } }
  it('adds the recipe after the command and the extra arguments when on', () => {
    expect(yoloSummary(claude, true, ['--model', 'opus'])).toEqual({ argv: ['claude', '--model', 'opus', '--dangerously-skip-permissions'], env: [], applies: true, notice: '' })
    expect(yoloSummary(copilot, true).env).toEqual(['COPILOT_ALLOW_ALL'])
  })
  it('adds nothing when off', () => {
    expect(yoloSummary(claude, false)).toMatchObject({ argv: ['claude'], applies: false, notice: '' })
  })
  it('says so for an agent without a recipe, or with an empty one', () => {
    expect(hasYoloRecipe({ yolo: {} })).toBe(false)
    expect(yoloSummary({ name: 'Shell', command: ['/bin/bash', '-l'] }, true)).toMatchObject({ applies: false, notice: expect.stringContaining('Shell has no yolo recipe') })
    expect(yoloSummary({ name: 'X', command: ['x'], yolo: {} }, true).applies).toBe(false)
  })
})

describe('resumeLabel', () => {
  it('resumes a conversation the agent has had, and relaunches otherwise', () => {
    expect(resumeLabel({ agentSession: { id: 'u', resumable: true, source: 'set' } }).label).toBe('Resume')
    expect(resumeLabel({ agentSession: { id: 'u', resumable: false, source: 'set' } }).label).toBe('Relaunch')
    expect(resumeLabel({}).label).toBe('Relaunch')
  })
})
