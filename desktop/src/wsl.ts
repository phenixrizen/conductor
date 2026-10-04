/**
 * Pure parsers for what wsl.exe prints (Windows). `wsl.exe -l -v` lists the distributions with their state and WSL version; the
 * output is UTF-16LE unless WSL_UTF8=1 is set, so decode() takes either.
 */
export interface WslDistro {
  name: string
  default: boolean
  state: string
  version: number
}

/** decode turns wsl.exe's bytes into text: UTF-16LE when it looks like it (NUL every other byte), else UTF-8. */
export function decode(buf: Buffer): string {
  if (buf.length >= 4 && buf[1] === 0 && buf[3] === 0) return buf.toString('utf16le')
  return buf.toString('utf8')
}

/** parseList reads `wsl.exe -l -v`: a header line, then `* Ubuntu  Running  2` rows (the star marks the default). */
export function parseList(text: string): WslDistro[] {
  const out: WslDistro[] = []
  for (const raw of text.replace(/\0/g, '').split(/\r?\n/)) {
    const line = raw.trim()
    if (!line || /^NAME\s/i.test(line)) continue
    const def = line.startsWith('*')
    const fields = line.replace(/^\*\s*/, '').split(/\s{2,}|\t/).map((f) => f.trim()).filter(Boolean)
    if (fields.length < 3) continue
    const version = parseInt(fields[fields.length - 1]!, 10)
    if (!Number.isFinite(version)) continue
    out.push({ name: fields[0]!, default: def, state: fields[fields.length - 2]!, version })
  }
  return out
}

/** chooseDistro picks the distribution to run in: the one named, else the default, else the first; it must be WSL 2. */
export function chooseDistro(list: WslDistro[], wanted: string): { ok: true; distro: string } | { ok: false; reason: string } {
  if (!list.length) return { ok: false, reason: 'no WSL distribution is installed' }
  const pick = wanted ? list.find((d) => d.name === wanted) : (list.find((d) => d.default) ?? list[0])
  if (!pick) return { ok: false, reason: `the distribution ${JSON.stringify(wanted)} is not installed (installed: ${list.map((d) => d.name).join(', ')})` }
  if (pick.version !== 2) return { ok: false, reason: `${pick.name} is WSL ${pick.version}; Conductor needs WSL 2 (wsl --set-version ${pick.name} 2)` }
  return { ok: true, distro: pick.name }
}

/** windowsPathToWsl turns C:\Users\me\x into /mnt/c/Users/me/x (what wslpath -u does for a local drive). */
export function windowsPathToWsl(p: string): string {
  const m = /^([A-Za-z]):[\\/](.*)$/.exec(p)
  if (!m) return p
  return `/mnt/${m[1]!.toLowerCase()}/${m[2]!.replace(/\\/g, '/')}`
}

/** The shell script that installs the Linux binary inside the distribution: fixed text, its inputs positional, never interpolated. */
export const INSTALL_SCRIPT = `set -eu
src="$1"; version="$2"
dir="$HOME/.local/share/conductor/bin"
dst="$dir/conductor"
mkdir -p "$dir"
if [ -f "$dst" ] && [ -f "$dst.version" ] && [ "$(cat "$dst.version")" = "$version" ]; then exit 0; fi
cp "$src" "$dst.tmp"
chmod 755 "$dst.tmp"
mv "$dst.tmp" "$dst"
printf '%s\\n' "$version" > "$dst.version"
`

/** parseWslconfigNetworking reads %USERPROFILE%\.wslconfig for networkingMode; '' when unset (NAT). */
export function parseWslconfigNetworking(text: string): string {
  let inWsl2 = false
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (/^\[.*\]$/.test(line)) {
      inWsl2 = line.toLowerCase() === '[wsl2]'
      continue
    }
    if (!inWsl2) continue
    const m = /^networkingMode\s*=\s*(\S+)/i.exec(line)
    if (m) return m[1]!.toLowerCase()
  }
  return ''
}
