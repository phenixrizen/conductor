<script setup lang="ts">
import type { SessionInfo, ShareLink } from '~/composables/useSessions'
import type { ActivityEntry, FileResponse, Role, ViewerInfo } from '~/utils/protocol'
import type { FileTarget } from '~/components/FileBrowser.vue'
import { initials, relativeTime } from '~/utils/sessions'
import { parseLocation } from '~/utils/links'

export type InspectorTab = 'people' | 'files' | 'activity'

const props = defineProps<{
  session: SessionInfo
  role: Role
  viewers: ViewerInfo[]
  activity: ActivityEntry[]
  links: ShareLink[]
  request: (path: string, stat?: boolean) => Promise<FileResponse>
  rawUrl?: (path: string) => string | null
}>()
const emit = defineEmits<{ newLink: []; revoke: [link: ShareLink] }>()

const tab = defineModel<InspectorTab>('tab', { default: 'people' })
const target = defineModel<FileTarget | null>('target', { default: null })
const url = defineModel<string | null>('url', { default: null })

const tabs: Array<{ id: InspectorTab; label: string }> = [
  { id: 'people', label: 'People' },
  { id: 'files', label: 'Files' },
  { id: 'activity', label: 'Activity' },
]

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

const pathInput = ref('')
function openPath() {
  const loc = parseLocation(pathInput.value)
  if (!loc.path) return
  url.value = null
  target.value = { path: loc.path, line: loc.line }
  pathInput.value = ''
}

const activityDesc = computed(() => [...props.activity].reverse())
function describe(e: ActivityEntry) {
  const by = e.byName ? `${e.byName} ` : ''
  switch (e.type) {
    case 'input':
      return `${by}answered${e.message ? `: ${e.message}` : ''}`
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
        @click="tab = t.id"
      >
        {{ t.label }}
      </button>
    </div>

    <div v-if="tab === 'people'" class="flex-1 min-h-0 overflow-y-auto p-4 flex flex-col gap-6">
      <section class="flex flex-col gap-2.5">
        <h3 class="text-[11px] font-semibold uppercase tracking-wider text-muted">Here now · {{ viewers.length || session.viewers }}</h3>
        <p v-if="!viewers.length" class="text-xs text-muted">Names arrive once everyone reconnects.</p>
        <div v-for="v in viewers" :key="v.id" class="flex items-center gap-2.5">
          <span class="grid size-7 place-items-center rounded-full bg-primary text-[11px] font-semibold text-inverted flex-none" :class="typing(v) && 'ring-2 ring-warning ring-offset-2 ring-offset-default'">{{ initials(v.name) }}</span>
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
      <form class="flex items-center gap-1 border-b border-default px-2 py-1.5" @submit.prevent="openPath">
        <UInput v-model="pathInput" placeholder="open path[:line]" size="xs" class="w-full font-mono" icon="i-lucide-file-search" />
      </form>
      <FileBrowser v-model:target="target" v-model:url="url" :request="request" :cwd="session.cwd" :raw-url="rawUrl" class="flex-1 min-h-0" />
    </div>

    <div v-else class="flex-1 min-h-0 overflow-y-auto p-4">
      <p v-if="!activityDesc.length" class="text-xs text-muted">Nothing yet.</p>
      <ol v-else class="flex flex-col gap-2 text-sm">
        <li v-for="(e, i) in activityDesc" :key="i" class="flex gap-2.5">
          <span class="font-mono text-[11px] text-muted pt-0.5 flex-none">{{ new Date(e.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</span>
          <span class="min-w-0 break-words">{{ describe(e) }}</span>
        </li>
      </ol>
    </div>
  </aside>
</template>
