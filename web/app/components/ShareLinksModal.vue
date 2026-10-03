<script setup lang="ts">
import type { ReachInfo, ShareLink } from '~/composables/useSessions'
import type { Role } from '~/utils/protocol'
import { linkReach } from '~/utils/reach'

/** A session's links, or a crew run's: those open every member of the run, one added later included. Exactly one of the two is given. */
type Target = { sessionId: string; runId?: undefined } | { runId: string; sessionId?: undefined }
const props = defineProps<Target & { sessionName?: string }>()
const open = defineModel<boolean>('open', { default: false })

const api = useSessions()

/** The link routes of whichever the modal shares. */
function routes() {
  if (props.runId !== undefined) {
    const run = props.runId
    return {
      list: () => api.listRunLinks(run),
      create: (body: { role: Role; label?: string; ttlSeconds?: number }) => api.createRunLink(run, body),
      revoke: (linkId: string) => api.revokeRunLink(run, linkId),
    }
  }
  const id = props.sessionId
  return {
    list: () => api.links(id),
    create: (body: { role: Role; label?: string; ttlSeconds?: number }) => api.createLink(id, body),
    revoke: (linkId: string) => api.revokeLink(id, linkId),
  }
}
const title = computed(() => (props.sessionName ? `Share ${props.sessionName}` : props.runId ? 'Share this crew' : 'Share this session'))
const description = computed(() =>
  props.runId
    ? 'Anyone with the link joins every agent of the crew, one added later too, with the role you pick. Revoking disconnects them.'
    : 'Anyone with the link joins with the role you pick. Revoking disconnects them.',
)
const toast = useToast()
const copyText = useCopy()
const links = ref<ShareLink[]>([])
const loading = ref(false)
const creating = ref(false)
const error = ref('')
const created = ref<{ url: string; role: Role; label?: string; invite?: string; remote?: boolean } | null>(null)
/** The server's reach report, read when the modal opens; null when it could not be read. */
const reach = ref<ReachInfo | null>(null)
const createdReach = computed(() => (created.value ? linkReach(created.value.url, reach.value) : null))
const reachColor: Record<string, 'success' | 'warning' | 'neutral' | 'info'> = { public: 'success', pending: 'warning', local: 'neutral', unknown: 'info' }

const form = reactive<{ role: Role; label: string; ttl: string }>({ role: 'view', label: '', ttl: '7200' })
const roles: Array<{ value: Role; label: string; description: string }> = [
  { value: 'view', label: 'View', description: "Watch output, open files. Can't type." },
  { value: 'control', label: 'Control', description: 'Types into the agent, answers prompts.' },
]
const ttlItems = [
  { label: 'Never', value: '0' },
  { label: 'In 1 hour', value: '3600' },
  { label: 'In 2 hours', value: '7200' },
  { label: 'In 8 hours', value: '28800' },
  { label: 'In 24 hours', value: '86400' },
  { label: 'In 7 days', value: '604800' },
]

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    links.value = await routes().list()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

watch(open, (v) => {
  if (v) {
    created.value = null
    refresh()
    api
      .reach()
      .then((r) => (reach.value = r))
      .catch(() => (reach.value = null))
  }
})

async function create() {
  creating.value = true
  error.value = ''
  try {
    const res = await routes().create({ role: form.role, label: form.label || undefined, ttlSeconds: Number(form.ttl) || undefined })
    created.value = { url: res.url, role: res.link.role, label: res.link.label, invite: res.invite, remote: 'remote' in res && res.remote === true }
    form.label = ''
    await refresh()
    await copy(res.url)
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    creating.value = false
  }
}

function copy(url: string) {
  return copyText(url, 'Link copied', 'The token is shown once; keep it private.')
}

async function revoke(link: ShareLink) {
  try {
    await routes().revoke(link.id)
    toast.add({ title: 'Link revoked', description: 'Viewers using it were disconnected.', icon: 'i-lucide-ban', color: 'neutral' })
    if (created.value && links.value.find((l) => l.id === link.id)) created.value = null
    await refresh()
  } catch (e) {
    error.value = (e as Error).message
  }
}

function fmt(ts?: string) {
  return ts ? new Date(ts).toLocaleString() : ''
}

