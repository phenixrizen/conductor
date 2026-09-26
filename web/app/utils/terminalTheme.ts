import type { ITheme } from '@xterm/xterm'

/**
 * Terminal colours follow the workbench: the canvas is the UI background
 * (`--ui-bg`), text is the UI text colour and the cursor is the brand accent.
 * ANSI colours are fixed palettes tuned for a neutral dark or light canvas.
 */

const ANSI_DARK = {
  black: '#484f58',
  red: '#ff7b72',
  green: '#3fb950',
  yellow: '#d29922',
  blue: '#58a6ff',
  magenta: '#bc8cff',
  cyan: '#39c5cf',
  white: '#b1bac4',
  brightBlack: '#6e7681',
  brightRed: '#ffa198',
  brightGreen: '#56d364',
  brightYellow: '#e3b341',
  brightBlue: '#79c0ff',
  brightMagenta: '#d2a8ff',
  brightCyan: '#56d4dd',
  brightWhite: '#f0f6fc',
}

const ANSI_LIGHT = {
  black: '#24292f',
  red: '#cf222e',
  green: '#116329',
  yellow: '#7d4e00',
  blue: '#0969da',
  magenta: '#8250df',
  cyan: '#1b7c83',
  white: '#6e7781',
  brightBlack: '#57606a',
  brightRed: '#a40e26',
  brightGreen: '#1a7f37',
  brightYellow: '#633c01',
  brightBlue: '#218bff',
  brightMagenta: '#a475f9',
  brightCyan: '#3192aa',
  brightWhite: '#8c959f',
}

/** Used when a CSS variable is missing or in an unexpected format. */
const FALLBACK = {
  dark: { bg: '#18181b', fg: '#e4e4e7', cursor: '#df8259', selection: '#9bb3a3' },
  light: { bg: '#ffffff', fg: '#18181b', cursor: '#a44727', selection: '#263d35' },
}

export interface UiColors {
  bg?: string
  text?: string
  secondary?: string
  primary?: string
}

function clamp01(x: number): number {
  return Math.min(1, Math.max(0, x))
}

function hex2(x: number): string {
  return Math.round(clamp01(x) * 255)
    .toString(16)
    .padStart(2, '0')
}

function toHex(r: number, g: number, b: number): string {
  return `#${hex2(r)}${hex2(g)}${hex2(b)}`
}

function linearToSrgb(c: number): number {
  return c <= 0.0031308 ? 12.92 * c : 1.055 * Math.pow(c, 1 / 2.4) - 0.055
}

/** OKLab → sRGB (Björn Ottosson's reference matrices). */
function oklabToHex(L: number, a: number, b: number): string {
  const l_ = L + 0.3963377774 * a + 0.2158037573 * b
  const m_ = L - 0.1055613458 * a - 0.0638541728 * b
  const s_ = L - 0.0894841775 * a - 1.291485548 * b
  const l = l_ * l_ * l_
  const m = m_ * m_ * m_
  const s = s_ * s_ * s_
  const r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
  const g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
  const bb = -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s
  return toHex(linearToSrgb(clamp01(r)), linearToSrgb(clamp01(g)), linearToSrgb(clamp01(bb)))
}

function num(token: string | undefined, scale = 1): number | undefined {
  if (token === undefined) return undefined
  const t = token.trim()
  if (t === 'none') return 0
  if (t.endsWith('%')) {
    const v = Number(t.slice(0, -1))
    return Number.isFinite(v) ? (v / 100) * scale : undefined
  }
  const v = Number(t)
  return Number.isFinite(v) ? v : undefined
}

function args(body: string): string[] {
  // "a b c / d" or "a, b, c, d" → ["a", "b", "c"] (alpha dropped).
  const noAlpha = body.split('/')[0] ?? ''
  return noAlpha
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/**
 * Normalises a CSS colour to `#rrggbb` for xterm, which only understands hex
 * and rgb(). Handles hex, rgb()/rgba(), color(srgb …), oklab() and oklch(),
 * which is what Tailwind v4 palettes resolve to. Alpha is dropped.
 */
export function cssColorToHex(value: string | undefined | null): string | undefined {
  if (!value) return undefined
  const v = value.trim().toLowerCase()
  const hex = v.match(/^#([\da-f]{3,8})$/)
  if (hex) {
    const h = hex[1]!
    if (h.length === 3 || h.length === 4) return `#${h[0]}${h[0]}${h[1]}${h[1]}${h[2]}${h[2]}`
    if (h.length === 6 || h.length === 8) return `#${h.slice(0, 6)}`
    return undefined
  }
  const fn = v.match(/^([a-z]+)\((.*)\)$/)
  if (!fn) return undefined
  const name = fn[1]!
  const body = fn[2]!
  if (name === 'rgb' || name === 'rgba') {
    const [r, g, b] = args(body)
    const rr = num(r, 255)
    const gg = num(g, 255)
    const bb = num(b, 255)
    if (rr === undefined || gg === undefined || bb === undefined) return undefined
    return toHex(rr / 255, gg / 255, bb / 255)
  }
  if (name === 'color') {
    const [space, r, g, b] = args(body)
    if (space !== 'srgb') return undefined
    const rr = num(r)
    const gg = num(g)
    const bb = num(b)
    if (rr === undefined || gg === undefined || bb === undefined) return undefined
    return toHex(rr, gg, bb)
  }
  if (name === 'oklch') {
    const [l, c, h] = args(body)
    const L = num(l)
    const C = num(c)
    const H = num(h)
    if (L === undefined || C === undefined || H === undefined) return undefined
    const rad = (H * Math.PI) / 180
    return oklabToHex(L, C * Math.cos(rad), C * Math.sin(rad))
  }
  if (name === 'oklab') {
    const [l, a, b] = args(body)
    const L = num(l)
    const A = num(a)
    const B = num(b)
    if (L === undefined || A === undefined || B === undefined) return undefined
    return oklabToHex(L, A, B)
  }
  return undefined
}

function withAlpha(hex: string, alpha: string): string {
  return `${hex}${alpha}`
}

/** Builds the xterm theme for the current colour mode from resolved UI colours. */
export function buildTerminalTheme(dark: boolean, colors: UiColors = {}): ITheme {
  const fb = dark ? FALLBACK.dark : FALLBACK.light
  const background = cssColorToHex(colors.bg) ?? fb.bg
  const foreground = cssColorToHex(colors.text) ?? fb.fg
  const cursor = cssColorToHex(colors.secondary) ?? fb.cursor
  const selection = cssColorToHex(colors.primary) ?? fb.selection
  return {
    background,
    foreground,
    cursor,
    cursorAccent: background,
    selectionBackground: withAlpha(selection, dark ? '66' : '40'),
    selectionInactiveBackground: withAlpha(selection, dark ? '33' : '20'),
    ...(dark ? ANSI_DARK : ANSI_LIGHT),
  }
}
