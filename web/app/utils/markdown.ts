import MarkdownIt from 'markdown-it'

/**
 * The Markdown preview (#166): a `.md` file rendered for the editor's Preview
 * toggle. A repository's Markdown is untrusted text, so raw HTML is never
 * rendered (markdown-it parses it as text: no scripts, no handlers), links
 * with a scheme markdown-it refuses (`javascript:`, `vbscript:`, `file:`)
 * are not links, links to the web open outside the app, a relative link
 * opens its file in the editor (`data-md-link`, resolved against the file's
 * folder), a relative image loads through the session's file reads (the
 * raw URL), never by a URL the page makes up, and a remote image is not
 * loaded (its alt text shows) unless allowed. Fenced code is left escaped
 * for the preview component to highlight with shiki; a `mermaid` fence is
 * marked for Mermaid to draw (`data-mermaid`), its source kept as text.
 */
export interface RenderOptions {
  /** The folder of the file, which relative links and images resolve against. */
  dir: string
  /** The session's raw file URL for a path, for relative images; none means they are not shown. */
  rawUrl?: (path: string) => string | null
  /** Whether images on the web are loaded (they tell a third party the file was opened). Off by default. */
  allowRemote?: boolean
}

export interface Rendered {
  html: string
  /** How many Mermaid blocks the HTML holds, for the component to draw (none: Mermaid is never loaded). */
  mermaid: number
}

/** Whether a path is a Markdown file the editor previews. */
export function isMarkdownPath(path: string): boolean {
  return /\.(md|markdown)$/i.test(path)
}

/** The folder of a path: everything before its last segment. */
export function dirOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i > 0 ? path.slice(0, i) : i === 0 ? '/' : ''
}

/** A relative path resolved against dir: `.` and `..` folded, an absolute path kept. */
export function resolveRelative(dir: string, href: string): string {
  const base = href.startsWith('/') ? [] : dir.split('/').filter(Boolean)
  const out = [...base]
  for (const part of href.split('/')) {
    if (part === '' || part === '.') continue
    if (part === '..') out.pop()
    else out.push(part)
  }
  return (dir.startsWith('/') || href.startsWith('/') ? '/' : '') + out.join('/')
}

/** Whether a link or image points outside the repository: a scheme, or a protocol-relative URL. */
export function isRemote(href: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//')
}

/**
 * The file path a local link or image names: markdown-it gives link destinations URL-encoded (a space as %20, a
 * name in Japanese as its bytes), so the query and fragment are cut and the rest decoded once; one that is not
 * valid encoding is taken as written.
 */
