import type { Integration } from '~/composables/useSessions'

/** Why an integration card has no Install button. An adapter with no file to install into says so first, whatever the server's home. */
export function noInstallText(it: Pick<Integration, 'installable' | 'launchInjection'>, homeKnown: boolean): string {
  if (!it.installable) return it.launchInjection ? 'Nothing to install: this server wires it at launch; anywhere else, paste the snippet.' : 'Nothing to install: paste the snippet.'
  if (!homeKnown) return 'The server has no home directory to install into: paste the snippet.'
  return 'Nothing to install into on this machine: paste the snippet.'
}