/** Shortens a long join URL for display: host/join/k7Qx…9fRm */
function shortUrl(url: string) {
  const m = /^(https?:\/\/)?([^/]+)\/join\/(.+)$/.exec(url)
  if (!m) return url
  const tok = m[3]!
  return `${m[2]}/join/${tok.length > 12 ? `${tok.slice(0, 4)}…${tok.slice(-4)}` : tok}`
}

const live = computed(() => links.value.filter((l) => !l.revoked))
</script>

<template>
  <UModal v-model:open="open" :title="title" :description="description" :ui="{ content: 'max-w-lg' }">
    <template #body>
      <div class="flex flex-col gap-4">
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />

        <form class="flex flex-col gap-4" @submit.prevent="create">
          <div class="grid grid-cols-2 gap-2" role="radiogroup" aria-label="Role">
            <button
              v-for="r in roles"
              :key="r.value"
              type="button"
              role="radio"
              :aria-checked="form.role === r.value"
              class="flex flex-col gap-1 rounded-md border p-3 text-left transition-colors"
              :class="form.role === r.value ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'border-default hover:border-accented'"
              @click="form.role = r.value"
            >
              <span class="text-sm font-semibold">{{ r.label }}</span>
              <span class="text-xs leading-snug text-muted">{{ r.description }}</span>
            </button>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <UFormField label="Label" name="label">
              <UInput v-model="form.label" placeholder="pairing" class="w-full" />
            </UFormField>
            <UFormField label="Expires" name="ttl">
              <USelect v-model="form.ttl" :items="ttlItems" class="w-full" />
            </UFormField>
          </div>
          <UButton label="Create link" type="submit" icon="i-lucide-link" :loading="creating" class="self-end" />
        </form>

        <div v-if="created" class="flex flex-col gap-2 rounded-md bg-elevated px-3.5 py-3">
          <div class="flex items-center gap-2.5">
            <span class="flex-1 truncate font-mono text-xs" :title="created.url">{{ shortUrl(created.url) }}</span>
            <UButton label="Copy link" size="sm" @click="copy(created!.url)" />
          </div>
          <div v-if="created.invite" class="flex items-center gap-2.5" data-created-invite>
            <span class="flex-1 truncate font-mono text-xs text-muted" :title="created.invite">{{ created.invite }}</span>
            <UButton label="Copy invite" size="sm" color="neutral" variant="outline" @click="copyText(created!.invite!, 'Invite copied', 'It opens in the Conductor app; the link opens in a browser.')" />
          </div>
          <span v-if="created.remote" class="text-xs text-muted" data-created-remote>Minted at the rendezvous: the link and the invite reach it, which hands the terminal to this machine.</span>
          <span class="text-xs text-secondary">Shown once. Copy it now; you can always make a new one.</span>
          <UAlert
            v-if="createdReach"
            :color="reachColor[createdReach.level]"
            variant="soft"
            :icon="createdReach.level === 'public' ? 'i-lucide-globe' : createdReach.level === 'pending' ? 'i-lucide-hourglass' : 'i-lucide-house'"
            :title="createdReach.title"
            :description="createdReach.text"
            :data-link-reach="createdReach.level"
            class="mt-1"
          />
        </div>

        <div>
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-[11px] font-semibold uppercase tracking-wider text-muted">Links</h3>
            <UButton icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" :loading="loading" aria-label="Refresh" @click="refresh" />
          </div>
          <p v-if="!live.length && !loading" class="text-sm text-muted">No links yet.</p>
          <ul v-else class="flex flex-col gap-2">
            <li v-for="link in live" :key="link.id" class="flex flex-col gap-1 rounded-md border border-default px-3 py-2.5">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium truncate">{{ link.label || 'unlabelled' }}</span>
                <UBadge :label="link.role === 'control' ? 'Control' : 'View'" :color="link.role === 'control' ? 'primary' : 'neutral'" :variant="link.role === 'control' ? 'subtle' : 'outline'" size="sm" />
                <UButton label="Revoke" size="xs" variant="link" color="secondary" class="ml-auto" @click="revoke(link)" />
              </div>
              <span class="font-mono text-[11px] text-muted">
                created {{ fmt(link.createdAt) }}<template v-if="link.expiresAt"> · expires {{ fmt(link.expiresAt) }}</template><template v-if="link.active !== undefined"> · {{ link.active }} using</template>
              </span>
            </li>
          </ul>
        </div>
      </div>
    </template>
  </UModal>
</template>
