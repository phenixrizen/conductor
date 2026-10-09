import type { Ref } from 'vue'
import type { FileResponse, FileWriteOptions, NvimBridge, NvimEvent, NvimSwapChoice, Welcome } from '~/utils/protocol'

/** What a page's terminal offers the editor: Neovim (F8) and saves (F6), over its connection. */
export interface EditorTerminal {
  nvimOpen: (path: string) => Promise<NvimEvent>
  nvimInput: (id: string, keys: string) => void
  nvimClose: (id: string) => void
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
  function onNvim(ev: NvimEvent) {
    for (const cb of listeners) cb(ev)
  }
  function onWelcome(w: Welcome) {
    offer.value = { welcome: true, nvim: !!w.nvim, fileEdit: !!w.fileEdit, machine: machine() }
  }
  function reset() {
    offer.value = { welcome: false, nvim: false, fileEdit: false, machine: undefined }
  }
  const nvim = computed<NvimBridge>(() => ({
    offer: offer.value,
    open: (path: string) => (terminal.value ? terminal.value.nvimOpen(path) : Promise.reject(new Error('terminal not ready'))),
    input: (id: string, keys: string) => terminal.value?.nvimInput(id, keys),
    close: (id: string) => terminal.value?.nvimClose(id),
    swap: (id: string, choice: NvimSwapChoice) => terminal.value?.nvimSwap(id, choice),
    subscribe: (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
  }))
  function writeFile(path: string, data: Uint8Array, opts?: FileWriteOptions): Promise<FileResponse> {
    return terminal.value ? terminal.value.writeFile(path, data, opts) : Promise.reject(new Error('terminal not ready'))
  }
  const canEdit = computed(() => offer.value.welcome && offer.value.fileEdit)
  return { offer, nvim, onNvim, onWelcome, reset, writeFile, canEdit }
}