export function localPath(href: string): string {
  const path = href.split(/[?#]/, 1)[0] ?? ''
  try {
    return decodeURIComponent(path)
  } catch {
    return path
  }
}

/**
 * Why a Mermaid diagram would reach outside the page, or null when it would not. Mermaid under
 * securityLevel strict still loads an image node's URL (`A@{ img: "…" }`) while it draws, puts CSS from
 * `style`, `classDef` and `themeCSS` into the SVG (a `url(…)` in it is fetched by the browser), and keeps an
 * HTML element in a label that would load something or navigate (its sanitiser forbids only script).
 * A diagram with any of these is not drawn: its source shows with this reason. The match is on the source
 * as written, so a label that merely mentions one of these words is held too (it stays readable as source).
 */
export function mermaidFetches(source: string): string | null {
  if (/(^|[\s{,])img\s*:/i.test(source)) return 'an image node (img:) would be loaded from its URL'
  if (/url\s*\(/i.test(source)) return 'a url( in its CSS would be fetched'
  if (/@import\b/i.test(source)) return 'an @import in its CSS would be fetched'
  if (/<\s*(img|image|picture|source|video|audio|iframe|object|embed|link|use|svg|script|style|a|form|meta|base)\b/i.test(source)) return 'an HTML element in a label would load something or navigate'
  if (/\b(src|srcset|href|xlink:href|poster|background|action|formaction|data)\s*=/i.test(source)) return 'an attribute in a label names a URL'
  return null
}

/**
 * Mermaid's DOMPurify configuration for labels under this preview, in place of its own (which forbids only
 * style): nothing that loads a resource or navigates survives in a label, whatever the diagram's text said.
 */
export const MERMAID_PURIFY = {
  FORBID_TAGS: ['style', 'script', 'img', 'image', 'picture', 'source', 'video', 'audio', 'iframe', 'object', 'embed', 'link', 'use', 'svg', 'a', 'form', 'meta', 'base'],
  FORBID_ATTR: ['src', 'srcset', 'href', 'xlink:href', 'poster', 'background', 'style', 'action', 'formaction', 'data', 'ping'],
}

let mermaidSeq = 0
/** An id for Mermaid to draw under, unique across every preview on the page: Mermaid removes an element of the id it is given before it draws, so two previews sharing a counter would erase each other's diagrams. */
export function nextMermaidId(): string {
  return `md-mermaid-${++mermaidSeq}`
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

/** Renders Markdown source to HTML under the rules above. Pure: the same source and options give the same HTML. */
export function renderMarkdown(src: string, opts: RenderOptions): Rendered {
  const md = new MarkdownIt({ html: false, linkify: false, typographer: false, breaks: false })
  let mermaid = 0

  // Task lists: a list item beginning `[ ] ` or `[x] ` gets a checkbox (disabled: the preview changes no file).
  md.core.ruler.after('inline', 'task-lists', (state) => {
    const tokens = state.tokens
    for (let i = 0; i < tokens.length; i++) {
      const t = tokens[i]!
      if (t.type !== 'inline' || !t.children?.length) continue
      const first = t.children[0]!
      const m = first.type === 'text' ? /^\[([ xX])\] /.exec(first.content) : null
      if (!m) continue
      // The inline's paragraph, and the list item it is in.
      const item = tokens[i - 2]
      if (!item || item.type !== 'list_item_open' || tokens[i - 1]?.type !== 'paragraph_open') continue
      first.content = first.content.slice(m[0].length)
      const box = new state.Token('html_inline', '', 0)
      box.content = `<input type="checkbox" disabled${m[1] === ' ' ? '' : ' checked'}> `
      t.children.unshift(box)
      item.attrJoin('class', 'task')
    }
  })

  md.renderer.rules.link_open = (tokens, idx, options, _env, self) => {
    const t = tokens[idx]!
    const href = String(t.attrGet('href') ?? '')
    if (isRemote(href)) {
      t.attrSet('target', '_blank')
      t.attrSet('rel', 'noopener noreferrer')
    } else if (href && !href.startsWith('#')) {
      // A file in the repository: the component opens it in a tab.
      t.attrSet('data-md-link', resolveRelative(opts.dir, localPath(href)))
      t.attrSet('href', '#')
    }
    return self.renderToken(tokens, idx, options)
  }

  md.renderer.rules.image = (tokens, idx, options, env, self) => {
    const t = tokens[idx]!
    const src = String(t.attrGet('src') ?? '')
    const alt = self.renderInlineAsText(t.children ?? [], options, env)
    if (/^data:image\//i.test(src)) {
      // An image embedded in the file (markdown-it passes only gif, png, jpeg and webp data URLs): nothing is fetched.
    } else if (isRemote(src)) {
      if (!opts.allowRemote) return `<span class="md-image-held" title="An image on the web is not loaded here">${escapeHtml(alt || src)}</span>`
    } else {
      const url = opts.rawUrl?.(resolveRelative(opts.dir, localPath(src))) ?? null
      if (!url) return `<span class="md-image-held" title="${escapeHtml(src)}">${escapeHtml(alt || src)}</span>`
      t.attrSet('src', url)
    }
    t.attrSet('alt', alt)
    return self.renderToken(tokens, idx, options)
  }

  md.renderer.rules.fence = (tokens, idx) => {
    const t = tokens[idx]!
    const lang = (t.info || '').trim().split(/\s+/)[0] ?? ''
    if (lang === 'mermaid') {
      mermaid++
      return `<pre class="md-mermaid" data-mermaid>${escapeHtml(t.content)}</pre>\n`
    }
    return `<pre class="md-code"${lang ? ` data-code-lang="${escapeHtml(lang)}"` : ''}><code>${escapeHtml(t.content)}</code></pre>\n`
  }

  return { html: md.render(src), mermaid }
}
