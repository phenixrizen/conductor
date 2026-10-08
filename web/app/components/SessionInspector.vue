<script setup lang="ts">
import type { ChatThread } from '~/composables/useChat'
import type { SessionInfo, ShareLink } from '~/composables/useSessions'
import type { ActivityEntry, ChatMessage, FileRequester, Role, ViewerInfo } from '~/utils/protocol'
import type { FileTarget } from '~/components/FileBrowser.vue'
import type { ChangeRow } from '~/utils/changes'
import { avatarTone } from '~/utils/avatar'
import { initials, relativeTime } from '~/utils/sessions'
import { COLOR_TEXT, entryIcon, fileOpWords, linkableUrl } from '~/utils/events'

export type InspectorTab = 'people' | 'files' | 'activity' | 'chat'

const props = defineProps<{
  session: SessionInfo
  role: Role
  viewers: ViewerInfo[]
  activity: ActivityEntry[]
  links: ShareLink[]
  request: FileRequester
  rawUrl?: (path: string) => string | null
  /** The session's chat (design 2a); the tab shows once the owner said it takes chat. */
  chat?: ChatThread
  chatUnread?: number
  chatOffline?: boolean
  ended?: boolean
}>()
const emit = defineEmits<{ newLink: []; revoke: [link: ShareLink]; chatSend: [text: string, to: string]; chatSendToAgent: [ref: string]; chatRetry: [nonce: string]; chatAnswer: [m: ChatMessage, index: number]; openFile: [target: FileTarget]; openDiff: [change: ChangeRow, against: { top: string; branch?: string; base?: string; baseId?: string }] }>()

const tab = defineModel<InspectorTab>('tab', { default: 'people' })
const target = defineModel<FileTarget | null>('target', { default: null })
const url = defineModel<string | null>('url', { default: null })

const tabs = computed<Array<{ id: InspectorTab; label: string }>>(() => [
  { id: 'people', label: 'People' },
  { id: 'files', label: 'Files' },
  { id: 'activity', label: 'Activity' },
  ...(props.chat?.capable ? [{ id: 'chat' as const, label: 'Chat' }] : []),
])

const now = ref(Date.now())
let tick: number | undefined
onMounted(() => (tick = window.setInterval(() => (now.value = Date.now()), 1000)))
onBeforeUnmount(() => window.clearInterval(tick))

function typing(v: ViewerInfo) {
  return !!v.lastInputAt && now.value - Date.parse(v.lastInputAt) < 4000
}

function who(v: ViewerInfo) {
  const parts: string[] = []
  if (v.link) parts.push(`via “${v.link}”`)
  else parts.push(v.role === 'control' ? 'Owner' : 'Viewer')
  parts.push(typing(v) ? 'typing' : relativeTime(v.since, now.value))
  return parts.join(' · ')
}

const liveLinks = computed(() => props.links.filter((l) => !l.revoked))

function expiry(l: ShareLink) {
  if (!l.expiresAt) return 'never expires'
  const ms = Date.parse(l.expiresAt) - now.value
  if (ms <= 0) return 'expired'
  const min = Math.floor(ms / 60000)
  if (min < 60) return `expires in ${min}m`
  const h = Math.floor(min / 60)
  if (h < 24) return `expires in ${h}h ${min % 60}m`
  return `expires in ${Math.floor(h / 24)}d`
}


// Newest first. Text only: nothing an agent sends is rendered as HTML, and a
// URL is a link only when linkableUrl allows it.
const activityRows = computed(() =>
  [...props.activity].reverse().map((e) => {
    const icon = entryIcon(e)
    return { e, text: describe(e), icon: icon.icon, color: COLOR_TEXT[icon.color], link: linkableUrl(e.url) }
  }),
)
function describe(e: ActivityEntry) {
  const by = e.byName ? `${e.byName} ` : ''
  const msg = e.message ? `: ${e.message}` : ''
  switch (e.type) {
    case 'input':
      return `${by}answered${msg}`
    case 'join':
      return `${by}joined`
    case 'leave':
      return `${by}left`
    case 'attention':
      return e.message || 'attention changed'
    case 'link':
      return e.message || 'link changed'
    case 'status':
      return e.message || 'status changed'
    case 'progress':
      return e.message || 'progress'
    case 'artifact':
      return e.message || (e.url ? '' : 'artifact')
    case 'handoff':
      return `handed off${e.to ? ` to ${e.to}` : ''}${msg}`
    case 'tool_use':
      return `used ${e.tool || 'a tool'}${msg}`
    case 'tool_denied':
      return `${e.tool || 'a tool'} denied${msg}`
    case 'error':
      return `${e.tool ? `${e.tool}: ` : ''}${e.message || 'error'}`
    case 'file':
      return `${fileOpWords(e.op)} ${e.path ?? ''}${e.tool ? ` · ${e.tool}` : ''}`
  }
  return e.message || e.type
}
</script>

