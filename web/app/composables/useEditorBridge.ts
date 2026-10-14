import type { Ref } from 'vue'
import { NvimHolds } from '~/utils/nvimHold'
import { CONNECTION_LOST, NvimLive } from '~/utils/nvimLost'
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
  // connected: the welcome's connection is up (a close ends it; the next welcome brings it back), for what needs the machine now.
  const offer = ref({ welcome: false, nvim: false, fileEdit: false, ownerOnly: false, machine: undefined as string | undefined, settled: false, connected: false })
  const listeners = new Set<(ev: NvimEvent) => void>()
  // The Neovims of tabs not in front holding changes not written; they live on this connection, so they go with it (reset).
  const holds = new NvimHolds()
  // The editors this connection has: they end with it, and are told lost then (each tab keeps its text, Reopen).
  const live = new NvimLive()
  function onNvim(ev: NvimEvent) {
    live.note(ev)
    if (ev.kind === 'closed' && ev.id) holds.ended(ev.id, ev.reason)
    for (const cb of listeners) cb(ev)
  }
  function lost(ids: string[]) {
    for (const id of ids) onNvim({ t: 'nvim_event', id, kind: 'closed', reason: CONNECTION_LOST })
  }
  /** The terminal's connection closed: its editors ended with it on the machine. */
  function onClosed() {
    offer.value = { ...offer.value, connected: false }
    lost(live.ended())
  }
  function onWelcome(w: Welcome) {
    // Another subscription's welcome: the last one's editors ended with it, though no close was seen.
    lost(live.welcome(w.subscriberId))
    offer.value = { welcome: true, nvim: !!w.nvim, fileEdit: !!w.fileEdit, ownerOnly: !!w.nvimOwnerOnly, machine: machine(), settled: !!w.nvimSettled, connected: true }
  }
  function reset() {
    offer.value = { welcome: false, nvim: false, fileEdit: false, ownerOnly: false, machine: undefined, settled: false, connected: false }
    holds.clear()
    live.clear()
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
  return { offer, nvim, onNvim, onWelcome, onClosed, reset, writeFile, canEdit }
}
