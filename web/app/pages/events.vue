<script setup lang="ts">
import type { Integration, WebhookInfo } from '~/composables/useSessions'

useHead({ title: 'Events' })

const api = useSessions()
const admin = useAdminToken()
const attention = useAttention()
const events = useEvents()
const { httpBase } = useApiBase()

const integrations = ref<Integration[]>([])
const serverHost = ref('')
const webhooks = ref<WebhookInfo[]>([])
const loading = ref(false)
const error = ref('')
// The clock of the activity chart's window.
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
const skillCommands = [
  '"${CONDUCTOR_BIN:-conductor}" notify --event progress --message "4/7 handlers"',
  '"${CONDUCTOR_BIN:-conductor}" notify --event artifact --url https://github.com/acme/api/pull/212',
  '"${CONDUCTOR_BIN:-conductor}" notify --event handoff --to tests --message "/v1/users done"',
  'conductor skill',
]
</script>

<template>
  <UDashboardPanel id="events">
    <template #header>
      <UDashboardNavbar title="Events">
        <template #right>
          <UButton label="Refresh" icon="i-lucide-refresh-cw" color="neutral" variant="outline" :loading="loading" @click="refresh" />
          <FullscreenButton />
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
          <IntegrationCard v-for="i in integrations" :key="i.id" :integration="i" :host="host" :home-known="homeKnown" @changed="refresh" />
          <p v-if="!integrations.length && !loading && !error" class="text-sm text-muted">No hook adapters listed.</p>

          <UCard :ui="{ body: 'flex flex-col gap-3' }" data-skill-card>
            <div class="flex items-center gap-2.5">
              <UIcon name="i-lucide-book-open-text" class="size-6 flex-none text-primary" />
              <span class="font-semibold text-highlighted">Conductor skill</span>
              <span class="ml-auto text-xs text-muted">any SKILL.md reader</span>
            </div>
            <p class="text-sm">Teaches agents to report progress, artifacts, blockers and handoffs to named crew members, not just “needs input”.</p>
            <CodeBlock :commands="skillCommands" />
            <p class="text-sm text-muted">
              The notify commands run <code>CONDUCTOR_BIN</code>, the conductor binary every session's hooks run, and fall back to the
              <code>conductor</code> on the <code>PATH</code> where it is unset.
            </p>
            <p class="text-sm text-muted">
              Every agent that reads skills gets it when it is first launched after the server starts, in its skills directory in the server user's
              home ({{ skillReaderNames }}); <b class="text-default">Install on this machine</b> on their cards puts it there too, with the hooks.
              Every session names the server's copy in <code>CONDUCTOR_SKILL</code>.
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
          <RoutingMatrix :routes="events.routes.value" :webhooks="webhooks" @update="events.setRoute" />
          <ActivityChart :entries="events.entries.value" :now="now" />
          <EventFeed :entries="events.entries.value" />
        </section>
      </div>
    </template>
  </UDashboardPanel>
</template>
