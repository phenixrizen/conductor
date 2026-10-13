<script setup lang="ts">
import type { editor as MonacoEditor } from 'monaco-editor'
import type { NvimBridge, NvimEvent, NvimSwapChoice } from '~/utils/protocol'
import type { NvimViewState } from '~/utils/nvimSwap'
import { cursorStyleFor, isVisual, keptByConductor, keyToNvim, textToNvim } from '~/utils/nvimKeys'
import { byteColToUtf16, linesEdit } from '~/utils/nvimLines'
import { ECHO_HOLD_MS, NvimEcho, type EchoDoc, type EchoLines } from '~/utils/nvimEcho'
import { heldChanges, type HeldNvim } from '~/utils/nvimHold'
import { closedByQuit, lostHoldsChanges } from '~/utils/nvimLost'
import { hasModel, isDirty, markSaved, noteVersion } from '~/utils/editorModels'

/**
 * A file in Monaco (design 4b): the gutter with folding, the minimap, find
 * and replace (Ctrl+F, Ctrl+H), go to line (Ctrl+G), the line asked for
 * shown and marked for a moment. Monaco loads with the first editor on the
 * page (`utils/monaco.ts`, a chunk of its own). Read-only until F6.
 */
const props = withDefaults(defineProps<{ path: string; text: string; line?: number; readOnly?: boolean; nvim?: NvimBridge }>(), { line: undefined, readOnly: true, nvim: undefined })
const emit = defineEmits<{
  cursor: [pos: { line: number; col: number }]
  ready: []
  nvim: [state: NvimViewState]
  nvimClosed: []
  dirty: [dirty: boolean]
  /** Lines selected (F7): the range and where below it, in the editor's pixels, a bar can sit; null when nothing is. */
  selection: [sel: { from: number; to: number; top: number; left: number } | null]
}>()

const host = ref<HTMLElement>()
const colorMode = useColorMode()
let editor: MonacoEditor.IStandaloneCodeEditor | null = null
let model: MonacoEditor.ITextModel | null = null
let monacoRef: typeof import('monaco-editor') | null = null
// The model was made for this showing (not kept from the tab's last): a held Neovim's text is not in it.
let freshModel = true
let marks: MonacoEditor.IEditorDecorationsCollection | null = null

const theme = computed(() => (colorMode.value === 'dark' ? 'conductor-dark' : 'conductor-light'))

async function mount() {
  const { loadMonaco, languageFor } = await import('~/utils/monaco')
  const monaco = loadMonaco()
  monacoRef = monaco
  if (!host.value) return
  const uri = monaco.Uri.parse(`conductor://file${props.path}`)
  // A model lives for its tab: unsaved edits (F6) stay across the tab being switched away from and back.
  const existing = monaco.editor.getModel(uri)
  model = existing ?? monaco.editor.createModel(props.text, languageFor(props.path), uri)
  // A Neovim held for this tab (its changes not written) is what the model mirrors: the text read from disk is not.
  const held = !!props.nvim?.holds.peek(props.path)
  freshModel = !existing || !hasModel(props.path)
  if (freshModel) {
    if (model.getValue() !== props.text) model.setValue(props.text)
    markSaved(props.path, model.getAlternativeVersionId())
  } else if (!held && !isDirty(props.path, model.getAlternativeVersionId()) && model.getValue() !== props.text) {
    model.setValue(props.text)
    markSaved(props.path, model.getAlternativeVersionId())
  }
  noteVersion(props.path, model.getAlternativeVersionId())
  contentSub = model.onDidChangeContent(() => {
    if (!model) return
    noteVersion(props.path, model.getAlternativeVersionId())
    if (nvimId) markSaved(props.path, model.getAlternativeVersionId()) // Neovim's buffer, mirrored: Neovim keeps its own
    tellDirty()
  })
  editor = monaco.editor.create(host.value, {
    model,
    theme: theme.value,
    readOnly: props.readOnly,
    // The textarea, not Chromium's EditContext div: the page's single-key shortcuts know a textarea is for typing and pause.
    editContext: false,
    automaticLayout: true,
    minimap: { enabled: true, renderCharacters: false },
    folding: true,
    lineNumbers: 'on',
    glyphMargin: false,
    fontFamily: '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: 13,
    lineHeight: 20,
    scrollBeyondLastLine: false,
    renderLineHighlight: 'line',
    smoothScrolling: true,
    cursorBlinking: 'smooth',
    padding: { top: 8 },
    fixedOverflowWidgets: true,
    // Enter takes a suggestion only when it changes the text: the word just typed, offered back, leaves Enter to break the line.
    acceptSuggestionOnEnter: 'smart',
  })
  editor.onDidChangeCursorPosition((e) => emit('cursor', { line: e.position.lineNumber, col: e.position.column }))
  editor.onDidChangeCursorSelection(() => tellSelection())
  editor.onDidScrollChange(() => tellSelection())
  emit('cursor', { line: 1, col: 1 })
  if (props.line) showLine(props.line)
  tellDirty()
  emit('ready')
  if (props.nvim) void startNvim()
}

