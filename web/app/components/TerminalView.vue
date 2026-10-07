<script setup lang="ts">
import { Terminal, type ILink, type ILinkProvider } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'
import { findFileLocations } from '~/utils/links'
import { ALT_PASSTHROUGH_CODES } from '~/composables/useShortcuts'
import { NEWLINE_IN_PROMPT, newlineChord } from '~/utils/terminalKeys'
import { closeReason, encodeText, FOLLOW_SIZE, type ActivityEntry, type ChatHistory, type ChatMessage, type ChatPost, type ChatRoster, type ChatSend, type ControlMessage, type FileResponse, type Role, type TransportKind, type ViewerInfo, type Welcome } from '~/utils/protocol'
import type { CloseInfo, TerminalTransport, TransportState } from '~/utils/transport/types'
import { FIT_DEBOUNCE_MS, helloSize, tileScale } from '~/utils/tile'

const props = withDefaults(
  defineProps<{
    /** Creates a fresh transport for each (re)connection. */
    createTransport: () => TerminalTransport
    /** No input: keys and pastes reach nothing. How the terminal is sized is the `fit` prop's alone. */
    readOnly?: boolean
    /** Connect on mount. Vue casts absent booleans to false, hence the explicit default. */
    autoConnect?: boolean
    fontSize?: number
    scrollback?: number
    /** Tile mode: no frame, overlays or notices, just the terminal. */
    compact?: boolean
    /**
     * How the terminal meets its pane. `fill` (default, the full view) fits
     * the pane and, with control, sizes the session to it. `tile` does the
     * same for a wall or crew tile, at the font size given and with no
     * scrollback (so no scrollbar gutter is reserved), and scales the screen
     * down to show it whole while another viewer has made the session larger
     * than the tile. `scale` keeps the session's cols × rows and scales the
     * whole terminal into the pane: a view-only tile; its hello asks for no
     * size (0 × 0, follow the session).
     */
    fit?: 'fill' | 'scale' | 'tile'
    /** Focus the terminal once connected (controllers only). */
    autoFocus?: boolean
  }>(),
  { readOnly: false, autoConnect: true, fontSize: 13, scrollback: 5000, compact: false, fit: 'fill', autoFocus: true },
)

const emit = defineEmits<{
  welcome: [welcome: Welcome]
  status: [status: string, exitCode?: number]
  attention: [msg: { state: string; message?: string; source?: string; kind?: string; options?: Array<{ label: string; input: string }> }]
  viewers: [info: { count: number; list?: ViewerInfo[] }]
  activity: [entry: ActivityEntry]
  transport: [info: { kind: TransportKind; state: TransportState; rtt: number | null }]
  closed: [info: CloseInfo]
  openFile: [loc: { path: string; line?: number }]
  openUrl: [url: string]
  /** A chat message, live; the kept chat on each welcome; an error the owner sent about one of this client's posts or sends. */
  chat: [msg: ChatMessage]
  chatHistory: [history: ChatHistory]
  chatRoster: [roster: ChatRoster]
  requestError: [err: { code: string; message: string; requestId: string }]
}>()

const host = ref<HTMLDivElement>()
/** The renderer xterm draws with: webgl, or dom when WebGL is not available. */
const renderer = ref<'webgl' | 'dom' | ''>('')
const overlay = ref<{ title: string; detail?: string } | null>(null)
const connecting = ref(false)
const fileView = ref(false)
const notice = ref('')
/** The role the server gave this connection: a view-only one never sizes the session, whatever the page asked for. */
const role = ref<Role | ''>('')
/** The size the last hello carried: 0 × 0 when this view did not size the session as it connected. */
let helloSent: { cols: number; rows: number } = { ...FOLLOW_SIZE }
/** True once the process is gone: the cursor is hidden and stops blinking. */
const ended = ref(false)
let endedAtWelcome = false

let term: Terminal | undefined
let fit: FitAddon | undefined
let transport: TerminalTransport | undefined
let observer: ResizeObserver | undefined
let resizeTimer: number | undefined
let pingTimer: number | undefined
let stopWatch: (() => void) | undefined
let stopTheme: (() => void) | undefined
const existsCache = new Map<string, Promise<boolean>>()

