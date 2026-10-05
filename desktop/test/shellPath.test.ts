import { describe, expect, it } from 'vitest'
import { extractPath, loginShellPath, mergePaths } from '../src/shellPath'

// On Windows the server and its files live in WSL (launcher-wsl); these POSIX paths, modes and shells are not the app's there.
const win = process.platform === 'win32'

describe('shell PATH', () => {
  it('finds the marked line among a profile\'s noise', () => {
    expect(extractPath('hello from .zshrc\n__CONDUCTOR_PATH__/opt/x/bin:/usr/bin\n')).toBe('/opt/x/bin:/usr/bin')
    expect(extractPath('nothing')).toBe('')
  })
  it('merges without duplicates and with the tool directories last', () => {
    expect(mergePaths('/a:/b', '/b:/c', '/home/me', 'linux')).toBe('/a:/b:/c:/home/me/.local/bin:/home/me/go/bin:/usr/local/bin:/usr/bin:/bin')
    expect(mergePaths('', '/usr/bin', '/Users/me', 'darwin')).toBe('/usr/bin:/opt/homebrew/bin:/usr/local/bin:/Users/me/.local/bin:/bin')
  })
  it.skipIf(win)('asks the shell and gives up on silence', async () => {
    expect(await loginShellPath('/bin/sh', 5000)).toContain('/')
    expect(await loginShellPath('', 100)).toBe('')
    expect(await loginShellPath('/nonexistent/shell', 100)).toBe('')
  })
})
