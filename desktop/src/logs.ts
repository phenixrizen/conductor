import { existsSync, mkdirSync, renameSync, statSync, writeFileSync, appendFileSync } from 'node:fs'
import { join } from 'node:path'

const MAX_BYTES = 5 * 1024 * 1024
const KEEP = 3
const RING = 2000

/** Logs keeps the app's and the server's lines: rotating files under dir, and the last RING lines for the log window. */
export class Logs {
  readonly ring: string[] = []
  private listeners = new Set<(line: string) => void>()

  constructor(readonly dir: string) {
    mkdirSync(dir, { recursive: true })
  }

  /** line records text under kind (main, server); the ring gets a stamped line, the file the text as it came. */
  line(kind: 'main' | 'server', text: string): void {
    const clean = text.replace(/\r?\n$/, '')
    const stamped = `${new Date().toISOString()} [${kind}] ${clean}`
    this.ring.push(stamped)
    if (this.ring.length > RING) this.ring.splice(0, this.ring.length - RING)
    this.append(join(this.dir, `${kind}.log`), clean + '\n')
    for (const l of this.listeners) l(stamped)
  }

  onLine(l: (line: string) => void): () => void {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  private append(file: string, text: string) {
    try {
      if (existsSync(file) && statSync(file).size > MAX_BYTES) {
        for (let i = KEEP - 1; i >= 1; i--) {
          const from = `${file}.${i}`
          if (existsSync(from)) renameSync(from, `${file}.${i + 1}`)
        }
        renameSync(file, `${file}.1`)
        writeFileSync(file, '')
      }
      appendFileSync(file, text)
    } catch {
      /* a log that cannot be written is not worth stopping for */
    }
  }
}
