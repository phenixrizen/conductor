/**
 * A QR code of a short text, for the share dialog (#144): byte mode, error correction level M, versions 1 to 10 (up to 213
 * bytes), the mask picked by the standard's penalty rules. Written after ISO/IEC 18004 so that the page needs no library and
 * the switchyard serves nothing for it. The modules come as rows of booleans, true dark; null for a text too long for version 10.
 */
export type QrCode = { version: number; size: number; mask: number; modules: boolean[][] }

/** Error-correction codewords per block and blocks per code, level M, by version (index 0 unused). */
const ECC_PER_BLOCK = [0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26]
const BLOCKS = [0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5]
const MAX_VERSION = 10

/** Every codeword a version holds, data and error correction together. */
function rawCodewords(version: number): number {
  let bits = (16 * version + 128) * version + 64
  if (version >= 2) {
    const n = Math.floor(version / 7) + 2
    bits -= (25 * n - 10) * n - 55
    if (version >= 7) bits -= 36
  }
  return Math.floor(bits / 8)
}

function dataCodewords(version: number): number {
  return rawCodewords(version) - ECC_PER_BLOCK[version]! * BLOCKS[version]!
}

/** The bits of the byte count: 8 up to version 9, 16 from version 10. */
function countBits(version: number): number {
  return version >= 10 ? 16 : 8
}

/** The bytes a version holds in byte mode at level M. */
export function qrCapacity(version: number): number {
  return Math.floor((dataCodewords(version) * 8 - 4 - countBits(version)) / 8)
}

/** Encodes `text` (as UTF-8); `mask` forces one of the eight masks, else the one with the lowest penalty is used. */
export function encodeQr(text: string, mask?: number): QrCode | null {
  const bytes = new TextEncoder().encode(text)
  let version = 1
  while (version <= MAX_VERSION && qrCapacity(version) < bytes.length) version++
  if (version > MAX_VERSION) return null

  // The bit stream: the mode, the count, the bytes, the terminator, zeros to a byte, then pad bytes to the capacity.
  const bits: number[] = []
  const push = (value: number, n: number) => {
    for (let i = n - 1; i >= 0; i--) bits.push((value >>> i) & 1)
  }
  push(4, 4)
  push(bytes.length, countBits(version))
  for (const b of bytes) push(b, 8)
  const capacity = dataCodewords(version) * 8
  push(0, Math.min(4, capacity - bits.length))
  while (bits.length % 8) bits.push(0)
  for (let pad = 0xec; bits.length < capacity; pad ^= 0xec ^ 0x11) push(pad, 8)
  const data: number[] = new Array(bits.length / 8).fill(0)
  bits.forEach((bit, i) => (data[i >>> 3]! |= bit << (7 - (i & 7))))

  const size = version * 4 + 17
  const modules: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  const fixed: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false))
  drawFunctionPatterns(modules, fixed, version)
  drawCodewords(modules, fixed, interleave(data, version))

  let chosen = mask ?? 0
  if (mask === undefined) {
    let best = Infinity
    for (let m = 0; m < 8; m++) {
      applyMask(modules, fixed, m)
      drawFormat(modules, fixed, m)
      const score = penaltyOf(modules)
      if (score < best) {
        best = score
        chosen = m
      }
      applyMask(modules, fixed, m) // undone: the mask is its own inverse
    }
  }
  applyMask(modules, fixed, chosen)
  drawFormat(modules, fixed, chosen)
  return { version, size, mask: chosen, modules }
}

/** The SVG of a text's code: a path of one square per dark module inside a quiet zone of `border` modules; null when too long. */
export function qrSvg(text: string, border = 4): { viewBox: string; path: string; size: number } | null {
  const qr = encodeQr(text)
  if (!qr) return null
  const parts: string[] = []
  qr.modules.forEach((row, y) => row.forEach((dark, x) => dark && parts.push(`M${x + border},${y + border}h1v1h-1z`)))
  const n = qr.size + border * 2
  return { viewBox: `0 0 ${n} ${n}`, path: parts.join(''), size: n }
}

// Function patterns: the finders with their separators, the timing lines, the alignment patterns, the format and version bits.

