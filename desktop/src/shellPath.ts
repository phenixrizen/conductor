import { execFile } from 'node:child_process'

const MARKER = '__CONDUCTOR_PATH__'

/** extractPath finds the PATH a login shell printed after the marker, or returns '' when it printed none. */
export function extractPath(output: string): string {
  for (const line of output.split('\n').reverse()) {
    const at = line.indexOf(MARKER)
    if (at >= 0) return line.slice(at + MARKER.length).replace(/\r$/, '').trim()
  }
  return ''
}

/** mergePaths joins the login shell's PATH with the process's, without duplicates, and with the usual tool directories at the end. */
export function mergePaths(loginPath: string, processPath: string, home: string, platform = process.platform): string {
  const extra = platform === 'darwin' ? ['/opt/homebrew/bin', '/usr/local/bin', `${home}/.local/bin`] : [`${home}/.local/bin`, `${home}/go/bin`, '/usr/local/bin']
  const out: string[] = []
  for (const p of [...loginPath.split(':'), ...processPath.split(':'), ...extra, '/usr/bin', '/bin']) {
    const q = p.trim()
    if (q && !out.includes(q)) out.push(q)
  }
  return out.join(':')
}

/**
 * loginShellPath asks the user's login shell for its PATH (an app started from a dock or a launcher has a bare one, and the agents
 * live where the shell's profile points), within timeoutMs; '' when the shell does not answer.
 */
export function loginShellPath(shell: string, timeoutMs = 5000, runner = execFile): Promise<string> {
  if (!shell) return Promise.resolve('')
  return new Promise((resolve) => {
    let settled = false
    const done = (v: string) => {
      if (!settled) {
        settled = true
        resolve(v)
      }
    }
    try {
      const child = runner(shell, ['-ilc', `printf '%s%s\\n' ${MARKER} "$PATH"`], { timeout: timeoutMs, env: { ...process.env, TERM: 'dumb' } }, (err, stdout) => {
        if (err && !stdout) return done('')
        done(extractPath(String(stdout)))
      })
      child.on?.('error', () => done(''))
    } catch {
      done('')
    }
  })
}
