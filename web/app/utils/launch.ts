/**
 * Whether the launch dialog asks where the agent runs. There are two answers
 * only when the workbench is served from another machine: in the desktop
 * app, or on a workbench opened at this computer's own address, "this
 * server" and "this computer" are the same place, and the question is noise.
 */
export function showsRunsOn(isDesktop: boolean, hostname: string): boolean {
  if (isDesktop) return false
  return !isLoopbackHost(hostname)
}

/** A hostname that names the computer the browser runs on. */
export function isLoopbackHost(hostname: string): boolean {
  const h = hostname.trim().toLowerCase().replace(/^\[|\]$/g, '')
  return h === '' || h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\.\d+\.\d+\.\d+$/.test(h)
}
