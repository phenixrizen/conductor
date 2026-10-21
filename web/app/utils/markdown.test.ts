import { describe, expect, it } from 'vitest'
import { dirOf, isMarkdownPath, localPath, mermaidFetches, nextMermaidId, renderMarkdown, resolveRelative } from './markdown'

const dir = '/repo/docs'
const rawUrl = (p: string) => `/api/sessions/s1/files?path=${encodeURIComponent(p)}&raw=1`

describe('the Markdown preview', () => {
  it('renders headings, lists, tables, task lists and code, and names Markdown files and their folders', () => {
    const { html, mermaid } = renderMarkdown('# Title\n\n- one\n- [x] done\n- [ ] not yet\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc main() {}\n```\n', { dir })
    expect(html).toContain('<h1>Title</h1>')
    expect(html).toContain('<li class="task"><input type="checkbox" disabled checked> done</li>')
    expect(html).toContain('<li class="task"><input type="checkbox" disabled> not yet</li>')
    expect(html).toContain('<li>one</li>')
    expect(html).toContain('<td>1</td>')
    expect(html).toContain('<pre class="md-code" data-code-lang="go"><code>func main() {}\n</code></pre>')
    expect(mermaid).toBe(0)
    expect(isMarkdownPath('/r/README.md')).toBe(true)
    expect(isMarkdownPath('/r/notes.MARKDOWN')).toBe(true)
    expect(isMarkdownPath('/r/main.go')).toBe(false)
    expect(dirOf('/repo/docs/users.md')).toBe('/repo/docs')
    expect(dirOf('/users.md')).toBe('/')
  })

  it('renders raw HTML as text and refuses scripted links: nothing runs', () => {
    const { html } = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[run](javascript:alert(1)) and [also](vbscript:x)\n', { dir })
    // Shown as the text it is: no element, no attribute, no link.
    expect(html).not.toMatch(/<script|<img/)
    expect(html).toContain('<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>')
    expect(html).toContain('<p>&lt;img src=x onerror=alert(1)&gt;</p>')
    expect(html).not.toMatch(/href=|<a/)
    expect(html).toContain('<p>[run](javascript:alert(1)) and [also](vbscript:x)</p>')
  })

  it('opens a relative link in the editor, resolved against the folder, and a web link outside the app', () => {
    const { html } = renderMarkdown('[users](users.md), [up](../README.md#top), [here](#intro), [web](https://example.net/x)\n', { dir })
    expect(html).toContain('<a href="#" data-md-link="/repo/docs/users.md">users</a>')
    expect(html).toContain('<a href="#" data-md-link="/repo/README.md">up</a>')
    expect(html).toContain('<a href="#intro">here</a>')
    expect(html).toContain('<a href="https://example.net/x" target="_blank" rel="noopener noreferrer">web</a>')
    expect(resolveRelative('/repo/docs', '../img/../a.md')).toBe('/repo/a.md')
    expect(resolveRelative('/repo/docs', '/abs/b.md')).toBe('/abs/b.md')
    expect(resolveRelative('/repo/docs', './c.md')).toBe('/repo/docs/c.md')
  })

  it('names the file a local link or image means: the encoding markdown-it gives undone once, the query and fragment cut', () => {
    expect(localPath('my%20notes.md')).toBe('my notes.md')
    expect(localPath('a%2520b.md')).toBe('a%20b.md')
    expect(localPath('a%23b.md#x')).toBe('a#b.md')
    expect(localPath('c.md?x=1#y')).toBe('c.md')
    expect(localPath('%E0%A4%A.md')).toBe('%E0%A4%A.md')
    const { html } = renderMarkdown('[n](my%20notes.md) [j](日本.md) [p](a%2520b.md) [h](a%23b.md#x) ![s](im%20g.png)\n', { dir, rawUrl })
    expect(html).toContain('data-md-link="/repo/docs/my notes.md"')
    expect(html).toContain('data-md-link="/repo/docs/日本.md"')
    expect(html).toContain('data-md-link="/repo/docs/a%20b.md"')
    expect(html).toContain('data-md-link="/repo/docs/a#b.md"')
    expect(html).toContain(`src="${rawUrl('/repo/docs/im g.png').replace(/&/g, '&amp;')}"`)
  })

  it('holds a Mermaid diagram that would reach outside the page, and draws one that would not', () => {
    expect(mermaidFetches('flowchart LR\n  A[Yard] --> B[Roundhouse]\n')).toBeNull()
    expect(mermaidFetches('sequenceDiagram\n  a->>b: the image is at /img/a.png\n')).toBeNull()
    expect(mermaidFetches('flowchart LR\n  A@{ img: "https://attacker.invalid/p.png", h: 60 }\n')).toMatch(/image node/)
    expect(mermaidFetches('flowchart LR\n  A@{\n    shape: image,\n    img: \'x.png\'\n  }\n')).toMatch(/image node/)
    expect(mermaidFetches('flowchart LR\n  A --> B\n  style A fill:url(https://attacker.invalid/x.svg#p)\n')).toMatch(/url\(/)
    expect(mermaidFetches('%%{init: {"themeCSS": "@import url(https://attacker.invalid/x.css);"}}%%\nflowchart LR\n  A --> B\n')).toMatch(/url\(/)
    expect(mermaidFetches('flowchart LR\n  A["<img src=https://attacker.invalid/p.png>"]\n')).toMatch(/HTML element/)
    expect(mermaidFetches('flowchart LR\n  A["<a href=https://attacker.invalid>go</a>"]\n')).toMatch(/HTML element/)
    expect(mermaidFetches('flowchart LR\n  A["`**bold** src=https://attacker.invalid`"]\n')).toMatch(/attribute/)
    const ids = new Set([nextMermaidId(), nextMermaidId(), nextMermaidId()])
    expect(ids.size).toBe(3)
    for (const id of ids) expect(id).toMatch(/^md-mermaid-\d+$/)
  })

  it('loads a relative image through the session, holds one on the web unless allowed, and shows the alt text otherwise', () => {
    const { html } = renderMarkdown('![a diagram](img/a.png) ![remote](https://example.net/r.png)\n', { dir, rawUrl })
    expect(html).toContain('<img src="/api/sessions/s1/files?path=%2Frepo%2Fdocs%2Fimg%2Fa.png&amp;raw=1" alt="a diagram">')
    expect(html).toContain('<span class="md-image-held" title="An image on the web is not loaded here">remote</span>')
    const allowed = renderMarkdown('![remote](https://example.net/r.png)\n', { dir, rawUrl, allowRemote: true }).html
    expect(allowed).toContain('<img src="https://example.net/r.png" alt="remote">')
    const noRaw = renderMarkdown('![local](a.png)\n', { dir }).html
    expect(noRaw).toContain('<span class="md-image-held" title="a.png">local</span>')
    expect(noRaw).not.toContain('<img')
    // An image embedded in the file fetches nothing: shown; a data URL of anything else is no image to markdown-it.
    const embedded = renderMarkdown('![dot](data:image/png;base64,iVBORw0KGgo=) ![page](data:text/html;base64,PHA+)\n', { dir }).html
    expect(embedded).toContain('<img src="data:image/png;base64,iVBORw0KGgo=" alt="dot">')
    expect(embedded).not.toMatch(/src="data:text/)
    expect(embedded).toContain('![page](data:text/html;base64,PHA+)</p>')
  })

  it('marks a mermaid fence for drawing, its source kept as text, and counts them', () => {
    const { html, mermaid } = renderMarkdown('```mermaid\nflowchart LR\n  A --> B\n```\n\n```mermaid\nsequenceDiagram\n  a->>b: hi\n```\n', { dir })
    expect(mermaid).toBe(2)
    expect(html).toContain('<pre class="md-mermaid" data-mermaid>flowchart LR\n  A --&gt; B\n</pre>')
    expect(html).not.toContain('<svg')
  })
})