// The Neovim keymap (design round 12, F8): the real Neovim on the session's
// machine holds the file; every key goes to it, its changes come back into
// the model, its cursor, mode, command line and messages show. Monaco stays
// read-only, so nothing is typed into the text except by Neovim.
let contentSub: { dispose: () => void } | null = null
function tellDirty() {
  // Under the Neovim keymap Neovim's buffer is the one with changes or none (its `modified` event); the model only mirrors it. Characters
  // typed that it has not had yet (the local echo's guesses) are changes too: the dot, and a close asks.
  if (props.nvim) emit('dirty', nvimState.modified || !!echo?.showing)
  else if (model) emit('dirty', isDirty(props.path, model.getAlternativeVersionId()))
}
/** The text now, for a save. */
function getText(): string {
  return model?.getValue() ?? ''
}
/** The model's version now: what a save sends is this version's text (editorModels.savedAt). */
function version(): number | undefined {
  return model?.getAlternativeVersionId()
}
/** The file as read again (a reload): the model takes it, unsaved edits dropped. */
function setText(text: string) {
  if (!model) return
  if (model.getValue() !== text) model.setValue(text)
  markSaved(props.path, model.getAlternativeVersionId())
  tellDirty()
}
let nvimId: string | null = null
// The file its Neovim shows, as Neovim names it (its `opened` events): a write of it is this tab's.
let nvimPath: string | null = null
// The Neovim lost (utils/nvimLost) whose text this tab shows: the changes it sent before its end, which can come after it, still apply,
// reconciled with the characters typed that it never acknowledged (lostEcho, the local echo finished, while any stay).
let lostId: string | null = null
let lostEcho: NvimEcho | null = null
let unsubscribe: (() => void) | null = null
// The local echo (round 13, G5): plain characters typed in insert mode show at once, Neovim's acknowledgement settles them.
let echo: NvimEcho | null = null
let holdTimer: ReturnType<typeof setTimeout> | undefined
let guessMarks: MonacoEditor.IEditorDecorationsCollection | null = null
function applyLinesEvent(ev: EchoLines) {
  if (!model || !monacoRef) return
  const edit = linesEdit(ev, model.getLineCount(), (n) => model!.getLineLength(n))
  model.applyEdits([{ range: new monacoRef.Range(edit.range.startLineNumber, edit.range.startColumn, edit.range.endLineNumber, edit.range.endColumn), text: edit.text }])
}
function markGuesses() {
  if (!guessMarks || !monacoRef) return
  const r = echo?.range()
  guessMarks.set(r ? [{ range: new monacoRef.Range(r.line, r.from, r.line, r.to), options: { inlineClassName: 'nvim-guess' } }] : [])
}
function stopEcho() {
  clearTimeout(holdTimer)
  holdTimer = undefined
  echo = null
  guessMarks?.clear()
  guessMarks = null
}
/** The editor's text as the local echo reaches it. */
function echoDoc(m: MonacoEditor.ITextModel): EchoDoc {
  return {
    line: (n) => (n >= 1 && n <= m.getLineCount() ? m.getLineContent(n) : ''),
    setLine: (n, text) => {
      if (n >= 1 && n <= m.getLineCount() && monacoRef) m.applyEdits([{ range: new monacoRef.Range(n, 1, n, m.getLineMaxColumn(n)), text }])
    },
    applyLines: applyLinesEvent,
  }
}
const nvimState: NvimViewState = { mode: 'n', cmdline: '', message: '', messageKind: '', swap: null, recovered: false, modified: false }
function tellNvim() {
  nvimState.unsettled = !!echo?.unsettled
  emit('nvim', { ...nvimState })
}
async function startNvim() {
  const bridge = props.nvim
  if (!bridge || !editor || !model || !monacoRef) return
  // This tab's Neovim, held while another tab was in front with its changes not written: taken back as it was.
  const held = bridge.holds.take(props.path)
  if (held?.state.lost && !freshModel) {
    // Lost (utils/nvimLost) while away, or before the tab was last left: its text stays, read only, until Reopen or a close.
    Object.assign(nvimState, held.state)
    lostId = held.id
    lostEcho = held.echo ?? null
    lostEcho?.rebind(echoDoc(model))
    unsubscribe = bridge.subscribe(onNvimEvent)
    editor.setPosition({ lineNumber: held.cursor.line, column: held.cursor.col })
    editor.revealPositionInCenterIfOutsideViewport({ lineNumber: held.cursor.line, column: held.cursor.col })
    tellNvim()
    tellDirty()
    return
  }
  let resumed: NvimEcho | null = null
  if (held && !held.state.lost && !freshModel) {
    resumed = held.echo ?? null
    nvimId = held.id
    // Keys refused while away are said now, on the status line (the state's message), and no longer kept for it.
    Object.assign(nvimState, held.state, { refused: undefined })
    editor.setPosition({ lineNumber: held.cursor.line, column: held.cursor.col })
    editor.revealPositionInCenterIfOutsideViewport({ lineNumber: held.cursor.line, column: held.cursor.col })
  } else {
    // Its model is gone with the text it mirrored: that Neovim ends as a lost connection ends it, its swap file keeping the changes.
    if (held && !held.state.lost) bridge.close(held.id)
    try {
      const opened = await bridge.open(props.path)
      if (!editor) return
      nvimId = opened.id ?? null
      nvimPath = opened.path ?? null
    } catch (err) {
      nvimState.message = err instanceof Error ? err.message : String(err)
      nvimState.messageKind = 'emsg'
      tellNvim()
      return
    }
  }
  if (!nvimId) return
  // The echo it had as the tab was left goes on, its characters not acknowledged yet still guesses; else a new one.
  if (resumed) {
    echo = resumed
    echo.rebind(echoDoc(model))
  } else echo = new NvimEcho(echoDoc(model))
  echo.setMode(nvimState.mode)
  guessMarks = editor.createDecorationsCollection()
  markGuesses()
  unsubscribe = bridge.subscribe(onNvimEvent)
  editor.updateOptions({ cursorStyle: cursorStyleFor(nvimState.mode), cursorBlinking: 'solid' })
  editor.onKeyDown((e) => {
    if (!nvimId || !bridge) return
    const be = e.browserEvent
    // A key an input method or a dead key is composing with: theirs, not Neovim's (round 13, G4).
    if (be.isComposing || be.keyCode === 229) return
    if (keptByConductor(be)) return
    const keys = keyToNvim(be)
    if (keys === null) return
    e.preventDefault()
    e.stopPropagation()
    sendKeys(bridge, keys)
  })
  listenForText(bridge)
  tellNvim()
  tellDirty()
}

