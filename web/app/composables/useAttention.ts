import type { SessionInfo } from './useSessions'
import type { RoutedEvent } from './useEvents'
import { attentionFavicon, needingInput, newlyNeedingInput, playChime } from '~/utils/attention'
import { EVENT_INFO, markOf } from '~/utils/events'
import type { SessionActivity } from '~/utils/protocol'

const SETTINGS_KEY = 'conductor.attention.settings'
/** Shortest gap between two chimes, so a burst of routed events plays one. */
const CHIME_GAP_MS = 1500
let lastChime = 0
let stopEvents: (() => void) | undefined

export interface AttentionSettings {
  notifications: boolean
  chime: boolean
}

function readSettings(): AttentionSettings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY)
    if (raw) return { notifications: false, chime: false, ...JSON.parse(raw) }
  } catch {
    /* ignore */
  }
  return { notifications: false, chime: false }
}

/** Persisted user preferences for attention alerts. */
export function useAttentionSettings() {
  const settings = useState<AttentionSettings>('attentionSettings', () => (import.meta.client ? readSettings() : { notifications: false, chime: false }))
  const permission = useState<NotificationPermission | 'unsupported'>('notificationPermission', () =>
    import.meta.client && 'Notification' in window ? Notification.permission : 'unsupported',
  )

  function save() {
    try {
      localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings.value))
    } catch {
      /* ignore */
    }
  }

  async function setNotifications(on: boolean) {
    if (on && permission.value !== 'granted' && permission.value !== 'unsupported') {
      permission.value = await Notification.requestPermission()
      on = permission.value === 'granted'
    }
    settings.value = { ...settings.value, notifications: on }
    save()
  }

  function setChime(on: boolean) {
    settings.value = { ...settings.value, chime: on }
    save()
    if (on) playChime()
  }

  return { settings, permission, setNotifications, setChime }
}

interface AttentionStore {
  sessions: Map<string, SessionInfo>
  connected: boolean
  started: boolean
  error: string
}

/** The live session store behind useAttention, for readers that must not start it (useEvents). */
export function useAttentionStore() {
  return useState<AttentionStore>('attentionStore', () => ({ sessions: new Map(), connected: false, started: false, error: '' }))
}

/**
 * Live session state for the whole app: one streaming fetch of
 * /api/events (admin token in the Authorization header, never in the URL)
 * with polling as a fallback. Drives badges, counters, the tab title, the
 * favicon, browser notifications and the chime, as the Events page routes
 * them, and hands every activity entry to useEvents.
 */
