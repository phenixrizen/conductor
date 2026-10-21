<script setup lang="ts">
import { MERMAID_PURIFY, mermaidFetches, nextMermaidId, renderMarkdown } from '~/utils/markdown'
/**
 * A Markdown file rendered (#166): the editor's Preview. The HTML is
 * renderMarkdown's (raw HTML never rendered, links and images under its
 * rules), set into the host and then enhanced: fenced code highlighted with
 * shiki in the editor's theme, and Mermaid blocks drawn by Mermaid, loaded
 * only when the file has one (it is large). A Mermaid diagram reaches the
 * page three ways short: a source that would load from a URL (an image
 * node, a url( in its CSS, an HTML element in a label; mermaidFetches) is
 * not drawn; Mermaid runs with securityLevel strict and a DOMPurify
 * configuration that forbids every label element and attribute that loads
 * or navigates (its own forbids only style); and the SVG it gives is
 * scrubbed of any such element or attribute before it goes in, its bind
 * functions never called. A block Mermaid cannot draw keeps its source
 * with Mermaid's error above it. The theme switch draws the diagrams
 * again. Each pass of enhancement is numbered; a pass overtaken by a later
 * one, or by the preview going, stops at its next step. A click on a
 * relative link asks the editor to open that file.
 */
const props = defineProps<{
  text: string
  /** The file's folder, which relative links and images resolve against. */
  dir: string
  rawUrl?: (path: string) => string | null
}>()
const emit = defineEmits<{ open: [path: string] }>()

const host = ref<HTMLElement | null>(null)
const colorMode = useColorMode()
const dark = computed(() => colorMode.value === 'dark')
const rendered = computed(() => renderMarkdown(props.text, { dir: props.dir, rawUrl: props.rawUrl }))

/** Code shiki highlights at most; past it the block stays plain. */
const HIGHLIGHT_LIMIT = 100_000
let pass = 0

async function enhance() {
  const el = host.value
  if (!el) return
  const n = ++pass
  el.innerHTML = rendered.value.html
  await highlightCode(el, n)
  if (n !== pass) return
  if (rendered.value.mermaid > 0) await drawMermaid(el, n)
}

async function highlightCode(el: HTMLElement, n: number) {
  const blocks = Array.from(el.querySelectorAll<HTMLElement>('pre[data-code-lang]'))
  if (!blocks.length) return
  const shiki = await import('shiki')
  for (const pre of blocks) {
    if (n !== pass) return
    const lang = pre.dataset.codeLang ?? ''
    const source = pre.textContent ?? ''
    if (!lang || source.length > HIGHLIGHT_LIMIT) continue
    try {
      const html = await shiki.codeToHtml(source, {
        lang,
        theme: dark.value ? 'github-dark' : 'github-light',
        transformers: [
          {
            pre(node) {
              node.properties.style = ''
              node.properties.class = 'shiki md-code'
            },
          },
        ],
      })
      if (n !== pass) return
      pre.outerHTML = html
    } catch {
      // A language shiki does not have: the block stays as it is.
    }
  }
}

async function drawMermaid(el: HTMLElement, n: number) {
  const blocks = Array.from(el.querySelectorAll<HTMLElement>('pre[data-mermaid]'))
  if (!blocks.length) return
  const { default: mermaid } = await import('mermaid')
  if (n !== pass) return
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', dompurifyConfig: MERMAID_PURIFY, theme: dark.value ? 'dark' : 'default', fontFamily: 'inherit' })
  for (const pre of blocks) {
    if (n !== pass) return
    const source = pre.textContent ?? ''
    const fetches = mermaidFetches(source)
    if (fetches) {
      hold(pre, `This diagram is not drawn: ${fetches}. Diagrams here load nothing from outside the page.`, 'held')
      continue
    }
    try {
      const { svg } = await mermaid.render(nextMermaidId(), source)
      if (n !== pass) return
      const figure = document.createElement('div')
      figure.className = 'md-diagram'
      figure.setAttribute('data-mermaid-rendered', '')
      figure.innerHTML = svg
      scrub(figure)
      pre.replaceWith(figure)
    } catch (e) {
      if (n !== pass) return
      hold(pre, `Mermaid could not draw this block: ${(e as Error).message || String(e)}`, 'error')
    }
  }
}

/** Keeps a Mermaid block as its source, with why above it. */
function hold(pre: HTMLElement, why: string, kind: 'held' | 'error') {
  pre.classList.add('md-mermaid-error')
  pre.setAttribute(kind === 'held' ? 'data-mermaid-held' : 'data-mermaid-error', '')
  const note = document.createElement('div')
  note.className = 'md-error'
  note.textContent = why
  pre.prepend(note)
}