/** Sends keys with their number; a plain character in insert mode shows at once (the local echo). */
function sendKeys(bridge: NvimBridge, keys: string) {
  if (!nvimId) return
  const at = editor?.getPosition()
  const r = echo ? echo.send(keys, { line: at?.lineNumber ?? 1, col: at?.column ?? 1 }) : { seq: 0 }
  bridge.input(nvimId, keys, r.seq || undefined)
  if (r.cursor && editor) {
    editor.setPosition({ lineNumber: r.cursor.line, column: r.cursor.col })
    markGuesses()
    tellDirty()
  }
  if (r.seq && !nvimState.unsettled) tellNvim()
}

// Text with no key press (round 13, G4): an input method's composed word, a dead key's character, dictation. Taken on the way down to
// Monaco's text area (the capture phase on the editor's own element), so Monaco, read-only here, never sees it, and sent to Neovim as keys.
let stopText: (() => void) | null = null
function listenForText(bridge: NvimBridge) {
  const dom = editor?.getDomNode()
  if (!dom) return
  let composing = false
  const send = (text: string) => {
    if (!nvimId) return
    for (const keys of textToNvim(text)) sendKeys(bridge, keys)
  }
  const clear = (t: EventTarget | null) => {
    if (t instanceof HTMLTextAreaElement) t.value = ''
  }
  const onStart = (e: Event) => {
    composing = true
    e.stopPropagation()
  }
  const onUpdate = (e: Event) => e.stopPropagation()
  const onEnd = (e: Event) => {
    composing = false
    e.stopPropagation()
    const data = (e as CompositionEvent).data
    if (data) send(data)
    clear(e.target)
  }
  const onBeforeInput = (e: Event) => {
    const ie = e as InputEvent
    if (composing || ie.isComposing || ie.inputType !== 'insertText' || !ie.data) return
    ie.preventDefault()
    ie.stopPropagation()
    send(ie.data)
  }
  const onInput = (e: Event) => {
    // Monaco is read-only here: what reaches its text area is not its to type (a composition's text waits for its end).
    e.stopPropagation()
    if (!composing) clear(e.target)
  }
  const pairs: Array<[string, (e: Event) => void]> = [
    ['compositionstart', onStart],
    ['compositionupdate', onUpdate],
    ['compositionend', onEnd],
    ['beforeinput', onBeforeInput],
    ['input', onInput],
  ]
  for (const [type, fn] of pairs) dom.addEventListener(type, fn, true)
  stopText = () => {
    for (const [type, fn] of pairs) dom.removeEventListener(type, fn, true)
    stopText = null
  }
}
/**
 * While the tab is not in front (its Neovim held, or lost), its Neovim's changes still reach its text: a change on its way as the tab
 * was left, one a plugin makes later, the last ones a lost Neovim sent, which can come after its end. Its word on changes not written
 * goes to the held state. The holds stop it as the Neovim leaves them (taken back, its tab closed).
 */
