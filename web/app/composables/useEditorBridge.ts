import type { Ref } from 'vue'
import { NvimHolds } from '~/utils/nvimHold'
import type { FileResponse, FileWriteOptions, NvimBridge, NvimEvent, NvimSwapChoice, Welcome } from '~/utils/protocol'

/** What a page's terminal offers the editor: Neovim (F8) and saves (F6), over its connection. */
export interface EditorTerminal {
  nvimOpen: (path: string) => Promise<NvimEvent>
  nvimInput: (id: string, keys: string, seq?: number) => void
  nvimClose: (id: string, discard?: boolean) => void
  nvimSwap: (id: string, choice: NvimSwapChoice) => void
  writeFile: (path: string, data: Uint8Array, opts?: FileWriteOptions) => Promise<FileResponse>
}

/**
 * The editor's side of a page (design round 12, F6 and F8): what the
 * welcome allows this connection (editing, and Neovim on the machine), the
 * Neovim bridge, the save. The session page, the Yard's focused tile and
 * the guest page share it; `reset` forgets the welcome when the terminal
 * changes (the Yard's focus moving).
 */
export function useEditorBridge(terminal: Ref<EditorTerminal | null>, machine: () => string | undefined) {
  const offer = ref({ welcome: false, nvim: false, fileEdit: false, machine: undefined as string | undefined })
  const listeners = new Set<(ev: NvimEvent) => void>()
  // The Neovims of tabs not in front holding changes not written; they live on this connection, so they go with it (reset).
  const holds = new NvimHolds()
  function onNvim(ev: NvimEvent) {
    if (ev.kind === 'closed' && ev.id) holds.ended(ev.id)
    for (const cb of listeners) cb(ev)
  }
  function onWelcome(w: Welcome) {
    offer.value = { welcome: true, nvim: !!w.nvim, fileEdit: !!w.fileEdit, machine: machine() }
  }
  function reset() {
    offer.value = { welcome: false, nvim: false, fileEdit: false, machine: undefined }
    holds.clear()
  }
  const nvim = computed<NvimBridge>(() => ({
    offer: offer.value,
    open: (path: string) => (terminal.value ? terminal.value.nvimOpen(path) : Promise.reject(new Error('terminal not ready'))),
    input: (id: string, keys: string, seq?: number) => terminal.value?.nvimInput(id, keys, seq),
    close: (id: string, discard?: boolean) => terminal.value?.nvimClose(id, discard),
    swap: (id: string, choice: NvimSwapChoice) => terminal.value?.nvimSwap(id, choice),
    subscribe: (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    holds,
  }))
  function writeFile(path: string, data: Uint8Array, opts?: FileWriteOptions): Promise<FileResponse> {
    return terminal.value ? terminal.value.writeFile(path, data, opts) : Promise.reject(new Error('terminal not ready'))
  }
  const canEdit = computed(() => offer.value.welcome && offer.value.fileEdit)
  return { offer, nvim, onNvim, onWelcome, reset, writeFile, canEdit }
}
