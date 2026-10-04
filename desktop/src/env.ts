import type { DesktopSettings } from './settings'

/**
 * serverEnv is the environment the server runs with: the app's own minus any CONDUCTOR_* that was set, the login shell's PATH, and
 * the CONDUCTOR_* values the settings choose. The workbench token is minted per run and never written anywhere.
 */
export function serverEnv(base: NodeJS.ProcessEnv, settings: DesktopSettings, token: string, path: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(base)) {
    if (v === undefined || k.startsWith('CONDUCTOR_')) continue
    out[k] = v
  }
  if (path) out.PATH = path
  out.CONDUCTOR_WORKBENCH_TOKEN = token
  out.CONDUCTOR_DATA_DIR = settings.dataDir
  out.CONDUCTOR_ALLOWED_ROOTS = settings.allowedRoots.join(',')
  out.CONDUCTOR_DEFAULT_CWD = settings.defaultCwd
  out.CONDUCTOR_YOLO = settings.yolo ? '1' : '0'
  out.CONDUCTOR_REACH = settings.reach
  // Publishing is the server's default; off is a choice. The server's own
  // default switchyard stands when none is named, and a token is optional.
  out.CONDUCTOR_RENDEZVOUS = settings.switchyardEnabled ? '1' : '0'
  if (settings.switchyardServer) out.CONDUCTOR_RENDEZVOUS_SERVER = settings.switchyardServer.replace(/\/+$/, '')
  if (settings.switchyardToken) out.CONDUCTOR_RENDEZVOUS_TOKEN = settings.switchyardToken
  if (settings.switchyardName) out.CONDUCTOR_RENDEZVOUS_HOST_NAME = settings.switchyardName
  if (!out.HOME && base.HOME) out.HOME = base.HOME
  return out
}

/** The server's arguments: a free loopback port, the handshake, and the exit with the app. */
export const SERVE_ARGS = ['serve', '--listen', '127.0.0.1:0', '--print-listen', '--exit-on-stdin-close']