function follow(bridge: NvimBridge, id: string, echoAsLeft: NvimEcho | null): () => void {
  const m = model
  const monaco = monacoRef
  const path = props.path
  // Kept only for keys on their way: once Neovim has done them (settled), with no change and nothing typed left to have, it goes, as a
  // clean tab's Neovim goes when the tab is left.
  const letGoIfClean = (h: HeldNvim) => {
    if (h.state.lost || heldChanges(h) || h.echo?.unsettled || h.state.refused) return
    bridge.holds.take(path)
    bridge.close(id)
  }
  return bridge.subscribe((ev) => {
    if (ev.id !== id || !m || !monaco || m.isDisposed()) return
    const h = bridge.holds.peek(path)
    if (h?.id !== id) return
    if (ev.kind === 'lines') {
      const lev = { first: ev.first ?? 0, last: ev.last ?? 0, lines: ev.lines ?? [] }
      if (echoAsLeft) {
        // The characters typed it had not acknowledged as the tab was left stay laid over its text until its lines hold them.
        echoAsLeft.lateLines(lev)
        h.state.unconfirmed = echoAsLeft.unconfirmed || undefined
      } else {
        const edit = linesEdit(lev, m.getLineCount(), (n) => m.getLineLength(n))
        m.applyEdits([{ range: new monaco.Range(edit.range.startLineNumber, edit.range.startColumn, edit.range.endLineNumber, edit.range.endColumn), text: edit.text }])
      }
      if (h.state.lost && !h.state.modified) h.state.modified = true
      bridge.holds.changed(path)
    } else if (ev.kind === 'modified') {
      h.state.modified = h.state.lost ? h.state.modified || !!ev.modified : !!ev.modified
      bridge.holds.changed(path)
    } else if (ev.kind === 'cursor' && ev.ack) {
      // Its numbers go on (the next editor goes on with this echo); an acknowledgement is no sign the keys are done.
      echoAsLeft?.noteAck(ev.ack)
      bridge.holds.changed(path)
    } else if (ev.kind === 'error' && ev.ack) {
      // Keys refused while the tab was not in front: said on its status line when it is, and its Neovim kept until then.
      const keys = echoAsLeft?.noteRefused(ev.ack, ev.text) ?? ev.text ?? ''
      h.state.refused = (h.state.refused ?? '') + keys
      h.state.message = `Keys did not reach Neovim (${ev.message ?? ev.code ?? 'refused'}): ${h.state.refused}`
      h.state.messageKind = 'emsg'
      bridge.holds.changed(path)
    } else if (ev.kind === 'settled' && ev.ack) {
      echoAsLeft?.noteSettled(ev.ack)
      if (echoAsLeft) h.state.unconfirmed = echoAsLeft.unconfirmed || undefined
      bridge.holds.changed(path)
      letGoIfClean(h)
    }
  })
}
function onNvimEvent(ev: NvimEvent) {
  if (!editor || !model || !monacoRef) return
  if (ev.id && ev.id === nvimId && ev.kind === 'opened' && ev.path) nvimPath = ev.path
  if (lostId && ev.id === lostId) {
    // A lost Neovim's change that came after its end: the tab's text takes it.
    if (ev.kind === 'lines' || (ev.kind === 'modified' && ev.modified)) {
      if (ev.kind === 'lines') {
        const lev = { first: ev.first ?? 0, last: ev.last ?? 0, lines: ev.lines ?? [] }
        if (lostEcho) {
          lostEcho.lateLines(lev)
          nvimState.unconfirmed = lostEcho.unconfirmed || undefined
        } else applyLinesEvent(lev)
      }
      nvimState.modified = true
      tellNvim()
      tellDirty()
    } else if (ev.kind === 'settled' && lostEcho) {
      // Its word that keys were done, after its end: the guesses of those it made nothing of go, and what it confirmed is no longer
      // kept aside.
      lostEcho.noteSettled(ev.ack ?? 0)
      nvimState.unconfirmed = lostEcho.unconfirmed || undefined
      tellNvim()
    }
    return
  }
  if (!nvimId || ev.id !== nvimId) return
  const monaco = monacoRef
  switch (ev.kind) {
    case 'lines': {
      const lev = { first: ev.first ?? 0, last: ev.last ?? 0, lines: ev.lines ?? [] }
      if (echo?.lines(lev)) {
        // Guesses show: the change waits for its acknowledgement, which comes right after it, so the two settle at once.
        holdTimer ??= setTimeout(() => {
          holdTimer = undefined
          echo?.timeout()
          markGuesses()
          tellDirty()
        }, ECHO_HOLD_MS)
        break
      }
      applyLinesEvent(lev)
      break
    }
    case 'cursor': {
      // Neovim counts a line's bytes, Monaco its UTF-16 units.
      const toUtf16 = (l: number, c: number) => byteColToUtf16(model!.getLineContent(Math.max(1, Math.min(l, model!.getLineCount()))), Math.max(1, c))
      const shown = echo ? echo.cursor({ line: ev.line ?? 1, col: ev.col ?? 1, mode: ev.mode ?? nvimState.mode, ack: ev.ack }, toUtf16) : null
      if (echo && !echo.holding && holdTimer) {
        clearTimeout(holdTimer)
        holdTimer = undefined
      }
      markGuesses()
      if (ev.ack) tellDirty()
      const line = Math.max(1, Math.min(ev.line ?? 1, model.getLineCount()))
      const col = toUtf16(line, ev.col ?? 1)
      if (echo && !shown) {
        // A guess is ahead of Neovim's cursor: the cursor stays after it.
      } else if (ev.mode && isVisual(ev.mode) && ev.visualLine) {
        const vl = Math.max(1, Math.min(ev.visualLine, model.getLineCount()))
        const vc = byteColToUtf16(model.getLineContent(vl), Math.max(1, ev.visualCol ?? 1))
        const forward = vl < line || (vl === line && vc <= col)
        editor.setSelection(ev.mode === 'V' ? new monaco.Selection(Math.min(vl, line), 1, Math.max(vl, line), model.getLineMaxColumn(Math.max(vl, line))) : forward ? new monaco.Selection(vl, vc, line, col + 1) : new monaco.Selection(vl, vc + 1, line, col))
      } else {
        const at = shown ?? { line, col }
        editor.setPosition({ lineNumber: at.line, column: at.col })
      }
      editor.revealPositionInCenterIfOutsideViewport({ lineNumber: line, column: col })
      if (ev.mode && ev.mode !== nvimState.mode) {
        nvimState.mode = ev.mode
        editor.updateOptions({ cursorStyle: cursorStyleFor(ev.mode) })
        tellNvim()
      }
      break
    }
    case 'mode':
      if (ev.mode) {
        echo?.setMode(ev.mode)
        nvimState.mode = ev.mode
        editor.updateOptions({ cursorStyle: cursorStyleFor(ev.mode) })
        tellNvim()
      }
      break
    case 'cmdline':
      nvimState.cmdline = ev.show ? `${ev.prompt ?? ''}${ev.content ?? ''}` : ''
      tellNvim()
      break
    case 'message':
      nvimState.message = ev.messageKind === 'clear' ? '' : (ev.text ?? '')
      nvimState.messageKind = ev.messageKind ?? ''
      tellNvim()
      break
    case 'written':
      nvimState.message = nvimState.message || 'written'
      tellNvim()
      break
    case 'modified':
      nvimState.modified = !!ev.modified
      tellNvim()
      tellDirty()
      break
    case 'settled':
      echo?.noteSettled(ev.ack ?? 0)
      if (nvimState.unsettled && !echo?.unsettled) tellNvim()
      // What it held is shown now: its wait for an acknowledgement is over.
      if (echo && !echo.holding && holdTimer) {
        clearTimeout(holdTimer)
        holdTimer = undefined
      }
      markGuesses()
      tellDirty()
      break
    case 'swap':
      // Another editor's swap file: the file opened read-only (round 13, G3).
      nvimState.swap = ev.swap ?? null
      nvimState.recovered = false
      tellNvim()
      break
    case 'closed': {
      // What Neovim sent and the echo held is shown first, with the characters typed it never acknowledged.
      if (!closedByQuit(ev.reason)) echo?.markLost()
      const pending = closedByQuit(ev.reason) ? false : (echo?.finish() ?? false)
      const unconfirmed = echo?.unconfirmed ?? ''
      // Kept while guesses show or characters wait to be confirmed: what comes after its end is reconciled with them.
      const finished = pending || unconfirmed ? echo : null
      nvimId = null
      stopText?.()
      stopEcho()
      if (closedByQuit(ev.reason)) {
        unsubscribe?.()
        unsubscribe = null
        emit('nvimClosed')
        break
      }
      // Lost, not quit: the tab stays with the text Neovim last held (read only, which Monaco is here), and the area says so. Its
      // subscription stays: the changes it sent before its end can come after it.
      lostId = ev.id ?? null
      lostEcho = finished
      nvimState.lost = ev.reason ?? 'error'
      if (unconfirmed) nvimState.unconfirmed = unconfirmed
      nvimState.modified = lostHoldsChanges(nvimState.modified, pending || !!unconfirmed, model.getValue(), props.text)
      nvimState.cmdline = ''
      nvimState.message = 'Neovim ended'
      nvimState.messageKind = 'emsg'
      tellNvim()
      tellDirty()
      break
    }
    case 'error': {
      // Keys refused on their way (their number as ack): they never reached Neovim, and the person is told which.
      const refused = ev.ack && echo ? echo.noteRefused(ev.ack, ev.text) : ''
      nvimState.message = refused ? `Keys did not reach Neovim (${ev.message ?? ev.code ?? 'refused'}): ${refused}` : (ev.message ?? ev.code ?? 'error')
      nvimState.messageKind = 'emsg'
      markGuesses()
      tellNvim()
      tellDirty()
      break
    }
  }
}