function drawFunctionPatterns(modules: boolean[][], fixed: boolean[][], version: number) {
  const size = modules.length
  const set = (x: number, y: number, dark: boolean) => {
    if (x < 0 || y < 0 || x >= size || y >= size) return
    modules[y]![x] = dark
    fixed[y]![x] = true
  }
  for (let i = 0; i < size; i++) {
    set(6, i, i % 2 === 0)
    set(i, 6, i % 2 === 0)
  }
  const finder = (cx: number, cy: number) => {
    for (let dy = -4; dy <= 4; dy++)
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy))
        set(cx + dx, cy + dy, dist !== 2 && dist !== 4)
      }
  }
  finder(3, 3)
  finder(size - 4, 3)
  finder(3, size - 4)
  const pos = alignmentPositions(version)
  const n = pos.length
  for (let i = 0; i < n; i++)
    for (let j = 0; j < n; j++) {
      if ((i === 0 && j === 0) || (i === 0 && j === n - 1) || (i === n - 1 && j === 0)) continue
      for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) set(pos[i]! + dx, pos[j]! + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1)
    }
  drawFormat(modules, fixed, 0) // reserved now; the bits of the mask chosen come last
  if (version >= 7) {
    let rem = version
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
    const bits = (version << 12) | rem
    for (let i = 0; i < 18; i++) {
      const bit = ((bits >>> i) & 1) === 1
      const a = size - 11 + (i % 3)
      const b = Math.floor(i / 3)
      set(a, b, bit)
      set(b, a, bit)
    }
  }
}

/** The centres of the alignment patterns along one axis. */
function alignmentPositions(version: number): number[] {
  if (version === 1) return []
  const size = version * 4 + 17
  const count = Math.floor(version / 7) + 2
  const step = Math.ceil((version * 4 + 4) / (count * 2 - 2)) * 2
  const result = [6]
  for (let pos = size - 7; result.length < count; pos -= step) result.splice(1, 0, pos)
  return result
}

/** The format bits (level M, the mask) in their two places, and the dark module. */
function drawFormat(modules: boolean[][], fixed: boolean[][], mask: number) {
  const size = modules.length
  const data = mask // level M is 00, so the five bits are the mask's three
  let rem = data
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
  const bits = ((data << 10) | rem) ^ 0x5412
  const set = (x: number, y: number, dark: boolean) => {
    modules[y]![x] = dark
    fixed[y]![x] = true
  }
  const bit = (i: number) => ((bits >>> i) & 1) === 1
  for (let i = 0; i <= 5; i++) set(8, i, bit(i))
  set(8, 7, bit(6))
  set(8, 8, bit(7))
  set(7, 8, bit(8))
  for (let i = 9; i < 15; i++) set(14 - i, 8, bit(i))
  for (let i = 0; i < 8; i++) set(size - 1 - i, 8, bit(i))
  for (let i = 8; i < 15; i++) set(8, size - 15 + i, bit(i))
  set(8, size - 8, true)
}

/** The codewords zigzag up and down in pairs of columns from the right, skipping the timing column and the fixed modules. */
function drawCodewords(modules: boolean[][], fixed: boolean[][], codewords: number[]) {
  const size = modules.length
  let i = 0
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j
        const upward = ((right + 1) & 2) === 0
        const y = upward ? size - 1 - vert : vert
        if (!fixed[y]![x] && i < codewords.length * 8) {
          modules[y]![x] = ((codewords[i >>> 3]! >>> (7 - (i & 7))) & 1) === 1
          i++
        }
      }
    }
  }
}

function applyMask(modules: boolean[][], fixed: boolean[][], mask: number) {
  const size = modules.length
  for (let y = 0; y < size; y++)
    for (let x = 0; x < size; x++) {
      if (fixed[y]![x]) continue
      let invert: boolean
      switch (mask) {
        case 0: invert = (x + y) % 2 === 0; break
        case 1: invert = y % 2 === 0; break
        case 2: invert = x % 3 === 0; break
        case 3: invert = (x + y) % 3 === 0; break
        case 4: invert = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0; break
        case 5: invert = ((x * y) % 2) + ((x * y) % 3) === 0; break
        case 6: invert = (((x * y) % 2) + ((x * y) % 3)) % 2 === 0; break
        default: invert = (((x + y) % 2) + ((x * y) % 3)) % 2 === 0
      }
      if (invert) modules[y]![x] = !modules[y]![x]
    }
}

