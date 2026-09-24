<script setup lang="ts">
import { Terminal, type ILink, type ILinkProvider } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'
import { findFileLocations } from '~/utils/links'
import { closeReason, encodeText, type ControlMessage, type FileResponse, type TransportKind, type Welcome } from '~/utils/protocol'
import type { CloseInfo, TerminalTransport, TransportState } from '~/utils/transport/types'

const props = defineProps<{
  /** Creates a fresh transport for each (re)connection. */
  createTransport: () => TerminalTransport
  readOnly?: boolean
  autoConnect?: boolean
}>()

const emit = defineEmits<{
  welcome: [welcome: Welcome]
  status: [status: string, exitCode?: number]
  viewers: [count: number]
  transport: [info: { kind: TransportKind; state: TransportState }]
  closed: [info: CloseInfo]
  openFile: [loc: { path: string; line?: number }]
  openUrl: [url: string]
}>()

const host = ref<HTMLDivElement>()
const overlay = ref<{ title: string; detail?: string } | null>(null)
const connecting = ref(false)
const fileView = ref(false)
const notice = ref('')

let term: Terminal | undefined
let fit: FitAddon | undefined
let transport: TerminalTransport | undefined
let observer: ResizeObserver | undefined
let resizeTimer: number | undefined
let pingTimer: number | undefined
let stopWatch: (() => void) | undefined
const existsCache = new Map<string, Promise<boolean>>()

// Brand ink/sage canvas with the terracotta accent as cursor; ANSI colors stay conventional.
const theme = {
  background: '#18211c',
  foreground: '#eef1e9',
  cursor: '#d26b3f',
  cursorAccent: '#18211c',
  selectionBackground: '#f3ac8955',
  black: '#18211c',
  brightBlack: '#526454',
  red: '#f28b82',
  green: '#8fd3a5',
  yellow: '#f6c76b',
  blue: '#8ab4f8',
  magenta: '#d7aefb',
  cyan: '#78d9ec',
  white: '#eef1e9',
  brightWhite: '#ffffff',
}

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

function measure(): { cols: number; rows: number } {
  if (!props.readOnly) fit?.fit()
  return { cols: term?.cols ?? 80, rows: term?.rows ?? 24 }
}

function scheduleResize() {
  if (props.readOnly) return
  window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(() => {
    const { cols, rows } = measure()
    transport?.resize(cols, rows)
  }, 100)
}

function handleControl(msg: ControlMessage) {
  switch (msg.t) {
    case 'welcome':
      fileView.value = msg.fileView
      existsCache.clear()
      if (props.readOnly && msg.cols && msg.rows) term?.resize(msg.cols, msg.rows)
      emit('welcome', msg)
      break
    case 'resize':
      if (msg.cols && msg.rows && (term?.cols !== msg.cols || term?.rows !== msg.rows)) term?.resize(msg.cols, msg.rows)
      break
    case 'status':
      emit('status', msg.status, msg.exitCode)
      if (msg.status === 'exited' || msg.status === 'stopped') {
        notice.value = msg.status === 'exited' ? `Process exited${msg.exitCode !== undefined ? ` with code ${msg.exitCode}` : ''}` : 'Session stopped'
      }
      break
    case 'viewers':
      emit('viewers', msg.count)
      break
    case 'error':
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
  stopWatch = watch([t.kind, t.state], ([kind, state]) => emit('transport', { kind, state }), { immediate: true })
  t.onOutput((data) => term?.write(data))
  t.onControl(handleControl)
  t.onClose((info) => {
    if (transport !== t) return
    connecting.value = false
    overlay.value = { title: info.error?.message || closeReason(info.code, info.reason), detail: info.error ? info.error.code : undefined }
    emit('closed', info)
  })
  term.reset()
  try {
    await t.connect(measure())
    if (!props.readOnly) t.resize(term.cols, term.rows)
    term.focus()
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

defineExpose({ connect, disconnect, requestFile, focus: () => term?.focus() })

onMounted(() => {
  term = new Terminal({
    cursorBlink: true,
    scrollback: 5000,
    allowProposedApi: true,
    disableStdin: !!props.readOnly,
    fontSize: 13,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    theme,
    convertEol: false,
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
  term.open(host.value!)
  try {
    const webgl = new WebglAddon()
    webgl.onContextLoss(() => webgl.dispose())
    term.loadAddon(webgl)
  } catch {
    /* canvas renderer fallback */
  }
  term.onData((data) => transport?.sendInput(encodeText(data)))
  term.onBinary((data) => {
    const bytes = new Uint8Array(data.length)
    for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 0xff
    transport?.sendInput(bytes)
  })
  if (!props.readOnly) fit.fit()
  observer = new ResizeObserver(() => scheduleResize())
  observer.observe(host.value!)
  pingTimer = window.setInterval(() => transport?.ping(), 25000)
  if (props.autoConnect !== false) connect()
})

onBeforeUnmount(() => {
  observer?.disconnect()
  window.clearTimeout(resizeTimer)
  window.clearInterval(pingTimer)
  disconnect(false)
  term?.dispose()
  term = undefined
})
</script>

<template>
  <div class="relative h-full w-full overflow-hidden rounded-lg border border-default">
    <div ref="host" class="terminal-host" :aria-label="readOnly ? 'terminal (read-only)' : 'terminal'" role="region" />

    <div v-if="notice" class="absolute top-2 right-2 z-10">
      <UBadge :label="notice" color="warning" variant="solid" size="sm" class="cursor-pointer" @click="notice = ''" />
    </div>

    <div v-if="overlay || connecting" class="absolute inset-0 z-20 flex items-center justify-center bg-black/60 backdrop-blur-[1px]">
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
