import mermaid from 'mermaid'
import { MERMAID_FONT, MERMAID_PURIFY, type DrawReply, type DrawRequest } from './mermaidRules'

/**
 * The Mermaid renderer (#166), running inside the sandboxed frame the page
 * makes (mermaidFrame.ts): an opaque origin, and a Content Security Policy
 * that lets nothing be fetched (no image, font, style sheet, connection or
 * navigation), so whatever a repository's diagram asks Mermaid to load
 * while it draws, the browser refuses here. Mermaid runs with securityLevel
 * strict, errors not drawn into the document, labels as SVG text rather
 * than HTML, and a DOMPurify configuration forbidding every label element
 * and attribute that loads or navigates. The page sends a request, the
 * frame answers with the SVG or the error, and the document is emptied
 * after each (Mermaid draws in a container of its own in the body). The
 * requests are taken one after another: Mermaid queues renders of its own
 * accord, and a cleanup running beside the next render would take that
 * render's container away. Bundled as a worker-format script (one file,
 * its dependencies inlined), loaded by the frame with a script element:
 * the build records its packages for THIRD_PARTY_NOTICES as it does a
 * worker's.
 */
const base = {
  startOnLoad: false,
  securityLevel: 'strict' as const,
  suppressErrorRendering: true,
  dompurifyConfig: MERMAID_PURIFY,
  fontFamily: MERMAID_FONT,
  htmlLabels: false,
  flowchart: { htmlLabels: false },
}
let seq = 0
let chain: Promise<void> = Promise.resolve()

function reply(r: DrawReply) {
  parent.postMessage(r, '*')
}

async function draw(m: DrawRequest) {
  try {
    mermaid.initialize({ ...base, theme: m.theme === 'dark' ? 'dark' : 'default' })
    const { svg } = await mermaid.render(`d${++seq}`, m.source)
    reply({ id: m.id, svg })
  } catch (err) {
    reply({ id: m.id, error: (err as Error)?.message || String(err) })
  } finally {
    document.body.replaceChildren()
  }
}

addEventListener('message', (e: MessageEvent) => {
  if (e.source !== parent) return
  const m = e.data as Partial<DrawRequest> | null
  if (!m || typeof m.id !== 'number' || typeof m.source !== 'string') return
  const req: DrawRequest = { id: m.id, source: m.source, theme: m.theme === 'dark' ? 'dark' : 'default' }
  chain = chain.then(() => draw(req))
})

reply({ id: 0 })
