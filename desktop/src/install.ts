import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'

/**
 * stableBinaryPath is where the server binary is copied to run from: a path that outlives the app's own files (an AppImage mounts
 * somewhere new each start; a WSL distribution cannot run a binary from /mnt/c well), so the hook assets the server writes, which
 * name the binary, stay valid across updates.
 */
export function stableBinaryPath(platform: string, home: string): string {
  if (platform === 'darwin') return join(home, 'Library', 'Application Support', 'Conductor', 'bin', 'conductor')
  return join(home, '.local', 'share', 'conductor', 'bin', 'conductor')
}

/**
 * ensureStableBinary copies src to dst when dst is missing or was copied from another version (dst's .version differs), mode 0755,
 * through a temporary file. It returns whether it copied.
 */
export function ensureStableBinary(src: string, dst: string, version: string): boolean {
  const stamp = dst + '.version'
  if (existsSync(dst) && existsSync(stamp)) {
    try {
      if (readFileSync(stamp, 'utf8').trim() === version && statSync(dst).size === statSync(src).size) return false
    } catch {
      /* copy */
    }
  }
  mkdirSync(dirname(dst), { recursive: true })
  const tmp = dst + '.tmp'
  copyFileSync(src, tmp)
  chmodSync(tmp, 0o755)
  renameSync(tmp, dst)
  writeFileSync(stamp, version + '\n', { mode: 0o644 })
  return true
}
