import type { SessionInfo } from './useSessions'
import { attentionFavicon, needingInput, newlyNeedingInput, playChime } from '~/utils/attention'

const SETTINGS_KEY = 'conductor.attention.settings'

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

/**
 * Live session state for the whole app: one streaming fetch of
 * /api/events (admin token in the Authorization header, never in the URL)
 * with polling as a fallback. Drives badges, counters, the tab title, the
 * favicon, browser notifications and the chime.
 */
export function useAttention() {
  const store = useState<AttentionStore>('attentionStore', () => ({ sessions: new Map(), connected: false, started: false, error: '' }))
  const version = useState<number>('attentionVersion', () => 0)
  const admin = useAdminToken()
  const { httpBase } = useApiBase()
  const api = useSessions()
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

  function replaceAll(list: SessionInfo[]) {
    const next = new Map(list.map((s) => [s.id, s]))
    react(store.value.sessions, next)
    store.value.sessions = next
    bump()
  }

  function upsert(s: SessionInfo) {
    const prev = new Map(store.value.sessions)
    store.value.sessions.set(s.id, s)
    react(prev, store.value.sessions)
    bump()
  }

  function remove(id: string) {
    store.value.sessions.delete(id)
    bump()
  }

  function react(prev: Map<string, SessionInfo>, next: Map<string, SessionInfo>) {
    if (!import.meta.client) return
    const fresh = newlyNeedingInput(prev, next)
    if (!fresh.length) return
    if (settings.value.notifications && 'Notification' in window && Notification.permission === 'granted') {
      for (const s of fresh) {
        try {
          const n = new Notification(`${s.name} needs input`, { body: s.attention?.message || 'The agent is waiting for you.', tag: `conductor-${s.id}` })
          n.onclick = () => {
            window.focus()
            navigateTo(`/sessions/${s.id}`)
            n.close()
          }
        } catch {
          /* notification blocked */
        }
      }
    }
    if (settings.value.chime) playChime()
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
