/**
 * The workbench's zoom. The keys are taken before the page sees them (webContents' before-input-event): on Windows and Linux a
 * page sees a key before the menu does, and the terminal swallows Ctrl+- (a control character), while the menu roles' own
 * accelerator, Ctrl+Plus, needs Shift on most layouts, so plain Ctrl+= did nothing. The level is the app's (kept in its settings),
 * so it outlives the server's port changing between starts.
 */

/** The bounds and step of the zoom level (Chromium's: each level is a factor of 1.2). */
export const ZOOM_MIN = -3
export const ZOOM_MAX = 5
export const ZOOM_STEP = 0.5

export type ZoomMove = 'in' | 'out' | 'reset'

/** What before-input-event hands over of a key (Electron's Input), as much as the zoom reads. */
export interface ZoomInput {
  type: string
  key: string
  code: string
  control: boolean
  meta: boolean
  alt: boolean
}

/**
 * zoomKey is the zoom move a key asks for, or null: with Ctrl (Cmd on macOS, where Ctrl stays the terminal's) and no Alt,
 * = or + (Shift or not, the numpad's +) zooms in, - (or the numpad's) out, 0 (or the numpad's) back to 100%.
 */
export function zoomKey(i: ZoomInput, platform: NodeJS.Platform): ZoomMove | null {
  if (i.type !== 'keyDown' || i.alt) return null
  if (!(platform === 'darwin' ? i.meta : i.control)) return null
  if (i.key === '=' || i.key === '+' || i.code === 'NumpadAdd' || i.code === 'Equal') return 'in'
  if (i.key === '-' || i.key === '_' || i.code === 'NumpadSubtract' || i.code === 'Minus') return 'out'
  if (i.key === '0' || i.code === 'Numpad0' || i.code === 'Digit0') return 'reset'
  return null
}

/** clampZoom bounds a level, a non-number being 0 (100%). */
export function clampZoom(level: unknown): number {
  if (typeof level !== 'number' || !Number.isFinite(level)) return 0
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(level / ZOOM_STEP) * ZOOM_STEP))
}

/** nextZoom is the level after a move. */
export function nextZoom(level: number, move: ZoomMove): number {
  if (move === 'reset') return 0
  return clampZoom(level + (move === 'in' ? ZOOM_STEP : -ZOOM_STEP))
}
