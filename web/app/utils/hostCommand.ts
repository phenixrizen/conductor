/** Single-quotes s for a POSIX shell, whatever it holds: an embedded quote becomes '\''. */
function singleQuote(s: string): string {
  return `'${s.replace(/'/g, "'\\''")}'`
}

/** POSIX single-quote quoting for display in a copyable command. */
export function shellQuote(s: string): string {
  if (s === '') return "''"
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(s)) return s
  return singleQuote(s)
}

/**
 * Renders the `conductor host` command for the Launch dialog's "My machine"
 * option. Display only: the server never builds commands from this. `adapter`
 * is the agent's hook adapter: `--agent` names it, and the host wires that
 * agent's hooks into the command as a launch from the server would. `pattern`
 * is the agent's screen pattern (a regular expression); it is always
 * single-quoted, so the person who pastes the command passes it on as written.
 */
export function hostCommand(o: { server: string; token: string; name: string; argv: string[]; cwd?: string; pattern?: string; adapter?: string }): string {
  const parts = ['conductor', 'host', '--server', shellQuote(o.server), '--token', shellQuote(o.token)]
  if (o.name) parts.push('--name', shellQuote(o.name))
  if (o.cwd) parts.push('--cwd', shellQuote(o.cwd))
  if (o.adapter) parts.push('--agent', shellQuote(o.adapter))
  if (o.pattern) parts.push('--signal-pattern', singleQuote(o.pattern))
  parts.push('--', ...o.argv.map(shellQuote))
  return parts.join(' ')
}