// Colours follow the workbench palette in both colour modes (see useTerminalTheme).
const theme = useTerminalTheme()

function pathExists(path: string): Promise<boolean> {
  if (!transport || transport.state.value !== 'open' || !fileView.value) return Promise.resolve(false)
  let p = existsCache.get(path)
  if (!p) {
    p = transport
      .requestFile(path, true)
      .then((r) => r.header.exists)
      .catch(() => false)
    existsCache.set(path, p)
    if (existsCache.size > 2000) existsCache.clear()
  }
  return p
}

/** Underlines file locations that the session owner confirms exist. */
const fileLinkProvider: ILinkProvider = {
  provideLinks(y, callback) {
    const line = term?.buffer.active.getLine(y - 1)
    if (!line) return callback(undefined)
    const text = line.translateToString(true)
    const locs = findFileLocations(text)
    if (!locs.length) return callback(undefined)
    Promise.all(locs.map((l) => pathExists(l.path).then((ok) => (ok ? l : null)))).then((checked) => {
      const links: ILink[] = []
      for (const loc of checked) {
        if (!loc) continue
        links.push({
          range: { start: { x: loc.start + 1, y }, end: { x: loc.end, y } },
          text: text.slice(loc.start, loc.end),
          decorations: { underline: true, pointerCursor: true },
          activate: () => emit('openFile', { path: loc.path, line: loc.line }),
        })
      }
      callback(links.length ? links : undefined)
    })
  },
}

/**
 * Whether this view sizes the session: a full view or a tile that takes input, unless the server said this connection may only watch. A
 * scaled or read-only view follows the session's size instead.
 */
function sizes(): boolean {
  return props.fit !== 'scale' && !props.readOnly && role.value !== 'view'
}

/** Fits the terminal to its pane when this view sizes the session, and returns the size to ask for: 0 × 0 (follow) otherwise, or before the pane is laid out. */
function measure(): { cols: number; rows: number } {
  const h = host.value
  if (!sizes() || !term || !fit || !h?.clientWidth || !h.clientHeight) return helloSize(false, 0, 0)
  fit.fit()
  return helloSize(true, term.cols, term.rows)
}

/**
 * Fits and resizes the session once this view's own pane has stopped changing size (FIT_DEBOUNCE_MS), as it becomes visible again, or once
 * the font has loaded. Never in answer to another viewer's resize: two views of one session take turns only when one of them attaches or
 * its pane changes (latest controller wins), never back and forth.
 */
function scheduleResize() {
  if (!sizes()) return
  window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(() => fitAndResize(2), FIT_DEBOUNCE_MS)
}

/**
 * Fits, resizes the session, and checks a frame later that the screen fits its pane: xterm may measure its cells again as it renders the
 * new size (the bundled font settling in), and then the fit is done again, `retries` times at most.
 */
function fitAndResize(retries: number) {
  const { cols, rows } = measure()
  if (cols) transport?.resize(cols, rows)
  window.requestAnimationFrame(() => {
    if (retries > 0 && cols && overflows()) fitAndResize(retries - 1)
    else scheduleTileScale()
  })
}

/** Whether the terminal's screen, with its padding, is wider or taller than its pane. */
function overflows(): boolean {
  const el = term?.element
  const h = host.value
  const screen = el?.querySelector<HTMLElement>('.xterm-screen')
  if (!el || !h || !screen) return false
  const style = getComputedStyle(el)
  const padX = Number.parseFloat(style.paddingLeft) + Number.parseFloat(style.paddingRight)
  const padY = Number.parseFloat(style.paddingTop) + Number.parseFloat(style.paddingBottom)
  return screen.offsetWidth + padX > h.clientWidth || screen.offsetHeight + padY > h.clientHeight
}

function onVisibility() {
  if (document.visibilityState === 'visible') scheduleResize()
}

