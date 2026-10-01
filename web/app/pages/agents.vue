<script setup lang="ts">
import type { AgentInfo } from '~/composables/useSessions'
import { joinArgv } from '~/utils/argv'
import { agentIcon } from '~/utils/agentIcons'
import { removalOf, removalText } from '~/utils/catalog'

useHead({ title: 'Agents' })

const api = useSessions()
const admin = useAdminToken()
const toast = useToast()
const agents = ref<AgentInfo[]>([])
/** IDs the server hides from the catalog; each can be restored. */
const hidden = ref<string[]>([])
const loading = ref(false)
const error = ref('')
const launch = useLaunchModal()

async function refresh() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  loading.value = true
  try {
    const r = await api.catalogWithHidden()
    agents.value = r.agents
    hidden.value = r.hidden
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
watch(() => admin.token.value, refresh)

// Add and edit share one slideover; `editing` is the agent being changed.
const formOpen = ref(false)
const editing = ref<AgentInfo | undefined>()

function addAgent() {
  editing.value = undefined
  formOpen.value = true
}
function editAgent(a: AgentInfo) {
  editing.value = a
  formOpen.value = true
}
async function onSaved() {
  await refresh()
}

// Hide asks first. The target outlives the dialog so its text does not blank
// while it fades out.
const hideOpen = ref(false)
const hideTarget = ref<AgentInfo | null>(null)
const hiding = ref(false)
const hideError = ref('')

function askHide(a: AgentInfo) {
  hideTarget.value = a
  hideError.value = ''
  hideOpen.value = true
}

const hideText = computed(() => (hideTarget.value ? removalText(removalOf(hideTarget.value), hideTarget.value.name, hideTarget.value.id) : undefined))

async function confirmHide() {
  const a = hideTarget.value
  if (!a) return
  const text = removalText(removalOf(a), a.name, a.id)
  hiding.value = true
  hideError.value = ''
  try {
    await api.deleteAgent(a.id)
    hideOpen.value = false
    toast.add({ title: text.toast.title, description: text.toast.description, icon: text.icon, color: 'neutral' })
    await refresh()
  } catch (e) {
    hideError.value = (e as Error).message
  } finally {
    hiding.value = false
  }
}

// Restore takes an ID off the hidden list; its agent comes back as it was.
const restoring = ref('')

async function restore(id: string) {
  restoring.value = id
  try {
    const a = await api.unhideAgent(id)
    if (a) toast.add({ title: 'Restored', description: a.name, icon: 'i-lucide-eye', color: 'success' })
    else toast.add({ title: 'Nothing to restore', description: `No agent has the ID ${id} any more; it is off the hidden list.`, icon: 'i-lucide-eye', color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Restore failed', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    // Reload after a failure too: a 404 usually means another admin restored
    // the agent first, and its stale entry should leave the Hidden list.
    await refresh()
    restoring.value = ''
  }
}

function signalBadge(a: AgentInfo): { label: string; title: string } {
  const s = a.signal ?? { kind: 'bell' as const }
  switch (s.kind) {
    case 'hook':
      return { label: 'hooks', title: a.adapter ? `Reports through hooks (${a.adapter} adapter)` : 'Reports through hooks' }
    case 'pattern':
      return { label: 'pattern', title: `Screen pattern: ${s.pattern ?? ''}` }
    case 'none':
      return { label: 'none', title: 'Never flagged as needing input' }
    default:
      return { label: 'bell / OSC', title: 'Terminal bell or notification escape' }
  }
}
</script>

<template>
  <UDashboardPanel id="agents">
    <template #header>
      <UDashboardNavbar title="Agents">
        <template #leading>
          <SidebarReveal />
        </template>
        <template #right>
          <UButton label="Add agent" icon="i-lucide-plus" color="neutral" variant="outline" @click="addAgent" />
          <UButton label="Launch agent" icon="i-lucide-play" @click="launch.show()" />
        </template>
      </UDashboardNavbar>
    </template>
    <template #body>
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" class="mb-4" />
      <p class="text-sm text-muted mb-4">
        Agents come from the server catalog: the built-in entries, the <code>catalog</code> section of the config file and whatever you add here, which the server keeps in its data directory. Commands are argv arrays; nothing goes through a shell.
      </p>
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <UCard v-for="a in agents" :key="a.id">
          <div class="flex items-start gap-3">
            <UIcon :name="agentIcon(a.icon)" class="size-6 text-primary flex-none mt-0.5" />
            <div class="min-w-0 flex-1">
              <div class="font-medium">{{ a.name }} <span class="text-xs text-muted font-mono">{{ a.id }}</span></div>
              <p v-if="a.description" class="text-sm text-muted">{{ a.description }}</p>
              <code class="block text-xs mt-2 truncate" :title="joinArgv(a.command)">{{ joinArgv(a.command) }}</code>
              <div class="mt-2 flex flex-wrap gap-2">
                <UBadge :label="signalBadge(a).label" :title="signalBadge(a).title" color="neutral" variant="subtle" size="sm" />
                <UBadge v-if="a.allowArgs" label="accepts args" color="neutral" variant="subtle" size="sm" />
                <UBadge v-if="a.cwd" :label="a.cwd" color="neutral" variant="subtle" size="sm" />
              </div>
              <div class="mt-3 -mb-1 flex justify-end gap-1">
                <UButton label="Edit" icon="i-lucide-pencil" size="xs" color="neutral" variant="ghost" :aria-label="`Edit ${a.name}`" @click="editAgent(a)" />
                <UButton
                  :label="removalText(removalOf(a), a.name, a.id).button"
                  :icon="removalText(removalOf(a), a.name, a.id).icon"
                  size="xs"
                  color="neutral"
                  variant="ghost"
                  :aria-label="`${removalText(removalOf(a), a.name, a.id).button} ${a.name}`"
                  @click="askHide(a)"
                />
              </div>
            </div>
          </div>
        </UCard>
      </div>
      <p v-if="!agents.length && !loading && !error" class="text-sm text-muted">No agents in the catalog.</p>

      <section v-if="hidden.length" class="mt-8" aria-labelledby="hidden-agents">
        <h2 id="hidden-agents" class="text-[11px] font-semibold uppercase tracking-wider text-muted">Hidden · {{ hidden.length }}</h2>
        <p class="mt-1 mb-3 text-sm text-muted">Not offered when launching. Restore brings an agent back as it was.</p>
        <ul class="flex flex-wrap gap-2">
          <li v-for="id in hidden" :key="id" class="flex items-center gap-1 rounded-md border border-default py-1 ps-3 pe-1">
            <span class="font-mono text-xs">{{ id }}</span>
            <UButton
              label="Restore"
              icon="i-lucide-eye"
              size="xs"
              color="neutral"
              variant="ghost"
              :loading="restoring === id"
              :disabled="!!restoring && restoring !== id"
              :aria-label="`Restore ${id}`"
              @click="restore(id)"
            />
          </li>
        </ul>
      </section>

      <AddAgentSlideover v-model:open="formOpen" :agent="editing" :taken-ids="agents.map((a) => a.id)" @saved="onSaved" />

      <UModal v-model:open="hideOpen" :title="hideText?.title ?? 'Hide agent?'" :description="hideText?.description">
        <template v-if="hideError" #body>
          <UAlert color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="hideError" />
        </template>
        <template #footer>
          <div class="flex w-full justify-end gap-2">
            <UButton label="Cancel" color="neutral" variant="ghost" @click="hideOpen = false" />
            <UButton :label="hideText?.button ?? 'Hide'" :icon="hideText?.icon" color="error" :loading="hiding" @click="confirmHide" />
          </div>
        </template>
      </UModal>
    </template>
  </UDashboardPanel>
</template>