const LOADING_TAGS = new Set(['image', 'img', 'picture', 'source', 'video', 'audio', 'iframe', 'object', 'embed', 'link', 'use', 'script', 'a', 'form', 'meta', 'base'])
const URL_ATTRS = ['src', 'srcset', 'href', 'xlink:href', 'poster', 'background', 'action', 'formaction', 'data', 'ping']
/** Takes out of Mermaid's SVG anything that would load a resource or navigate; a style's url( goes too. The diagram's own style stays. */
function scrub(root: HTMLElement) {
  for (const el of Array.from(root.querySelectorAll('*'))) {
    if (LOADING_TAGS.has(el.localName)) {
      el.remove()
      continue
    }
    for (const a of URL_ATTRS) {
      const v = el.getAttribute(a)
      if (v !== null && !v.startsWith('#')) el.removeAttribute(a)
    }
    const style = el.getAttribute('style')
    if (style && /url\s*\(/i.test(style)) el.setAttribute('style', style.replace(/url\s*\([^)]*\)/gi, 'none'))
    if (el.localName === 'style' && /url\s*\(|@import/i.test(el.textContent ?? '')) el.textContent = (el.textContent ?? '').replace(/url\s*\([^)]*\)/gi, 'none').replace(/@import[^;]*;?/gi, '')
  }
}

function onClick(e: MouseEvent) {
  const a = (e.target as HTMLElement | null)?.closest?.('a[data-md-link]') as HTMLAnchorElement | null
  if (!a) return
  e.preventDefault()
  const path = a.getAttribute('data-md-link')
  if (path) emit('open', path)
}

onMounted(() => void enhance())
onBeforeUnmount(() => {
  pass++
})
watch([rendered, dark], () => void nextTick(enhance))
defineExpose({ focus: () => host.value?.focus() })
</script>

<template>
  <div ref="host" class="md-preview h-full overflow-auto px-5 py-4 text-sm leading-relaxed text-default outline-none" data-md-preview tabindex="-1" @click="onClick" />
</template>

<style>
.md-preview h1,
.md-preview h2,
.md-preview h3,
.md-preview h4 {
  font-weight: 600;
  color: var(--ui-text-highlighted);
  margin: 1.25em 0 0.5em;
  line-height: 1.25;
}
.md-preview h1 {
  font-size: 1.5rem;
  margin-top: 0;
}
.md-preview h2 {
  font-size: 1.25rem;
  border-bottom: 1px solid var(--ui-border);
  padding-bottom: 0.25em;
}
.md-preview h3 {
  font-size: 1.05rem;
}
.md-preview p,
.md-preview ul,
.md-preview ol,
.md-preview blockquote,
.md-preview table,
.md-preview pre,
.md-preview .md-diagram {
  margin: 0.75em 0;
}
.md-preview ul {
  list-style: disc;
  padding-left: 1.5em;
}
.md-preview ol {
  list-style: decimal;
  padding-left: 1.5em;
}
.md-preview li.task {
  list-style: none;
  margin-left: -1.25em;
}
.md-preview li.task input {
  margin-right: 0.4em;
  vertical-align: -0.1em;
}
.md-preview a {
  color: var(--ui-primary);
  text-decoration: underline;
  text-underline-offset: 2px;
}
.md-preview code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.9em;
  background: var(--ui-bg-elevated);
  border-radius: 0.25rem;
  padding: 0.1em 0.3em;
}
.md-preview pre {
  overflow-x: auto;
  background: var(--ui-bg-elevated);
  border: 1px solid var(--ui-border);
  border-radius: 0.375rem;
  padding: 0.75em 1em;
}
.md-preview pre code {
  background: none;
  padding: 0;
  font-size: 0.85em;
}
.md-preview blockquote {
  border-left: 3px solid var(--ui-border-accented);
  padding-left: 1em;
  color: var(--ui-text-muted);
}
.md-preview table {
  border-collapse: collapse;
  display: block;
  overflow-x: auto;
}
.md-preview th,
.md-preview td {
  border: 1px solid var(--ui-border);
  padding: 0.3em 0.75em;
  text-align: left;
}
.md-preview th {
  background: var(--ui-bg-elevated);
  font-weight: 600;
}
.md-preview img {
  max-width: 100%;
}
.md-preview .md-image-held {
  color: var(--ui-text-muted);
  font-style: italic;
}
.md-preview .md-diagram svg {
  max-width: 100%;
  height: auto;
}
.md-preview .md-mermaid-error {
  border-color: var(--ui-error);
}
.md-preview .md-error {
  color: var(--ui-error);
  font-family: inherit;
  margin-bottom: 0.5em;
  white-space: pre-wrap;
}
</style>
