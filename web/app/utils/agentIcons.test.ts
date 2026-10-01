import { describe, expect, it } from 'vitest'
import { AGENT_ICON_FALLBACK, agentIcon } from './agentIcons'

describe('agentIcon', () => {
  it('keeps an icon the workbench carries', () => {
    expect(agentIcon('i-lucide-sparkles')).toBe('i-lucide-sparkles')
    expect(agentIcon('i-lucide-wrench')).toBe('i-lucide-wrench')
  })
  it('shows the generic agent icon for one it does not carry, or none', () => {
    expect(agentIcon('i-lucide-unicorn')).toBe(AGENT_ICON_FALLBACK)
    expect(agentIcon(undefined)).toBe(AGENT_ICON_FALLBACK)
    expect(agentIcon('')).toBe(AGENT_ICON_FALLBACK)
  })
})