/** The selection, as the bar over it needs it (F7): whole lines, from..to, and the spot below its last line. */
function tellSelection() {
  if (!editor || !model || nvimId) return
  const s = editor.getSelection()
  if (!s || s.isEmpty()) return emit('selection', null)
  let to = s.endLineNumber
  if (s.endColumn === 1 && to > s.startLineNumber) to-- // a selection ending at a line's start ends on the line before
  const at = editor.getScrolledVisiblePosition({ lineNumber: to, column: 1 })
  if (!at) return emit('selection', null)
  emit('selection', { from: s.startLineNumber, to, top: at.top + at.height, left: at.left })
}
/** The file's lines as the editor holds them. */
function getLines(): string[] {
  return model?.getLinesContent() ?? []
}
/** Shows lines from..to: in the middle, the cursor on the first, tinted for a moment (a quote opened, F7). */
function showRange(from: number, to: number) {
  if (!editor || !monacoRef) return
  const count = model?.getLineCount() ?? to
  const a = Math.max(1, Math.min(from, count))
  const b = Math.max(a, Math.min(to, count))
  editor.revealLinesInCenter(a, b)
  editor.setPosition({ lineNumber: a, column: 1 })
  marks?.clear()
  marks = editor.createDecorationsCollection([{ range: new monacoRef.Range(a, 1, b, 1), options: { isWholeLine: true, className: 'conductor-line-target' } }])
  setTimeout(() => marks?.clear(), 2500)
}
/** Goes to a line: shown in the middle, the cursor on it, tinted for a moment. */
function showLine(line: number) {
  if (!editor || !monacoRef) return
  const n = Math.max(1, Math.min(line, model?.getLineCount() ?? line))
  editor.revealLineInCenter(n)
  editor.setPosition({ lineNumber: n, column: 1 })
  marks?.clear()
  marks = editor.createDecorationsCollection([{ range: new monacoRef.Range(n, 1, n, 1), options: { isWholeLine: true, className: 'conductor-line-target' } }])
  setTimeout(() => marks?.clear(), 2500)
}

