<script setup lang="ts">
import type { Integration, WebhookInfo } from '~/composables/useSessions'

useHead({ title: 'Events' })

const api = useSessions()
const admin = useWorkbenchToken()
const attention = useAttention()
const events = useEvents()
const { httpBase } = useApiBase()

const integrations = ref<Integration[]>([])
const serverHost = ref('')
const webhooks = ref<WebhookInfo[]>([])
const loading = ref(false)
const error = ref('')
// The clock of the last hour's window and of the feed's "now".
const now = ref(Date.now())
let tick: number | undefined

async function refresh() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  loading.value = true
  try {
    const r = await api.integrations()
    integrations.value = r.integrations
    serverHost.value = r.host
    webhooks.value = r.webhooks
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
  tick = window.setInterval(() => (now.value = Date.now()), 15000)
})
onBeforeUnmount(() => window.clearInterval(tick))
watch(() => admin.token.value, refresh)

/**
 * Whether the server knows its user's home directory: it lists where each
 * adapter it can install goes, and lists none of them without one.
 */
const homeKnown = computed(() => integrations.value.some((i) => i.where))

/**
 * The machine the server runs on, where Install on this machine writes: the
 * name the server gives itself, else the host this browser reaches it by.
 */
const host = computed(() => {
  if (serverHost.value) return serverHost.value
  try {
    return new URL(httpBase.value || location.origin).hostname || 'this server'
  } catch {
    return 'this server'
  }
})

// The skill has no install of its own: a launch puts it in place, and so do the hooks of the agents that read skills.
const skillReaders = computed(() => integrations.value.filter((i) => i.installsSkill))
/** Their names as prose: "Claude Code, Codex, pi and Goose". */
const skillReaderNames = computed(() => {
  const names = skillReaders.value.map((i) => i.name)
  return names.length > 1 ? `${names.slice(0, -1).join(', ')} and ${names.at(-1)}` : (names[0] ?? 'the agents that read skills')
})
// The notify commands as the skill writes them: the binary the session names in CONDUCTOR_BIN, else the conductor on PATH.
/** The page's tab, in the URL so a link can open Routing or Integrations. */
const route = useRoute()
const router = useRouter()
type EventsTab = 'feed' | 'routing' | 'integrations'
const tab = computed<EventsTab>({
  get: () => (route.query.tab === 'routing' || route.query.tab === 'integrations' ? route.query.tab : 'feed'),
  set: (t) => router.replace({ query: { ...route.query, tab: t === 'feed' ? undefined : t } }),
})
const tabs = [
  { label: 'Feed', value: 'feed' },
  { label: 'Routing', value: 'routing' },
  { label: 'Integrations', value: 'integrations' },
]
const skillCommands = [
  '"${CONDUCTOR_BIN:-conductor}" notify --event progress --message "4/7 handlers"',
  '"${CONDUCTOR_BIN:-conductor}" notify --event artifact --url https://github.com/acme/api/pull/212',
  '"${CONDUCTOR_BIN:-conductor}" notify --event handoff --to tests --message "/v1/users done"',
  'conductor skill',
]
</script>

<template>
  <UDashboardPanel id="events" :ui="{ body: 'p-0 sm:p-0 gap-0' }">
    <template #header>
      <UDashboardNavbar title="Events">
        <template #right>
          <UButton label="Refresh" icon="i-lucide-refresh-cw" color="neutral" variant="outline" :loading="loading" @click="refresh" />
          <FullscreenButton />
        </template>
      </UDashboardNavbar>
    </template>
    <template #body>
      <div class="flex-none px-4 pt-2 sm:px-6">
        <UTabs v-model="tab" :items="tabs" :content="false" color="neutral" variant="link" size="sm" data-events-tabs />
      </div>
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="mx-4 mt-4 sm:mx-6" />

      <div v-if="tab === 'feed'" class="flex min-h-0 flex-1 flex-col xl:flex-row" data-events-feed-tab>
        <div class="min-w-0 flex-1 px-4 pb-6 sm:px-6">
          <EventFeed :entries="events.entries.value" :now="now" />
        </div>
        <EventsAside
          :entries="events.entries.value"
          :now="now"
          :routes="events.routes.value"
          :integrations="integrations"
          class="flex-none border-t border-default p-5 xl:w-[340px] xl:border-t-0 xl:border-l"
          @edit-routing="tab = 'routing'"
        />
      </div>

      <div v-else-if="tab === 'routing'" class="flex flex-col gap-4 px-4 py-5 sm:px-6" data-events-routing-tab>
        <p class="max-w-4xl text-[13.5px] text-muted">Where each kind of event goes in this browser. The routing is kept here, not on the server; webhooks are set in <code>conductor.json</code>.</p>
        <RoutingRows :routes="events.routes.value" :webhooks="webhooks" @update="events.setRoute" />
      </div>

      <div v-else class="flex flex-col gap-4 px-4 py-5 sm:px-6" data-events-integrations-tab>
        <p class="max-w-4xl text-[13.5px] leading-normal text-muted">
          Every session gets <code>CONDUCTOR_NOTIFY_URL</code> and <code>CONDUCTOR_NOTIFY_TOKEN</code>. An agent with hooks reports through them; one without falls back to
          the terminal bell and screen patterns, which only know "needs input".
        </p>
        <IntegrationsTable :integrations="integrations" :host="host" :home-known="homeKnown" @changed="refresh" />

        <section class="flex flex-col gap-3 rounded-lg p-4 ring ring-default" data-skill-card>
          <div class="flex items-center gap-2.5">
            <UIcon name="i-lucide-book-open-text" class="size-5 flex-none text-primary" />
            <span class="font-semibold text-highlighted">Conductor skill</span>
            <span class="ml-auto text-xs text-muted">any SKILL.md reader</span>
          </div>
          <p class="text-sm">
            Teaches agents to report progress, artifacts, blockers and handoffs to named crew members, not just "needs input". Every agent that reads skills gets it at its
            first launch ({{ skillReaderNames }}); <b class="text-default">Install on this machine</b> puts it there with the hooks.
          </p>
          <CodeBlock :commands="skillCommands" />
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
        </section>
      </div>
    </template>
  </UDashboardPanel>
</template>