// Tile mode's safety net: while another viewer has made the session larger
// than the tile, the screen is scaled down to show it whole; at the tile's own
// size it is not scaled at all.
let tileFrame: number | undefined
function applyTileScale() {
  const el = term?.element
  const h = host.value
  if (props.fit !== 'tile' || !el || !h) return
  const screen = el.querySelector<HTMLElement>('.xterm-screen')
  if (!screen) return
  const style = getComputedStyle(el)
  const padX = Number.parseFloat(style.paddingLeft) + Number.parseFloat(style.paddingRight)
  const padY = Number.parseFloat(style.paddingTop) + Number.parseFloat(style.paddingBottom)
  const s = tileScale({ width: h.clientWidth, height: h.clientHeight }, { width: screen.offsetWidth + padX, height: screen.offsetHeight + padY })
  el.style.transform = s < 1 ? `scale(${s})` : ''
}

function scheduleTileScale() {
  if (props.fit !== 'tile' || tileFrame !== undefined) return
  tileFrame = window.requestAnimationFrame(() => {
    tileFrame = undefined
    applyTileScale()
  })
}

// Scale mode: pick a font size that roughly fills the pane, then apply the
// exact remaining factor as a CSS transform so the whole screen stays visible.
const MIN_FONT = 5
const MAX_FONT = 28
let scaleFrame: number | undefined
let fontAdjustments = 0

function applyScale() {
  const el = term?.element
  const h = host.value
  if (!term || !el || !h || props.fit !== 'scale') return
  const natW = el.offsetWidth
  const natH = el.offsetHeight
  const hw = h.clientWidth
  const hh = h.clientHeight
  if (!natW || !natH || !hw || !hh) return
  const k = Math.min(hw / natW, hh / natH)
  const font = term.options.fontSize ?? props.fontSize
  const want = Math.max(MIN_FONT, Math.min(MAX_FONT, Math.round(font * k)))
  if (want !== font && fontAdjustments < 6) {
    fontAdjustments++
    term.options.fontSize = want // the element resizes; the observer calls applyScale again
    return
  }
  const s = Math.min(k, 1)
  const tx = Math.round((hw - natW * s) / 2)
  const ty = Math.round((hh - natH * s) / 2)
  el.style.transform = `translate(${tx}px, ${ty}px) scale(${s})`
}

function scheduleScale() {
  if (scaleFrame !== undefined) return
  scaleFrame = window.requestAnimationFrame(() => {
    scaleFrame = undefined
    applyScale()
  })
}

function isEnded(status: string) {
  return status === 'exited' || status === 'stopped'
}

/**
 * A finished process has no cursor. The DECTCEM hide sequence goes through
 * the terminal's own write queue, so it lands after any replayed output.
 */
function markEnded() {
  ended.value = true
  if (!term) return
  term.options.cursorBlink = false
  term.write('\x1b[?25l')
}

function handleControl(msg: ControlMessage) {
  switch (msg.t) {
    case 'welcome':
      role.value = msg.role
      fileView.value = msg.fileView
      existsCache.clear()
      // Sessions that already ended are marked once the scrollback has replayed.
      endedAtWelcome = isEnded(msg.status)
      // A view that does not size the session shows it at its size, and so
      // does one whose pane was not laid out as it connected, until it fits.
      if (msg.cols && msg.rows && (!sizes() || !helloSent.cols)) term?.resize(msg.cols, msg.rows)
      scheduleTileScale()
      emit('welcome', msg)
      break
    case 'ready':
      if (endedAtWelcome) markEnded()
      break
    case 'resize':
      // Another viewer's resize is followed, never answered with one of this view's own.
      if (msg.cols && msg.rows && (term?.cols !== msg.cols || term?.rows !== msg.rows)) term?.resize(msg.cols, msg.rows)
      scheduleTileScale()
      break
    case 'status':
      emit('status', msg.status, msg.exitCode)
      if (isEnded(msg.status)) {
        notice.value = msg.status === 'exited' ? `Process exited${msg.exitCode !== undefined ? ` with code ${msg.exitCode}` : ''}` : 'Session stopped'
        markEnded()
      }
      break
    case 'attention':
      emit('attention', { state: msg.state, message: msg.message, source: msg.source, kind: msg.kind, options: msg.options })
      break
    case 'viewers':
      emit('viewers', { count: msg.count, list: msg.list })
      break
    case 'activity':
      emit('activity', { at: msg.at, type: msg.type, by: msg.by, byName: msg.byName, message: msg.message, url: msg.url, to: msg.to, tool: msg.tool })
      break
    case 'chat':
      emit('chat', msg)
      break
    case 'chat_history':
      emit('chatHistory', msg)
      break
    case 'chat_roster':
      emit('chatRoster', msg)
      break
    case 'error':
      // An error naming a request (a chat post or send) is that request's, not the terminal's.
      if (msg.requestId) {
        emit('requestError', { code: msg.code, message: msg.message, requestId: msg.requestId })
        break
      }
      if (msg.code === 'read_only') notice.value = 'This link is view-only'
      else if (msg.code !== 'bad_frame') notice.value = msg.message
      break
  }
}

