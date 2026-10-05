/** What the app says of itself: Settings → The app, the join page's credit line, the desktop splash. */

/** The sponsor line, the same words everywhere it shows (the Sponsor Kit): footers and About screens, never the workspace. */
export const CREDIT = 'Sponsored and maintained by RockSolid Labs'
export const SPONSOR_NAME = 'RockSolid Labs'
export const SPONSOR_URL = 'https://rocksolidlabs.io'
export const SPONSOR_HOST = 'rocksolidlabs.io'
export const SOURCE_URL = 'https://github.com/phenixrizen/conductor'
/** The holder as the desktop package names it (electron-builder's copyright). */
export const COPYRIGHT_HOLDER = 'the Conductor authors'

/** The copyright line: the year of the first release, through `year` when later. */
export function copyrightLine(year: number = new Date().getFullYear()): string {
  const first = 2026
  return `© ${year > first ? `${first}–${year}` : first} ${COPYRIGHT_HOLDER}`
}

/** "0.6.0 · naterdev-win": a version with the machine it runs on, either part left out when unknown. */
export function versionOn(version: string, host: string): string {
  return [version, host].filter(Boolean).join(' · ')
}
