import { describe, expect, it } from 'vitest'
import { closeReason, CloseCode } from './protocol'

describe('what a closed connection says', () => {
  it('tells a link that expired from one that was revoked, both closed with 4403', () => {
    expect(closeReason(CloseCode.Forbidden, 'link expired')).toBe('This share link expired')
    expect(closeReason(CloseCode.Forbidden, 'link revoked')).toBe('This share link was revoked')
    expect(closeReason(CloseCode.Forbidden, '')).toBe('This share link was revoked')
  })
  it('names the other closes as before', () => {
    expect(closeReason(CloseCode.Unauthorized, '')).toBe('Not authorized for this session')
    expect(closeReason(CloseCode.SessionEnded, 'host disconnected')).toBe('host disconnected')
    expect(closeReason(CloseCode.SessionEnded, '')).toBe('Session ended')
    expect(closeReason(1006, '')).toBe('Connection lost')
    expect(closeReason(4999, '')).toBe('Connection closed (4999)')
  })
})
