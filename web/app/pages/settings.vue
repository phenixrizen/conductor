<script setup lang="ts">
import type { AgentInfo, ReachInfo } from '~/composables/useSessions'
import { identity } from '~/utils/agents'
import type { DesktopIceStatus, DesktopSettings } from '~/utils/desktop'

// The desktop app's settings: what its shell starts the server with. In a
// browser against a server configured by its file, the page says so.
const desktop = useDesktop()
const api = useSessions()
const toast = useToast()
const serverHost = useServerHost()

const settings = ref<DesktopSettings | null>(null)
const form = reactive<DesktopSettings>({ dataDir: '', allowedRoots: [], defaultCwd: '', yolo: false, reach: 'auto', closeToTray: true, wslDistro: '', wslWindowsHome: false, switchyardEnabled: true, switchyardServer: '', switchyardToken: '', switchyardName: '' })
const saving = ref(false)
const error = ref('')
const reach = ref<ReachInfo | null>(null)
/** Windows: what the app forwards for WebRTC from WSL (null elsewhere). */
const ice = ref<DesktopIceStatus | null>(null)
const allowing = ref(false)
async function allowFirewall() {
  const b = desktop.bridge.value
  if (!b || !ice.value) return
  allowing.value = true
  try {
    ice.value = { ...ice.value, firewall: await b.allowIceFirewall() }
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    allowing.value = false
  }
}
const agents = ref<AgentInfo[]>([])
const versions = ref<{ app: string; electron: string; node: string; chrome: string; server: string } | null>(null)
const rootsText = computed({
  get: () => form.allowedRoots.join('\n'),
  set: (v: string) => (form.allowedRoots = v.split('\n').map((s) => s.trim()).filter(Boolean)),
})
const reachItems = [
  { label: 'Auto: map the TLS port on the router when there is one', value: 'auto' },
  { label: 'Manual: find the public address only', value: 'manual' },
  { label: 'Off', value: 'off' },
]
const dirty = computed(() => !!settings.value && JSON.stringify({ ...settings.value }) !== JSON.stringify({ ...form }))
const restarts = computed(() => {
  if (!settings.value) return false
  const keys: Array<keyof DesktopSettings> = ['dataDir', 'allowedRoots', 'defaultCwd', 'yolo', 'reach', 'wslDistro', 'wslWindowsHome', 'switchyardEnabled', 'switchyardServer', 'switchyardToken', 'switchyardName']
  return keys.some((k) => JSON.stringify(settings.value![k]) !== JSON.stringify(form[k]))
})

async function load() {
  const b = desktop.bridge.value
  if (!b) return
  try {
    const s = await b.settings.get()
    settings.value = s
    Object.assign(form, s)
    versions.value = await b.versions()
    if (b.platform === 'win32') ice.value = await b.ice()
  } catch (e) {
    error.value = (e as Error).message
  }
  await Promise.all([
    api.reach().then((r) => (reach.value = r)).catch(() => (reach.value = null)),
    api.catalog().then((c) => (agents.value = c)).catch(() => (agents.value = [])),
  ])
}