// The penalty of a masked code (ISO/IEC 18004, 7.8.3): runs of five or more alike, 2×2 blocks alike, finder-like patterns, and the
// balance of dark and light.

const N1 = 3
const N2 = 3
const N3 = 40
const N4 = 10

export function penaltyOf(modules: boolean[][]): number {
  const size = modules.length
  let result = 0
  const line = (at: (i: number) => boolean) => {
    let runColor = false
    let runLength = 0
    const history = [0, 0, 0, 0, 0, 0, 0]
    for (let i = 0; i < size; i++) {
      if (at(i) === runColor) {
        runLength++
        if (runLength === 5) result += N1
        else if (runLength > 5) result++
      } else {
        addRun(history, runLength, size)
        if (!runColor) result += finderPatterns(history) * N3
        runColor = at(i)
        runLength = 1
      }
    }
    if (runColor) {
      addRun(history, runLength, size)
      runLength = 0
    }
    addRun(history, runLength + size, size)
    result += finderPatterns(history) * N3
  }
  for (let y = 0; y < size; y++) line((x) => modules[y]![x]!)
  for (let x = 0; x < size; x++) line((y) => modules[y]![x]!)
  for (let y = 0; y < size - 1; y++)
    for (let x = 0; x < size - 1; x++) {
      const c = modules[y]![x]
      if (c === modules[y]![x + 1] && c === modules[y + 1]![x] && c === modules[y + 1]![x + 1]) result += N2
    }
  let dark = 0
  for (const row of modules) for (const m of row) if (m) dark++
  const total = size * size
  const k = Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1
  return result + k * N4
}

/** A run joins the history of the last seven; the first run counts the light quiet zone before it. */
function addRun(history: number[], runLength: number, size: number) {
  if (history[0] === 0) runLength += size
  history.pop()
  history.unshift(runLength)
}

/** How many finder-like patterns (dark 1:1:3:1:1 with four light on a side) the last seven runs hold: 0, 1 or 2. */
function finderPatterns(h: number[]): number {
  const n = h[1]!
  const core = n > 0 && h[2] === n && h[3] === n * 3 && h[4] === n && h[5] === n
  return (core && h[0]! >= n * 4 && h[6]! >= n ? 1 : 0) + (core && h[6]! >= n * 4 && h[0]! >= n ? 1 : 0)
}

// Reed–Solomon error correction over GF(2^8) with the QR polynomial 0x11d, and the blocks interleaved as the standard lays them.

function gfMul(x: number, y: number): number {
  let z = 0
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d)
    z ^= ((y >>> i) & 1) * x
  }
  return z
}

function rsDivisor(degree: number): number[] {
  const result = new Array<number>(degree).fill(0)
  result[degree - 1] = 1
  let root = 1
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < degree; j++) result[j] = gfMul(result[j]!, root) ^ (j + 1 < degree ? result[j + 1]! : 0)
    root = gfMul(root, 2)
  }
  return result
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const result = divisor.map(() => 0)
  for (const b of data) {
    const factor = b ^ result.shift()!
    result.push(0)
    divisor.forEach((coef, i) => (result[i]! ^= gfMul(coef, factor)))
  }
  return result
}

function interleave(data: number[], version: number): number[] {
  const numBlocks = BLOCKS[version]!
  const eccLen = ECC_PER_BLOCK[version]!
  const raw = rawCodewords(version)
  const numShort = numBlocks - (raw % numBlocks)
  const shortLen = Math.floor(raw / numBlocks)
  const divisor = rsDivisor(eccLen)
  const blocks: number[][] = []
  for (let i = 0, k = 0; i < numBlocks; i++) {
    const datLen = shortLen - eccLen + (i < numShort ? 0 : 1)
    const dat = data.slice(k, k + datLen)
    k += datLen
    const ecc = rsRemainder(dat, divisor)
    if (i < numShort) dat.push(0) // a place the long blocks fill, skipped below
    blocks.push([...dat, ...ecc])
  }
  const result: number[] = []
  for (let i = 0; i < blocks[0]!.length; i++) blocks.forEach((block, j) => (i !== shortLen - eccLen || j >= numShort) && result.push(block[i]!))
  return result
}
