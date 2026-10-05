import type { Integration } from '~/composables/useSessions'

/** Why an integration card has no Install button. An adapter with no file to install into says so first, whatever the server's home. */
export function noInstallText(it: Pick<Integration, 'installable' | 'launchInjection'>, homeKnown: boolean): string {
  if (!it.installable) return it.launchInjection ? 'Nothing to install: this server wires it at launch; anywhere else, paste the snippet.' : 'Nothing to install: paste the snippet.'
  if (!homeKnown) return 'The server has no home directory to install into: paste the snippet.'
  return 'Nothing to install into on this machine: paste the snippet.'
}

/**
 * Whether an install left the agent's hooks to wire by hand, which the card's
 * snippet does: the server's `no_file_route` reply carries the snippet only
 * then, and not when a skill file of the user's own is all that is left.
 */
export function snippetLeft(error: { code: string; details: Record<string, unknown> }): boolean {
  return error.code === 'no_file_route' && typeof error.details.snippet === 'string' && error.details.snippet !== ''
}