async function save() {
  const b = desktop.bridge.value
  if (!b) return
  saving.value = true
  error.value = ''
  try {
    const next = await b.settings.set({ ...form })
    settings.value = next
    Object.assign(form, next)
    toast.add({ title: restarts.value ? 'Saved; the server restarted' : 'Saved', icon: 'i-lucide-check', color: 'success' })
    await load()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function pick(field: 'dataDir' | 'defaultCwd' | 'root') {
  const dir = await desktop.bridge.value?.settings.pickDirectory()
  if (!dir) return
  if (field === 'root') form.allowedRoots = [...form.allowedRoots, dir]
  else form[field] = dir
}

onMounted(load)
</script>

<template>
  <UDashboardPanel id="settings">
    <template #header>
      <UDashboardNavbar title="Settings" :ui="{ root: 'h-14' }">
        <template #leading><UDashboardSidebarCollapse /></template>
        <template #right>
          <template v-if="desktop.isDesktop.value">
            <UButton label="Open in browser" icon="i-lucide-external-link" color="neutral" variant="outline" data-open-in-browser @click="desktop.bridge.value?.openInBrowser()" />
            <UButton label="Server log" icon="i-lucide-scroll-text" color="neutral" variant="outline" @click="desktop.bridge.value?.showLog()" />
            <UButton label="Restart server" icon="i-lucide-rotate-ccw" color="neutral" variant="outline" @click="desktop.bridge.value?.restartServer()" />
          </template>
          <FullscreenButton />
        </template>
      </UDashboardNavbar>
    </template>
    <template #body>
      <div v-if="!desktop.isDesktop.value" class="max-w-2xl">
        <UAlert color="neutral" variant="subtle" icon="i-lucide-info" title="This server is configured by its file" description="These settings belong to the desktop app, which starts a server of its own. A server you run yourself takes its configuration file and CONDUCTOR_* variables (see the README)." />
      </div>
      <div v-else class="flex max-w-3xl flex-col gap-6" data-desktop-settings>
        <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
        <UAlert
          v-if="desktop.serverState.value"
          :color="desktop.serverState.value.state === 'running' ? 'success' : desktop.serverState.value.state === 'failed' ? 'error' : 'neutral'"
          variant="subtle"
          :icon="desktop.serverState.value.state === 'running' ? 'i-lucide-check' : 'i-lucide-loader-circle'"
          :title="`Server ${desktop.serverState.value.state}${desktop.serverState.value.url ? ` at ${desktop.serverState.value.url}` : ''}`"
          :description="desktop.serverState.value.lastError || (desktop.serverState.value.version ? `conductor ${desktop.serverState.value.version}` : '')"
          data-server-state
        />
        <UCard v-if="ice" data-ice-status>
          <template #header><h2 class="font-semibold">WebRTC from WSL</h2></template>
          <div class="flex flex-col gap-3 text-sm">
            <p v-if="ice.forwarding">
              The app forwards UDP port <code>{{ ice.port }}</code> on <code>{{ ice.publicIp }}</code> into the distribution at <code>{{ ice.wslAddress }}</code>, so a shared session connects peer to peer through your router only. WSL's own networking is left as it is.
            </p>
            <p v-else class="text-muted">Nothing is forwarded{{ ice.reason ? `: ${ice.reason}` : '' }}.</p>
            <div v-if="ice.forwarding" class="flex flex-wrap items-center gap-2">
              <UBadge :label="ice.firewall === 'present' ? 'firewall rule present' : ice.firewall === 'missing' ? 'firewall rule missing' : 'firewall rule unknown'" :color="ice.firewall === 'present' ? 'success' : ice.firewall === 'missing' ? 'warning' : 'neutral'" variant="subtle" data-ice-firewall />
              <UButton v-if="ice.firewall !== 'present'" label="Allow through the Windows firewall" icon="i-lucide-shield-check" size="sm" color="neutral" variant="outline" :loading="allowing" data-ice-allow @click="allowFirewall" />
              <span v-if="ice.firewall !== 'present'" class="text-xs text-muted">One elevation prompt adds an inbound rule for UDP {{ ice.port }}.</span>
            </div>
          </div>
        </UCard>

        <UCard data-switchyard-settings>
          <template #header><h2 class="font-semibold">Switchyard</h2></template>
          <div class="flex flex-col gap-4">
            <USwitch v-model="form.switchyardEnabled" label="Publish sessions to a switchyard" description="So a share link works from anywhere." data-switchyard-enabled />
            <p class="text-sm text-muted">
              Every session here is shared through a public switchyard, switchyard.rslabs.net unless you name another: the link is minted there, and the terminal goes between the viewer and this machine, through the switchyard's relay only when it must. Off, links work where this machine is reachable.
            </p>
            <UFormField label="Switchyard" description="Leave empty for the public one.">
              <UInput v-model="form.switchyardServer" class="w-full font-mono text-xs" placeholder="https://switchyard.rslabs.net" :disabled="!form.switchyardEnabled" />
            </UFormField>
            <UFormField label="Host token" description="Optional: a host token for a private switchyard, or a trusted seat on the public one, outside its per-address limits.">
              <UInput v-model="form.switchyardToken" type="password" class="w-full font-mono text-xs" autocomplete="off" :disabled="!form.switchyardEnabled" />
            </UFormField>
            <UFormField label="Shown there as" description="How this machine is named at the switchyard; its host name when empty.">
              <UInput v-model="form.switchyardName" class="w-full" maxlength="64" :disabled="!form.switchyardEnabled" />
            </UFormField>
          </div>
        </UCard>

        <UCard>
          <template #header><h2 class="font-semibold">Where agents work</h2></template>
          <div class="flex flex-col gap-4">
            <p v-if="desktop.bridge.value?.platform === 'win32'" class="text-sm text-muted" data-wsl-paths>
              The server runs inside your WSL distribution, so these are its paths (such as <code class="font-mono">/home/&lt;user&gt;/code</code>); the folder picker opens there. Windows folders need the switch under "How agents run" and live under <code class="font-mono">/mnt</code>.
            </p>
            <UFormField label="Allowed roots" description="Directories server sessions may run in, one per line. The agents can read and change everything under them.">
              <div class="flex gap-2">
                <UTextarea v-model="rootsText" :rows="3" class="flex-1 font-mono text-xs" />
                <UButton icon="i-lucide-folder-plus" color="neutral" variant="outline" aria-label="Add a root" @click="pick('root')" />
              </div>
            </UFormField>
            <UFormField label="Default working directory" description="Where a launch runs when it names no directory; under an allowed root.">
              <div class="flex gap-2"><UInput v-model="form.defaultCwd" class="flex-1 font-mono text-xs" /><UButton icon="i-lucide-folder" color="neutral" variant="outline" aria-label="Pick" @click="pick('defaultCwd')" /></div>
            </UFormField>
            <UFormField label="Data directory" description="The server's catalog, crews, hooks and certificates. Keep it outside the allowed roots.">
              <div class="flex gap-2"><UInput v-model="form.dataDir" class="flex-1 font-mono text-xs" /><UButton icon="i-lucide-folder" color="neutral" variant="outline" aria-label="Pick" @click="pick('dataDir')" /></div>
            </UFormField>
          </div>
        </UCard>

        <UCard>
          <template #header><h2 class="font-semibold">How agents run</h2></template>
          <div class="flex flex-col gap-4">
            <UFormField label="Yolo" description="Launch every agent with its yolo recipe, skipping its permission prompts (Codex's drops its sandbox). Only for roots you would let an agent change unasked.">
              <USwitch v-model="form.yolo" label="Skip permission prompts" />
            </UFormField>
            <UFormField label="Reach" description="How the server finds out it can be reached from outside your network. Links become public only once a TLS listener has a certificate.">
              <USelect v-model="form.reach" :items="reachItems" class="w-full" />
            </UFormField>
            <div v-if="reach" class="rounded-md bg-elevated px-3 py-2 text-xs font-mono" data-reach-state>
              reach {{ reach.mode }}<template v-if="reach.externalIp"> · public address {{ reach.externalIp }}</template><template v-if="reach.mapped"> · port {{ reach.externalPort }} mapped via {{ reach.method }}</template><template v-if="reach.tls"> · certificate {{ reach.tls.ready ? 'ready' : 'pending' }}</template><template v-if="reach.error"> · {{ reach.error }}</template>
            </div>
            <UFormField v-if="desktop.bridge.value?.platform === 'win32'" label="WSL distribution" description="The distribution the server runs in; empty for the default. Agents are found on its PATH.">
              <UInput v-model="form.wslDistro" class="w-full font-mono text-xs" placeholder="Ubuntu" />
            </UFormField>
            <UFormField v-if="desktop.bridge.value?.platform === 'win32'" label="Windows folders">
              <USwitch v-model="form.wslWindowsHome" label="Also allow your Windows profile under /mnt (slower)" />
            </UFormField>
          </div>
        </UCard>

        <UCard>
          <template #header><h2 class="font-semibold">The app</h2></template>
          <div class="flex flex-col gap-4">
            <USwitch v-model="form.closeToTray" label="Closing the window keeps Conductor in the tray; the server goes on" />
            <div v-if="versions" class="text-xs text-muted font-mono">app {{ versions.app }} · server {{ versions.server }} · electron {{ versions.electron }} · chrome {{ versions.chrome }}</div>
          </div>
        </UCard>

        <div class="flex items-center gap-3">
          <UButton :label="restarts ? 'Save and restart the server' : 'Save'" icon="i-lucide-save" :loading="saving" :disabled="!dirty" data-save-settings @click="save" />
          <span v-if="restarts" class="text-xs text-muted">Changing where or how agents run restarts the server; running sessions end.</span>
        </div>

        <UCard v-if="agents.length">
          <template #header><h2 class="font-semibold">Agents on this machine</h2></template>
          <ul class="flex flex-col gap-1.5 text-sm">
            <li v-for="a in agents" :key="a.id" class="flex items-center gap-2" :data-agent="a.id">
              <span class="font-medium">{{ a.name }}</span>
              <UBadge v-if="a.available === false" label="not installed" color="warning" variant="subtle" size="sm" />
              <UBadge v-else-if="identity(a, serverHost.host.value).state !== 'unprobed'" :label="identity(a, serverHost.host.value).label" :title="identity(a, serverHost.host.value).title" :color="identity(a, serverHost.host.value).state === 'ok' ? 'success' : identity(a, serverHost.host.value).state === 'impostor' ? 'error' : 'neutral'" variant="subtle" size="sm" />
              <code class="ml-auto text-xs text-muted truncate">{{ a.command.join(' ') }}</code>
            </li>
          </ul>
        </UCard>
      </div>
    </template>
  </UDashboardPanel>
</template>
