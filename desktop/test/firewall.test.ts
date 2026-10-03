import { describe, expect, it } from 'vitest'
import { allowIceThroughFirewall, firewallRuleExists, firewallRuleName } from '../src/firewall'

describe('firewall', () => {
  it('reads the rule state from netsh, and names the rule as the installer does', async () => {
    expect(firewallRuleName(7877)).toBe('Conductor WebRTC (UDP 7877)')
    const calls: string[][] = []
    const present = async (file: string, args: string[]) => {
      calls.push([file, ...args])
      return { ok: true, out: 'Rule Name: Conductor WebRTC (UDP 7877)' }
    }
    expect(await firewallRuleExists(7877, present)).toBe('present')
    expect(calls[0]).toEqual(['netsh', 'advfirewall', 'firewall', 'show', 'rule', 'name=Conductor WebRTC (UDP 7877)'])
    expect(await firewallRuleExists(7877, async () => ({ ok: false, out: 'No rules match the specified criteria.' }))).toBe('missing')
    expect(await firewallRuleExists(7877, async () => ({ ok: false, out: '' }))).toBe('unknown')
  })

  it('adds the rule through an elevated netsh with fixed arguments', async () => {
    const calls: string[][] = []
    const ok = await allowIceThroughFirewall(7877, async (file, args) => {
      calls.push([file, ...args])
      return { ok: true, out: '' }
    })
    expect(ok).toBe(true)
    expect(calls[0]![0]).toBe('powershell.exe')
    expect(calls[0]![3]).toContain(`Start-Process netsh.exe -ArgumentList 'advfirewall firewall add rule name="Conductor WebRTC (UDP 7877)" dir=in action=allow protocol=UDP localport=7877' -Verb RunAs`)
  })
})