<template>
  <aside class="flex h-full min-h-0 w-full flex-col border-l border-default bg-default" data-inspector>
    <div class="flex gap-1 border-b border-default px-3 pt-2 text-sm">
      <button
        v-for="t in tabs"
        :key="t.id"
        type="button"
        class="px-2.5 py-2 -mb-px border-b-2 transition-colors"
        :class="tab === t.id ? 'border-primary font-semibold text-highlighted' : 'border-transparent text-muted hover:text-default'"
        :data-chat-tab="t.id === 'chat' ? '' : undefined"
        @click="tab = t.id"
      >
        <span class="inline-flex items-center gap-1.5">{{ t.label }}<ChatUnreadPill v-if="t.id === 'chat' && tab !== 'chat'" :count="chatUnread ?? 0" /></span>
      </button>
    </div>

    <ChatPanel v-if="tab === 'chat' && chat" :thread="chat" :role="role" :ended="ended" :offline="chatOffline" :viewers="viewers" @send="(text, to) => emit('chatSend', text, to)" @send-to-agent="emit('chatSendToAgent', $event)" @retry="emit('chatRetry', $event)" @answer="(m, i) => emit('chatAnswer', m, i)" />

    <div v-else-if="tab === 'people'" class="flex-1 min-h-0 overflow-y-auto p-4 flex flex-col gap-6">
      <section class="flex flex-col gap-2.5">
        <h3 class="text-[11px] font-semibold uppercase tracking-wider text-muted">Here now · {{ viewers.length || session.viewers }}</h3>
        <p v-if="!viewers.length" class="text-xs text-muted">Names arrive once everyone reconnects.</p>
        <div v-for="v in viewers" :key="v.id" class="flex items-center gap-2.5">
          <span class="grid size-7 place-items-center rounded-full text-[11px] font-semibold flex-none" :class="[avatarTone(v.name), typing(v) && 'ring-2 ring-warning ring-offset-2 ring-offset-default']">{{ initials(v.name) }}</span>
          <div class="min-w-0 flex-1 flex flex-col">
            <span class="truncate text-sm font-medium">{{ v.name }}</span>
            <span class="truncate text-xs text-muted">{{ who(v) }}</span>
          </div>
          <UBadge :label="v.role === 'control' ? 'Control' : 'View'" :color="v.role === 'control' ? 'primary' : 'neutral'" :variant="v.role === 'control' ? 'subtle' : 'outline'" size="sm" />
        </div>
      </section>

      <section class="flex flex-col gap-2.5">
        <h3 class="flex items-center text-[11px] font-semibold uppercase tracking-wider text-muted">
          Links
          <UButton label="New link" icon="i-lucide-plus" size="xs" variant="link" color="secondary" class="ml-auto normal-case tracking-normal" @click="emit('newLink')" />
        </h3>
        <p v-if="!liveLinks.length" class="text-xs text-muted">No share links yet.</p>
        <div v-for="l in liveLinks" :key="l.id" class="flex flex-col gap-1 rounded-md border border-default px-3 py-2.5">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium truncate">{{ l.label || 'unlabelled' }}</span>
            <UBadge :label="l.role === 'control' ? 'Control' : 'View'" :color="l.role === 'control' ? 'primary' : 'neutral'" :variant="l.role === 'control' ? 'subtle' : 'outline'" size="sm" />
            <UButton label="Revoke" size="xs" variant="link" color="secondary" class="ml-auto" @click="emit('revoke', l)" />
          </div>
          <span class="font-mono text-[11px] text-muted">{{ expiry(l) }}<template v-if="l.active !== undefined"> · {{ l.active }} using</template></span>
        </div>
      </section>
    </div>

    <div v-else-if="tab === 'files'" class="flex-1 min-h-0 flex flex-col">
      <FileBrowser v-model:target="target" v-model:url="url" :request="request" :cwd="session.cwd" :raw-url="rawUrl" :activity="activity" external class="flex-1 min-h-0" @open="emit('openFile', $event)" @open-diff="(c, a) => emit('openDiff', c, a)" />
    </div>

    <div v-else class="flex-1 min-h-0 overflow-y-auto p-4">
      <p v-if="!activityRows.length" class="text-xs text-muted">Nothing yet.</p>
      <ol v-else class="flex flex-col gap-2 text-sm">
        <li v-for="(r, i) in activityRows" :key="i" class="flex gap-2.5" :data-activity="r.e.type">
          <span class="font-mono text-[11px] text-muted pt-0.5 flex-none">{{ new Date(r.e.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</span>
          <UIcon :name="r.icon" class="mt-0.5 size-4 flex-none" :class="r.color" aria-hidden="true" />
          <span class="min-w-0 break-words">
            {{ r.text }}
            <template v-if="r.e.url">
              <a v-if="r.link" :href="r.link" target="_blank" rel="noopener noreferrer" class="break-all text-primary underline underline-offset-2">{{ r.e.url }}</a>
              <span v-else class="break-all font-mono text-xs text-muted">{{ r.e.url }}</span>
            </template>
          </span>
        </li>
      </ol>
    </div>
  </aside>
</template>
