import { describe, expect, it } from 'vitest'
import { inviteInArgv, invitePath, parseInvite } from '../src/invite'

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
})
