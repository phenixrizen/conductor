import type { InjectionKey } from 'vue'
import type { ChatQuote, FileRequester } from '~/utils/protocol'
import { relocate, type Relocation } from '~/utils/quote'

/**
 * What a page with an editor does for a quote card in the chat (design
 * round 12, F7): where the quoted lines are in the file now, and opening
 * the file there. Provided by the page; a chat with no editor (a run's
 * page) has neither, and the card shows the lines alone.
 */
export interface ChatQuoteActions {
  relocate: (q: ChatQuote) => Promise<Relocation | null>
  open: (q: ChatQuote, at?: { from: number; to: number }) => void
}

const key: InjectionKey<ChatQuoteActions> = Symbol('chatQuotes')

export function provideChatQuotes(actions: ChatQuoteActions) {
  provide(key, actions)
}

export function useChatQuotes(): ChatQuoteActions | null {
  return inject(key, null)
}

/**
 * The quote actions of a page with an editor: the file read over the page's
 * terminal (kept 15 s per path, so a thread of quotes reads each file once)
 * and opened in its editor at the range.
 */
export function quoteActionsFor(request: FileRequester, cwd: () => string | undefined, openFile: (loc: { path: string; line?: number; lineTo?: number }) => void): ChatQuoteActions {
  const cache = new Map<string, { at: number; lines: Promise<string[] | null> }>()
  const abs = (path: string) => (path.startsWith('/') || !cwd() ? path : `${cwd()!.replace(/\/+$/, '')}/${path}`)
  function linesOf(path: string): Promise<string[] | null> {
    const hit = cache.get(path)
    if (hit && Date.now() - hit.at < 15_000) return hit.lines
    const lines = request(path, false)
      .then((res) => (res.header.kind === 'file' && !res.header.binary ? new TextDecoder().decode(res.body).split('\n') : null))
      .catch(() => null)
    cache.set(path, { at: Date.now(), lines })
    return lines
  }
  return {
    relocate: async (q) => {
      const lines = await linesOf(abs(q.path))
      return lines ? relocate(q, lines) : null
    },
    open: (q, at) => openFile({ path: abs(q.path), line: at?.from ?? q.from, lineTo: at?.to ?? q.to }),
  }
}