async function connect() {
  if (!term || connecting.value) return
  disconnect(false)
  overlay.value = null
  notice.value = ''
  connecting.value = true
  const t = props.createTransport()
  transport = t
  stopWatch = watch([t.kind, t.state, t.rtt], ([kind, state, rtt]) => emit('transport', { kind, state, rtt }), { immediate: true })
  t.onOutput((data) => term?.write(data))
  t.onControl(handleControl)
  t.onClose((info) => {
    if (transport !== t) return
    connecting.value = false
    overlay.value = { title: info.error?.message || closeReason(info.code, info.reason), detail: info.error ? info.error.code : undefined }
    emit('closed', info)
  })
  // A fresh connection starts live again; reset() also re-shows the cursor.
  ended.value = false
  endedAtWelcome = false
  term.options.cursorBlink = !props.compact
  term.reset()
  try {
    role.value = ''
    helloSent = measure()
    await t.connect(helloSent)
    t.ping()
    if (sizes()) {
      const { cols, rows } = measure()
      if (cols) t.resize(cols, rows)
    }
    if (props.autoFocus && !props.readOnly) term.focus()
  } catch (e) {
    if (!overlay.value) overlay.value = { title: (e as Error).message }
  } finally {
    connecting.value = false
  }
}

function disconnect(showOverlay = true) {
  stopWatch?.()
  stopWatch = undefined
  const t = transport
  transport = undefined
  t?.close()
  if (showOverlay && !overlay.value) overlay.value = { title: 'Disconnected' }
}

function requestFile(path: string, stat = false): Promise<FileResponse> {
  if (!transport) return Promise.reject(new Error('not connected'))
  return transport.requestFile(path, stat)
}

/** Sends text to the PTY as if typed; false when the transport is not open. */
function sendInput(text: string): boolean {
  if (!transport || transport.state.value !== 'open') return false
  transport.sendInput(encodeText(text))
  return true
}

/** Submits a line through the session's owner (a paste, then Enter); false when the transport is not open. */
function submit(text: string): boolean {
  if (!transport || transport.state.value !== 'open') return false
  transport.submit(text)
  return true
}

/** Posts to the chat; false when the transport is not open (the caller queues it). */
function chat(post: ChatPost): boolean {
  if (!transport || transport.state.value !== 'open') return false
  transport.chat(post)
  return true
}

/** Types a kept chat message into the agent; false when the transport is not open. */
function chatSend(send: ChatSend): boolean {
  if (!transport || transport.state.value !== 'open') return false
  transport.chatSend(send)
  return true
}

defineExpose({ connect, disconnect, requestFile, sendInput, submit, chat, chatSend, focus: () => term?.focus(), scrollToBottom: () => term?.scrollToBottom() })

