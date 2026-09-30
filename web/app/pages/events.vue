<script setup lang="ts">
import type { Integration } from '~/composables/useSessions'

useHead({ title: 'Events' })

const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const attention = useAttention()
const events = useEvents()
const { httpBase } = useApiBase()

const integrations = ref<Integration[]>([])
const loading = ref(false)
const error = ref('')

async function refresh() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  loading.value = true
  try {
    integrations.value = await api.integrations()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  // The feed fills from the live store's stream; nothing here polls.
  attention.start()
  refresh()
})
watch(() => admin.token.value, refresh)

/** The machine the server runs on, as this browser reaches it: where Install on this machine writes. */
const host = computed(() => {
  try {
    return new URL(httpBase.value || location.origin).hostname || 'this server'
  } catch {
    return 'this server'
  }
})

// The skill has no install of its own: it comes with the hooks of the agents that read skills.
const SKILL_READERS = ['claude', 'codex', 'pi', 'goose']
const skillReaders = computed(() => integrations.value.filter((i) => SKILL_READERS.includes(i.id)))
const skillCommands = [
  'conductor notify --event progress --message "4/7 handlers"',
  'conductor notify --event artifact --url https://github.com/acme/api/pull/212',
  'conductor notify --event handoff --to tests --message "/v1/users done"',
  'conductor skill',
]

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.add({ title: 'Copied', description: text, icon: 'i-lucide-clipboard-check', color: 'success' })
  } catch {
    toast.add({ title: 'Copy failed', description: 'Select the command and copy it by hand.', color: 'warning' })
  }
}
</script>

<template>
  <UDashboardPanel id="events">
    <template #header>
      <UDashboardNavbar title="Events">
        <template #leading>
          <SidebarReveal />
        </template>
        <template #right>
          <UButton label="Refresh" icon="i-lucide-refresh-cw" color="neutral" variant="outline" :loading="loading" @click="refresh" />
        </template>
      </UDashboardNavbar>
    </template>
    <template #body>
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="mb-4" />
      <p class="text-sm text-muted mb-4">
        Every session gets <code>CONDUCTOR_NOTIFY_URL</code> and <code>CONDUCTOR_NOTIFY_TOKEN</code>: agents report through their hooks and
        <code>conductor notify</code>. Routing decides where each kind of event goes in this browser.
      </p>
      <div class="grid gap-6 xl:grid-cols-[minmax(0,28rem)_minmax(0,1fr)]">
        <section class="flex min-w-0 flex-col gap-3" aria-labelledby="integrations-heading">
          <h2 id="integrations-heading" class="text-[11px] font-semibold uppercase tracking-wider text-muted">Integrations</h2>
          <IntegrationCard v-for="i in integrations" :key="i.id" :integration="i" :host="host" @changed="refresh" />
          <p v-if="!integrations.length && !loading && !error" class="text-sm text-muted">No hook adapters listed.</p>

          <UCard :ui="{ body: 'flex flex-col gap-3' }" data-skill-card>
            <div class="flex items-center gap-2.5">
              <UIcon name="i-lucide-book-open-text" class="size-6 flex-none text-primary" />
              <span class="font-semibold text-highlighted">Conductor skill</span>
              <span class="ml-auto text-xs text-muted">any SKILL.md reader</span>
            </div>
            <p class="text-sm">Teaches agents to report progress, artifacts, blockers and handoffs to named crew members, not just “needs input”.</p>
            <ul class="rounded-md bg-forest-950 px-3 py-2 font-mono text-xs leading-relaxed text-forest-100">
              <li v-for="c in skillCommands" :key="c" class="flex items-center gap-2">
                <span class="select-none text-forest-400">$</span>
                <span class="min-w-0 flex-1 truncate select-text" :title="c">{{ c }}</span>
                <UButton icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" :aria-label="`Copy ${c}`" class="text-active-300 hover:bg-forest-900 hover:text-active-200" @click="copy(c)" />
              </li>
            </ul>
            <p class="text-sm text-muted">
              The skill installs together with the hooks of Claude Code, Codex, pi and Goose: <b class="text-default">Install on this machine</b> on their cards
              puts it in their skills directory.
            </p>
            <div v-if="skillReaders.length" class="flex flex-wrap gap-1.5">
              <UBadge
                v-for="i in skillReaders"
                :key="i.id"
                :label="`${i.name}: ${i.installed ? 'installed' : 'not installed'}`"
                :color="i.installed ? 'success' : 'neutral'"
                :variant="i.installed ? 'subtle' : 'outline'"
                size="sm"
              />
            </div>
          </UCard>
        </section>

        <section class="flex min-w-0 flex-col gap-3" aria-labelledby="routing-heading">
          <h2 id="routing-heading" class="text-[11px] font-semibold uppercase tracking-wider text-muted">Routing</h2>
          <RoutingMatrix :routes="events.routes.value" @update="events.setRoute" />
          <EventFeed :entries="events.entries.value" />
        </section>
      </div>
    </template>
  </UDashboardPanel>
</template>
