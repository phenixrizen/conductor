/** Round-trip time from a pong that echoes the ping's timestamp; null when implausible. */
export function rttFromPong(sentTs: number, now: number): number | null {
  const d = now - sentTs
  return d < 0 || d > 60000 ? null : d
}