onMounted(() => {
  term = new Terminal({
    cursorBlink: !props.compact,
    scrollback: props.scrollback,
    allowProposedApi: true,
    disableStdin: !!props.readOnly,
    fontSize: props.fontSize,
    fontFamily: '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    theme: theme.value,
    convertEol: false,
  })
  stopTheme = watch(theme, (t) => {
    if (term) term.options.theme = t
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.loadAddon(
    new WebLinksAddon((event, uri) => {
      if (event.ctrlKey || event.metaKey) window.open(uri, '_blank', 'noopener,noreferrer')
      else emit('openUrl', uri)
    }),
  )
  term.registerLinkProvider(fileLinkProvider)
  // Shift+Enter and Ctrl+Enter are a newline in the agent's prompt, not a
  // submit: the session gets ESC CR instead of xterm's plain CR. Alt+<page
  // shortcut> is for the page, not the agent: xterm skips it and the keydown
  // bubbles to the shortcut handlers. Everything else reaches the PTY.
  term.attachCustomKeyEventHandler((e) => {
    if (newlineChord(e)) {
      if (e.type === 'keydown') sendInput(NEWLINE_IN_PROMPT)
      return false
    }
    return !(e.type === 'keydown' && e.altKey && !e.ctrlKey && !e.metaKey && ALT_PASSTHROUGH_CODES.has(e.code))
  })
  term.open(host.value!)
  // Re-measure once the bundled font has loaded so cell metrics are exact.
  document.fonts?.ready.then(() => {
    if (!term) return
    if (props.fit === 'scale') scheduleScale()
    else scheduleResize()
  })
  // The renderer in use, for anyone measuring the tiles (data-renderer on
  // the host): webgl once the addon is up, dom when it is not available or
  // its context is lost.
  renderer.value = 'dom'
  try {
    const webgl = new WebglAddon()
    webgl.onContextLoss(() => {
      webgl.dispose()
      renderer.value = 'dom'
    })
    term.loadAddon(webgl)
    renderer.value = 'webgl'
  } catch {
    /* canvas renderer fallback */
  }
  term.onData((data) => transport?.sendInput(encodeText(data)))
  term.onBinary((data) => {
    const bytes = new Uint8Array(data.length)
    for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 0xff
    transport?.sendInput(bytes)
  })
  if (sizes() && host.value?.clientWidth && host.value.clientHeight) fit.fit()
  observer = new ResizeObserver((entries) => {
    if (props.fit === 'scale') {
      if (entries.some((e) => e.target === host.value)) fontAdjustments = 0
      scheduleScale()
    } else {
      scheduleResize()
    }
  })
  observer.observe(host.value!)
  if (props.fit === 'scale' && term.element) observer.observe(term.element)
  // A tab brought back sizes its session again: the view the person looks at wins.
  document.addEventListener('visibilitychange', onVisibility)
  pingTimer = window.setInterval(() => transport?.ping(), 10000)
  if (props.autoConnect) connect()
})

onBeforeUnmount(() => {
  observer?.disconnect()
  document.removeEventListener('visibilitychange', onVisibility)
  stopTheme?.()
  if (scaleFrame !== undefined) window.cancelAnimationFrame(scaleFrame)
  if (tileFrame !== undefined) window.cancelAnimationFrame(tileFrame)
  window.clearTimeout(resizeTimer)
  window.clearInterval(pingTimer)
  disconnect(false)
  term?.dispose()
  term = undefined
})
</script>

<template>
  <div class="relative h-full w-full overflow-hidden" :class="compact ? '' : 'rounded-lg border border-default'">
    <div ref="host" class="terminal-host" :class="{ 'terminal-compact': compact, 'terminal-scale': props.fit === 'scale', 'terminal-tile': props.fit === 'tile' }" :data-ended="ended ? 'true' : undefined" :data-renderer="renderer" :aria-label="readOnly ? 'terminal (read-only)' : 'terminal'" role="region" />

    <div v-if="notice && !compact" class="absolute top-2 right-2 z-10">
      <UBadge :label="notice" color="warning" variant="solid" size="sm" class="cursor-pointer" @click="notice = ''" />
    </div>

    <div v-if="compact && overlay" class="absolute inset-0 z-20 flex items-center justify-center bg-black/50 text-[10px] text-white/80">{{ overlay.title }}</div>
    <div v-else-if="overlay || connecting" class="absolute inset-0 z-20 flex items-center justify-center bg-black/60 backdrop-blur-[1px]">
      <div class="flex flex-col items-center gap-3 rounded-lg bg-default/95 px-6 py-5 text-center shadow-lg border border-default max-w-sm">
        <template v-if="connecting && !overlay">
          <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-primary" />
          <p class="text-sm">Connecting…</p>
        </template>
        <template v-else-if="overlay">
          <UIcon name="i-lucide-plug-zap" class="size-6 text-warning" />
          <p class="text-sm font-medium">{{ overlay.title }}</p>
          <p v-if="overlay.detail" class="text-xs text-muted font-mono">{{ overlay.detail }}</p>
          <UButton label="Reconnect" icon="i-lucide-refresh-cw" size="sm" @click="connect" />
        </template>
      </div>
    </div>
  </div>
</template>