function find() {
  editor?.focus()
  editor?.trigger('conductor', 'actions.find', null)
}

function gotoLine() {
  editor?.focus()
  editor?.trigger('conductor', 'editor.action.gotoLine', null)
}

watch(theme, (t) => monacoRef?.editor.setTheme(t))
watch(() => props.line, (l) => l && showLine(l))
watch(
  () => props.text,
  (t) => {
    if (nvimId || !model) return // Neovim owns the text now
    if (isDirty(props.path, model.getAlternativeVersionId())) return // the person's unsaved edits stay
    if (model.getValue() !== t) {
      model.setValue(t)
      markSaved(props.path, model.getAlternativeVersionId())
    }
  },
)

onMounted(mount)
onBeforeUnmount(() => {
  if (nvimId && props.nvim) {
    // Changes not written keep their Neovim for the tab's next showing (the area discards it if the tab closes); none, it ends. Keys
    // Neovim has not said it has done may be making some, its word not come: they keep it too (their acknowledgement is no sign: it
    // answers in the middle of a key, a plugin's vim.wait), and so do characters typed that its text does not hold yet. What the echo
    // holds is shown first, and its changes from now on are followed into the tab's text, until it is done and clean.
    if (nvimState.modified || echo?.showing || echo?.unsettled) {
      // The echo goes with it whole (the numbers of its keys, the characters Neovim has not had): its next editor goes on with it.
      echo?.finish()
      const asLeft = echo
      const at = editor?.getPosition()
      props.nvim.holds.hold(props.path, { id: nvimId, state: { ...nvimState }, cursor: { line: at?.lineNumber ?? 1, col: at?.column ?? 1 }, echo: asLeft, stop: follow(props.nvim, nvimId, asLeft) })
    } else props.nvim.close(nvimId)
  } else if (props.nvim && nvimState.lost && lostId) {
    // Neovim lost: its text stays the tab's for the next showing (a tab switched, the area folded), until Reopen or a close.
    const at = editor?.getPosition()
    props.nvim.holds.hold(props.path, { id: lostId, state: { ...nvimState }, cursor: { line: at?.lineNumber ?? 1, col: at?.column ?? 1 }, echo: lostEcho, stop: follow(props.nvim, lostId, lostEcho) })
  }
  nvimId = null
  lostId = null
  lostEcho = null
  unsubscribe?.()
  unsubscribe = null
  stopText?.()
  stopEcho()
  contentSub?.dispose()
  contentSub = null
  editor?.dispose()
  editor = null
  // The model stays for the tab's next showing; a closed tab's model is dropped by the area.
})

