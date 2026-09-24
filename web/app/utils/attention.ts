import type { SessionInfo } from '~/composables/useSessions'

/** Sessions that need input, newest signal first. */
export function needingInput(sessions: Iterable<SessionInfo>): SessionInfo[] {
  const out: SessionInfo[] = []
  for (const s of sessions) {
    if (s.attention?.state === 'needs_input' && !isEnded(s.status)) out.push(s)
  }
  return out.sort((a, b) => (b.attention?.since ?? '').localeCompare(a.attention?.since ?? ''))
}

export function isEnded(status: string): boolean {
  return status === 'exited' || status === 'stopped'
}

export function isActive(s: SessionInfo): boolean {
  return s.status === 'running' || s.status === 'starting'
}

/**
 * Returns the sessions that newly entered needs_input compared with the
 * previous snapshot. Used to fire notifications exactly once per request.
 */
export function newlyNeedingInput(prev: Map<string, SessionInfo>, next: Map<string, SessionInfo>): SessionInfo[] {
  const out: SessionInfo[] = []
  for (const s of next.values()) {
    if (s.attention?.state !== 'needs_input' || isEnded(s.status)) continue
    const before = prev.get(s.id)
    const was = before?.attention?.state === 'needs_input'
    const sameSignal = was && before?.attention?.since === s.attention?.since
    if (!sameSignal) out.push(s)
  }
  return out
}

/** Document title with the count of sessions needing input. */
export function titleWithCount(base: string, count: number): string {
  return count > 0 ? `(${count}) ${base}` : base
}

let faviconCache: { plain?: string; alert?: string } = {}

/**
 * Draws the brand favicon with a terracotta dot in the corner. The dot is a
 * separate status marker; the junction in the mark is never recoloured.
 */
export async function attentionFavicon(alert: boolean, src = '/brand/conductor-favicon.svg'): Promise<string> {
  if (!alert) return src
  if (faviconCache.alert) return faviconCache.alert
  try {
    const img = new Image()
    img.src = src
    await img.decode()
    const size = 64
    const canvas = document.createElement('canvas')
    canvas.width = size
    canvas.height = size
    const ctx = canvas.getContext('2d')!
    ctx.drawImage(img, 0, 0, size, size)
    ctx.beginPath()
    ctx.arc(size - 13, 13, 12, 0, Math.PI * 2)
    ctx.fillStyle = '#eef1e9'
    ctx.fill()
    ctx.beginPath()
    ctx.arc(size - 13, 13, 9, 0, Math.PI * 2)
    ctx.fillStyle = '#d26b3f'
    ctx.fill()
    faviconCache.alert = canvas.toDataURL('image/png')
    return faviconCache.alert
  } catch {
    return src
  }
}

/** Short two-tone chime through the Web Audio API; no asset needed. */
export function playChime(): void {
  try {
    const AC = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
    const ctx = new AC()
    const now = ctx.currentTime
    for (const [i, freq] of [880, 1175].entries()) {
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()
      osc.type = 'sine'
      osc.frequency.value = freq
      gain.gain.setValueAtTime(0.0001, now + i * 0.14)
      gain.gain.exponentialRampToValueAtTime(0.2, now + i * 0.14 + 0.02)
      gain.gain.exponentialRampToValueAtTime(0.0001, now + i * 0.14 + 0.13)
      osc.connect(gain).connect(ctx.destination)
      osc.start(now + i * 0.14)
      osc.stop(now + i * 0.14 + 0.14)
    }
    window.setTimeout(() => ctx.close(), 600)
  } catch {
    /* audio unavailable */
  }
}

export function resetFaviconCache() {
  faviconCache = {}
}
