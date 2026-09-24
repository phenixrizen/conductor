<script setup lang="ts">
import type { ShareLink } from '~/composables/useSessions'
import type { Role } from '~/utils/protocol'

const props = defineProps<{ sessionId: string }>()
const open = defineModel<boolean>('open', { default: false })

const api = useSessions()
const toast = useToast()
const links = ref<ShareLink[]>([])
const loading = ref(false)
const creating = ref(false)
const error = ref('')
const created = ref<{ url: string; role: Role; label?: string } | null>(null)

const form = reactive<{ role: Role; label: string; ttl: string }>({ role: 'view', label: '', ttl: '0' })
const roleItems = [
  { label: 'View only', value: 'view', icon: 'i-lucide-eye' },
  { label: 'Control (can type)', value: 'control', icon: 'i-lucide-keyboard' },
]
const ttlItems = [
  { label: 'Never expires', value: '0' },
  { label: '1 hour', value: '3600' },
  { label: '8 hours', value: '28800' },
  { label: '24 hours', value: '86400' },
  { label: '7 days', value: '604800' },
]

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    links.value = await api.links(props.sessionId)
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
  }
})

async function create() {
  creating.value = true
  error.value = ''
  try {
    const res = await api.createLink(props.sessionId, { role: form.role, label: form.label || undefined, ttlSeconds: Number(form.ttl) || undefined })
    created.value = { url: res.url, role: res.link.role, label: res.link.label }
    form.label = ''
    await refresh()
    await copy(res.url)
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    creating.value = false
  }
}

async function copy(url: string) {
  try {
    await navigator.clipboard.writeText(url)
    toast.add({ title: 'Link copied', description: 'The token is shown once; keep it private.', icon: 'i-lucide-clipboard-check', color: 'success' })
  } catch {
    toast.add({ title: 'Copy failed', description: 'Select the link and copy it manually.', color: 'warning' })
  }
}

async function revoke(link: ShareLink) {
  try {
    await api.revokeLink(props.sessionId, link.id)
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
</script>

<template>
  <UModal v-model:open="open" title="Share this session" description="Links carry their own token. Anyone with a link can join with the role you choose.">
    <template #body>
      <div class="flex flex-col gap-4">
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />

        <form class="grid grid-cols-1 sm:grid-cols-[1fr_1fr_auto] gap-2 items-end" @submit.prevent="create">
          <UFormField label="Role" name="role">
            <USelect v-model="form.role" :items="roleItems" class="w-full" />
          </UFormField>
          <UFormField label="Expires" name="ttl">
            <USelect v-model="form.ttl" :items="ttlItems" class="w-full" />
          </UFormField>
          <UButton label="Create link" type="submit" icon="i-lucide-link" :loading="creating" />
          <UFormField label="Label" name="label" class="sm:col-span-3">
            <UInput v-model="form.label" placeholder="who is this for? (optional)" class="w-full" />
          </UFormField>
        </form>

        <UAlert
          v-if="created"
          color="primary"
          variant="subtle"
          icon="i-lucide-key-round"
          title="New link (shown once)"
          :actions="[{ label: 'Copy', icon: 'i-lucide-copy', onClick: () => copy(created!.url) }]"
        >
          <template #description>
            <code class="text-xs break-all select-all">{{ created.url }}</code>
          </template>
        </UAlert>

        <div>
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-sm font-medium">Existing links</h3>
            <UButton icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" :loading="loading" aria-label="Refresh" @click="refresh" />
          </div>
          <p v-if="!links.length && !loading" class="text-sm text-muted">No links yet.</p>
          <ul v-else class="divide-y divide-default border border-default rounded-lg">
            <li v-for="link in links" :key="link.id" class="flex items-center gap-3 px-3 py-2 text-sm">
              <UBadge :label="link.role" :color="link.role === 'control' ? 'warning' : 'neutral'" variant="subtle" size="sm" />
              <div class="flex-1 min-w-0">
                <div class="truncate">{{ link.label || 'unlabelled' }}</div>
                <div class="text-xs text-muted">
                  created {{ fmt(link.createdAt) }}<span v-if="link.expiresAt"> · expires {{ fmt(link.expiresAt) }}</span>
                </div>
              </div>
              <UBadge v-if="link.revoked" label="revoked" color="error" variant="subtle" size="sm" />
              <UButton v-else label="Revoke" size="xs" color="error" variant="soft" icon="i-lucide-ban" @click="revoke(link)" />
            </li>
          </ul>
        </div>
      </div>
    </template>
  </UModal>
</template>
