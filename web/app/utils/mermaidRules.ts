/**
 * What Mermaid runs under in the preview's renderer (#166), shared by the
 * page (web/app/utils/mermaidFrame.ts) and the sandboxed frame that does the
 * drawing (mermaidFrame.entry.ts). A repository's diagram is untrusted text.
 */

/**
 * Mermaid's DOMPurify configuration for labels, in place of its own (which
 * forbids only style): nothing that loads a resource or navigates survives in
 * a label, whatever the diagram's text said.
 */
export const MERMAID_PURIFY = {
  FORBID_TAGS: ['style', 'script', 'img', 'image', 'picture', 'source', 'video', 'audio', 'iframe', 'object', 'embed', 'link', 'use', 'svg', 'a', 'form', 'meta', 'base'],
  FORBID_ATTR: ['src', 'srcset', 'href', 'xlink:href', 'poster', 'background', 'style', 'action', 'formaction', 'data', 'ping'],
}

/**
 * The font the frame measures text in and the SVG names, the same on both
 * sides so the boxes Mermaid sizes fit the text the page shows: system fonts
 * only, since the frame loads none.
 */
export const MERMAID_FONT = 'ui-sans-serif, system-ui, sans-serif'

/** What the page asks the frame to draw. */
export interface DrawRequest {
  id: number
  source: string
  theme: 'dark' | 'default'
}

/** What the frame answers: the SVG, or why it could not draw. */
export interface DrawReply {
  id: number
  svg?: string
  error?: string
}
