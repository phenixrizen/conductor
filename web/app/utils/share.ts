import type { ReachInfo } from '~/composables/useSessions'
import { linkReach, type LinkReach } from './reach'

/** What a link just made reaches, in one line for the Share dialog. */
export type ShareReach = {
  /** `remote`: minted at the switchyard, works from anywhere; `unpublished`: a switchyard is configured but the session is not there; `local`: made here. */
  kind: 'remote' | 'unpublished' | 'local'
  level: LinkReach['level'] | 'remote'
  title: string
  text: string
}

/** The link as the create reply described it: where it was minted, or why not at the switchyard. */
export interface CreatedLink {
  url: string
  remote?: boolean
  rendezvous?: { server: string; error: string }
}

/** The host of a URL, for "shared through <host>"; the URL itself when it does not parse. */
export function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

/**
 * shareReach says what a link reaches: a link minted at the switchyard works from anywhere; one made here although a switchyard is
 * configured says why the session (or no member of the crew) is not there, then what the local link reaches.
 */
export function shareReach(created: CreatedLink, reach: ReachInfo | null | undefined, run = false): ShareReach {
  if (created.remote) {
    return {
      kind: 'remote',
      level: 'remote',
      title: 'Works from anywhere',
      text: run
        ? `Shared through ${hostOf(created.url)}: the link opens every agent of the crew there, members who join later included, and each terminal comes straight to this machine, through the switchyard's relay only when it must.`
        : `Shared through ${hostOf(created.url)}: the link opens there, and the terminal comes straight to this machine, through the switchyard's relay only when it must.`,
    }
  }
  const local = linkReach(created.url, reach)
  if (created.rendezvous) {
    return {
      kind: 'unpublished',
      level: local.level,
      title: 'Not published to the switchyard',
      text: `${hostOf(created.rendezvous.server)}: ${created.rendezvous.error}. This link works where this machine is reachable. ${local.text}`,
    }
  }
  return { kind: 'local', level: local.level, title: local.title, text: local.text }
}