watch(
  () => props.readOnly,
  (ro) => editor?.updateOptions({ readOnly: ro }),
)
/** Answers the swap file the editor found: recover keeps the banner for deleting it after the write; the others end it. */
function answerSwap(choice: NvimSwapChoice) {
  if (!nvimId || !props.nvim || !nvimState.swap) return
  props.nvim.swap(nvimId, choice)
  if (choice === 'recover') nvimState.recovered = true
  else nvimState.swap = null
  tellNvim()
  editor?.focus()
}
/**
 * Writes the file through Neovim (:w from whatever mode it is in), for the close question's Save: true once Neovim says the buffer
 * holds no changes not written; false when the write failed (its message shows) or nothing came back in ten seconds.
 */
function nvimWrite(): Promise<boolean> {
  const bridge = props.nvim
  const id = nvimId
  if (!bridge || !id) return Promise.resolve(false)
  // Nothing changed and nothing on its way: nothing to write.
  if (!nvimState.modified && !echo?.showing && !echo?.unsettled) return Promise.resolve(true)
  const file = nvimPath ?? props.path
  return new Promise((resolve) => {
    let off: () => void = () => {}
    const done = (ok: boolean) => {
      clearTimeout(timer)
      off()
      resolve(ok)
    }
    const timer = setTimeout(() => done(false), 10_000)
    // Done once Neovim has written this tab's file (not another buffer a hook wrote) and said the write's keys are done.
    let wrote = false
    let seq = 0
    let settled = false
    const check = () => {
      if (wrote && (!seq || settled)) done(true)
    }
    off = bridge.subscribe((ev) => {
      if (ev.id !== id) return
      if (ev.kind === 'written' && ev.path === file) {
        wrote = true
        check()
      } else if (ev.kind === 'settled' && seq && (ev.ack ?? 0) >= seq) {
        settled = true
        check()
      } else if ((ev.kind === 'message' && ev.messageKind === 'emsg') || ev.kind === 'closed') done(false)
    })
    // After the keys on their way, in order, so what they change is written too; <C-\\><C-n> first ends whatever waits (an operator's
    // motion, insert mode). Numbered, so its own keys are waited for too.
    const keys = '<C-\\><C-n>:w<CR>'
    const at = editor?.getPosition()
    seq = echo ? echo.send(keys, { line: at?.lineNumber ?? 1, col: at?.column ?? 1 }).seq : 0
    bridge.input(id, keys, seq || undefined)
  })
}
/** Answers Neovim's confirm question with its choice's key; the question goes. */
function answerConfirm(key: string) {
  if (!nvimId || !props.nvim || nvimState.messageKind !== 'confirm') return
  props.nvim.input(nvimId, key)
  nvimState.message = ''
  nvimState.messageKind = ''
  tellNvim()
  editor?.focus()
}
/** Lets a lost Neovim's text go (Reopen, the person's choice): this editor leaves keeping nothing, and the next starts Neovim anew. */
function releaseLost() {
  nvimState.lost = undefined
  nvimState.unconfirmed = undefined
  lostId = null
  lostEcho = null
  unsubscribe?.()
  unsubscribe = null
}
defineExpose({ showLine, showRange, find, gotoLine, focus: () => editor?.focus(), getText, version, setText, getLines, answerSwap, answerConfirm, nvimWrite, releaseLost })
</script>

<template>
  <div ref="host" class="h-full w-full min-h-0" data-code-editor />
</template>

<style>
/* A local echo's guess (round 13, G5): underlined only once it has waited 100 ms for Neovim, so a fast link shows nothing. */
.nvim-guess {
  text-decoration: underline dotted transparent;
  text-underline-offset: 3px;
  animation: nvim-guess-waits 0s linear 100ms forwards;
}
@keyframes nvim-guess-waits {
  to {
    text-decoration-color: var(--ui-text-muted);
  }
}
.conductor-line-target {
  background: color-mix(in oklab, var(--ui-primary) 22%, transparent);
}
</style>
