/** POSIX single-quote quoting for display in a copyable command. */
export function shellQuote(s: string): string {
  if (s === '') return "''"
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(s)) return s
  return `'${s.replace(/'/g, "'\\''")}'`
}

/**
 * Renders the `conductor host` command for the Launch dialog's "My machine"
 * option. Display only: the server never builds commands from this.
 */
export function hostCommand(o: { server: string; token: string; name: string; argv: string[]; cwd?: string }): string {
  const parts = ['conductor', 'host', '--server', shellQuote(o.server), '--token', shellQuote(o.token)]
  if (o.name) parts.push('--name', shellQuote(o.name))
  if (o.cwd) parts.push('--cwd', shellQuote(o.cwd))
  parts.push('--', ...o.argv.map(shellQuote))
  return parts.join(' ')
}