export function useAttention() {
  const store = useAttentionStore()
  const version = useState<number>('attentionVersion', () => 0)
  const admin = useAdminToken()
  const { httpBase } = useApiBase()
  const api = useSessions()
  const events = useEvents()
  const { settings } = useAttentionSettings()

  const sessions = computed(() => {
    void version.value
    return Array.from(store.value.sessions.values()).sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  })
  const needsInput = computed(() => needingInput(sessions.value))
  const count = computed(() => needsInput.value.length)
  const connected = computed(() => store.value.connected)
  const error = computed(() => store.value.error)

  function bump() {
    version.value++
  }

  // Each change also settles the activity entries useEvents holds for the
  // session change that carries their state.
  function replaceAll(list: SessionInfo[]) {
    const next = new Map(list.map((s) => [s.id, s]))
    react(store.value.sessions, next)
    store.value.sessions = next
    bump()
    events.settle()
  }

  function upsert(s: SessionInfo) {
    const prev = new Map(store.value.sessions)
    store.value.sessions.set(s.id, s)
    react(prev, store.value.sessions)
    bump()
    events.settle(s.id)
  }

  function remove(id: string) {
    store.value.sessions.delete(id)
    bump()
    events.forget(id)
  }

  /**
   * Alerts for sessions that newly need input, when the Events page routes
   * needs_input to Browser. They come from the session state, so each prompt
   * alerts once.
   */
  function react(prev: Map<string, SessionInfo>, next: Map<string, SessionInfo>) {
    if (!import.meta.client) return
    const fresh = newlyNeedingInput(prev, next)
    if (!fresh.length || !events.routes.value.needs_input.browser) return
    for (const s of fresh) notify(`${s.name} needs input`, s.attention?.message || 'The agent is waiting for you.', `conductor-${s.id}`, s.id)
    chime()
  }

  /** Alerts for any other event the Events page routes to Browser: artifact, tool_denied, error, exit_nonzero by default. */
  function reactToEvent(e: RoutedEvent) {
    if (e.type === 'needs_input' || !events.routes.value[e.type].browser) return
    const mark = markOf(e.type, e.entry)
    const what = e.type === 'exit_nonzero' ? mark.label : e.type.replace('_', ' ')
    notify(`${e.session?.name || e.sessionId}: ${what}`, mark.detail || EVENT_INFO[e.type].source, `conductor-${e.sessionId}-${e.type}`, e.sessionId)
    chime()
  }

  function notify(title: string, body: string, tag: string, sessionId: string) {
    if (!settings.value.notifications || !('Notification' in window) || Notification.permission !== 'granted') return
    try {
      const n = new Notification(title, { body, tag })
      n.onclick = () => {
        window.focus()
        navigateTo(`/sessions/${sessionId}`)
        n.close()
      }
    } catch {
      /* notification blocked */
    }
  }

  function chime() {
    if (!settings.value.chime || Date.now() - lastChime < CHIME_GAP_MS) return
    lastChime = Date.now()
    playChime()
  }

  let abort: AbortController | undefined
  let pollTimer: number | undefined
  let backoff = 1000

  async function stream() {
    if (!admin.token.value) return
    abort?.abort()
    abort = new AbortController()
    const signal = abort.signal
    try {
      const res = await fetch(`${httpBase.value}/api/events`, { headers: { Authorization: `Bearer ${admin.token.value}` }, signal })
      if (res.status === 401) {
        admin.needsToken.value = true
        store.value.error = 'Admin token required'
        return
      }
      if (!res.ok || !res.body) throw new Error(`events ${res.status}`)
      store.value.connected = true
      store.value.error = ''
      backoff = 1000
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let idx: number
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const block = buf.slice(0, idx)
          buf = buf.slice(idx + 2)
          handleBlock(block)
        }
      }
    } catch (e) {
      if (signal.aborted) return
      store.value.error = (e as Error).message
    } finally {
      store.value.connected = false
    }
    if (signal.aborted) return
    // Poll once while the stream is down, then retry with backoff.
    await poll()
    window.setTimeout(() => {
      if (!signal.aborted) stream()
    }, backoff)
    backoff = Math.min(backoff * 2, 15000)
  }

  function handleBlock(block: string) {
    let event = 'message'
    const data: string[] = []
    for (const line of block.split('\n')) {
      if (line.startsWith('event: ')) event = line.slice(7)
      else if (line.startsWith('data: ')) data.push(line.slice(6))
    }
    if (!data.length) return
    try {
      const payload = JSON.parse(data.join('\n'))
      if (event === 'snapshot') replaceAll(payload as SessionInfo[])
      else if (event === 'session') upsert(payload as SessionInfo)
      else if (event === 'removed') remove((payload as { id: string }).id)
      else if (event === 'activity') {
        const { sessionId, ...entry } = payload as SessionActivity
        events.push(sessionId, entry)
      }
    } catch {
      /* ignore malformed event */
    }
  }

  async function poll() {
    if (!admin.token.value) return
    try {
      replaceAll(await api.list())
      store.value.error = ''
    } catch (e) {
      store.value.error = (e as Error).message
    }
  }

  /** Starts the live connection once per app; safe to call from every page. */
  function start() {
    if (!import.meta.client || store.value.started) return
    store.value.started = true
    stopEvents?.()
    stopEvents = events.onEvent(reactToEvent)
    stream()
    pollTimer = window.setInterval(() => {
      if (!store.value.connected && document.visibilityState === 'visible') poll()
    }, 3000)
    watch(
      () => admin.token.value,
      () => {
        store.value.sessions = new Map()
        bump()
        stream()
      },
    )
  }

  function stop() {
    abort?.abort()
    stopEvents?.()
    stopEvents = undefined
    window.clearInterval(pollTimer)
    store.value.started = false
    store.value.connected = false
  }

  return { sessions, needsInput, count, connected, error, start, stop, refresh: poll }
}

/** Tab title prefix and favicon dot while sessions need input. Call once, in app.vue. */
export function useAttentionHead() {
  const { count } = useAttention()
  const faviconHref = ref('/brand/conductor-favicon.svg')
  if (import.meta.client) {
    watch(
      count,
      async (n) => {
        faviconHref.value = await attentionFavicon(n > 0)
      },
      { immediate: true },
    )
  }
  useHead({
    titleTemplate: (t) => {
      const base = t ? `${t} · Conductor` : 'Conductor'
      return count.value > 0 ? `(${count.value}) ${base}` : base
    },
    link: [{ key: 'icon', rel: 'icon', type: computed(() => (faviconHref.value.startsWith('data:') ? 'image/png' : 'image/svg+xml')), href: faviconHref }],
  })
}
