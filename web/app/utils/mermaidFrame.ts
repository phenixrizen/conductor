import frameUrl from '~/utils/mermaidFrame.entry?worker&url'
import { MERMAID_RENDERER_DEV_PATH, type DrawReply, type DrawRequest } from '~/utils/mermaidRules'

/**
 * Mermaid drawn at arm's length (#166). A repository's diagram is untrusted
 * text, and Mermaid under securityLevel strict still loads an image node's
 * URL while it measures it, puts the diagram's CSS into the live document,
 * and keeps in a label whatever its sanitiser allows; a scan of the source
 * cannot keep up with every spelling (a quoted key, a JSON escape). So the
 * drawing happens in a frame of its own: sandboxed with scripts only (an
 * opaque origin, no storage or cookies of the app's), with a Content
 * Security Policy whose default is none, so that no image, font, style
 * sheet, connection or navigation can leave it, and only the renderer's
 * own script loads, from the app's origin. The page posts a request, the
 * frame answers with the SVG string or the error, and the page scrubs the
 * SVG once more before it goes in (MarkdownPreview). One frame serves
 * every preview on the page, made when the first diagram is asked for.
 */
const READY_WAIT = 30_000
const DRAW_WAIT = 30_000

let frame: HTMLIFrameElement | null = null
let ready: Promise<Window> | null = null
let seq = 0
const pending = new Map<number, { resolve: (svg: string) => void; reject: (e: Error) => void; timer: number }>()

/** The frame's policy: the renderer's script by its exact URL and nothing else at all; styles inline (Mermaid writes its own). */
function policy(script: string): string {
  return `default-src 'none'; script-src ${script}; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'`
}

function boot(): Promise<Window> {
  if (ready) return ready
  ready = new Promise<Window>((resolve, reject) => {
    // One classic script either way: the worker-format asset of the built app, or the dev server's bundle of the same entry.
    const script = new URL(import.meta.dev ? MERMAID_RENDERER_DEV_PATH : frameUrl, location.href).href
    const f = document.createElement('iframe')
    f.setAttribute('sandbox', 'allow-scripts')
    f.setAttribute('aria-hidden', 'true')
    f.setAttribute('title', 'Diagram renderer')
    f.tabIndex = -1
    f.setAttribute('data-mermaid-frame', '')
    // Laid out (Mermaid measures text), never seen.
    f.style.cssText = 'position:fixed;left:-10000px;top:0;width:1200px;height:900px;border:0;visibility:hidden;pointer-events:none'
    const timer = window.setTimeout(() => {
      ready = null
      f.remove()
      frame = null
      reject(new Error('the diagram renderer did not start'))
    }, READY_WAIT)
    const onMessage = (e: MessageEvent) => {
      if (e.source !== f.contentWindow) return
      const r = e.data as Partial<DrawReply> | null
      if (!r || typeof r.id !== 'number') return
      if (r.id === 0) {
        window.clearTimeout(timer)
        resolve(f.contentWindow!)
        return
      }
      const p = pending.get(r.id)
      if (!p) return
      pending.delete(r.id)
      window.clearTimeout(p.timer)
      if (typeof r.svg === 'string') p.resolve(r.svg)
      else p.reject(new Error(typeof r.error === 'string' ? r.error : 'the diagram was not drawn'))
    }
    window.addEventListener('message', onMessage)
    f.srcdoc = `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${policy(script)}"></head><body><script src="${script}"></script></body></html>`
    document.body.appendChild(f)
    frame = f
  })
  return ready
}

/** Draws a Mermaid diagram in the sandboxed frame: the SVG as a string, or an error with Mermaid's words. */
export async function renderMermaidIsolated(source: string, theme: 'dark' | 'default'): Promise<string> {
  const win = await boot()
  const id = ++seq
  return new Promise<string>((resolve, reject) => {
    const timer = window.setTimeout(() => {
      pending.delete(id)
      reject(new Error('the diagram took too long to draw'))
    }, DRAW_WAIT)
    pending.set(id, { resolve, reject, timer })
    const req: DrawRequest = { id, source, theme }
    win.postMessage(req, '*')
  })
}

/** For tests: whether the frame exists yet (it is made at the first diagram). */
export function mermaidFrameMade(): boolean {
  return frame !== null
}
