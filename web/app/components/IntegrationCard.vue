<script setup lang="ts">
import type { Integration } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { noInstallText } from '~/utils/integrations'
import { shortCwd } from '~/utils/sessions'

const props = defineProps<{
  integration: Integration
  /** The machine the server runs on: where an install writes. */
  host: string
  /**
   * Whether the server knows its user's home directory. Without one no
   * adapter lists a place to install into, and none can be installed.
   */
  homeKnown: boolean
}>()
/** An install ran: the list is stale, even after a failure (files written before it stopped stay written). */
const emit = defineEmits<{ changed: [] }>()

const api = useSessions()
const toast = useToast()
const installing = ref(false)

/** Conductor's hooks reach the agent, from its own config or through a launch from this server. */
const wired = computed(() => props.integration.installed || props.integration.launchInjection)
// Open for an agent nothing wires yet, and opened when an install leaves the rest to do by hand.
const snippetOpen = ref(!wired.value)

const status = computed<{ label: string; color: 'success' | 'neutral'; variant: 'subtle' | 'outline' }>(() => {
  const it = props.integration
  if (it.installed) {
    const n = it.events.length
    return { label: `${n} ${n === 1 ? 'hook' : 'hooks'} installed`, color: 'success', variant: 'subtle' }
  }
  if (it.launchInjection) return { label: 'injected at launch', color: 'success', variant: 'outline' }
  if (it.experimental) return { label: 'experimental', color: 'neutral', variant: 'outline' }
  return { label: `not configured on ${props.host}`, color: 'neutral', variant: 'subtle' }
})

/** Why there is no Install button: an agent with no file to install into, or a server without a home directory. */
const noInstall = computed(() => noInstallText(props.integration, props.homeKnown))

// Paths are long and unbroken: let toast text wrap anywhere.
const pathsUi = { description: 'whitespace-pre-line break-all' }
// A message is prose with a path in it: wrap between words, and inside a word only when it cannot fit a line.
const messageUi = { description: 'whitespace-pre-line [overflow-wrap:anywhere]' }

async function install() {
  const it = props.integration
  installing.value = true
  try {
    const changed = await api.installIntegration(it.id)
    if (changed.length) {
      toast.add({ title: `Installed ${it.name} hooks`, description: changed.map(shortCwd).join('\n'), icon: 'i-lucide-download', color: 'success', ui: pathsUi })
    } else {
      toast.add({ title: 'Nothing to change', description: `${it.name} already has Conductor's hooks on ${props.host}.`, icon: 'i-lucide-check', color: 'neutral' })
    }
  } catch (e) {
    // The server says what is left to do; the snippet on this card is how.
    const byHand = e instanceof ApiError && e.code === 'no_file_route'
    if (byHand) snippetOpen.value = true
    toast.add({
      title: byHand ? `${it.name}: finish by hand` : `Installing ${it.name} failed`,
      description: (e as Error).message,
      icon: 'i-lucide-triangle-alert',
      color: byHand ? 'warning' : 'error',
      ui: messageUi,
    })
  } finally {
    installing.value = false
    emit('changed')
  }
}
</script>

<template>
  <UCard :ui="{ body: 'flex flex-col gap-3' }" data-integration :data-id="integration.id">
    <div class="flex items-center gap-2.5 min-w-0">
      <SessionAvatar :agent-id="integration.id" size="md" :solid="wired" />
      <span class="truncate font-semibold text-highlighted">{{ integration.name }}</span>
      <UBadge :label="status.label" :color="status.color" :variant="status.variant" size="sm" class="ml-auto flex-none" data-status />
    </div>

    <ul v-if="integration.events.length" class="flex flex-wrap gap-1.5" :aria-label="`What ${integration.name} reports`">
      <li v-for="ev in integration.events" :key="ev">
        <UBadge :label="ev" :color="wired ? 'success' : 'neutral'" :variant="wired ? 'subtle' : 'outline'" size="sm" class="font-mono" />
      </li>
    </ul>

    <div class="flex min-w-0 items-center gap-2 text-xs text-muted">
      <code v-if="integration.where" class="truncate" :title="integration.where">{{ shortCwd(integration.where) }}</code>
      <span v-else data-no-install>{{ noInstall }}</span>
      <UButton
        v-if="integration.snippet"
        :label="snippetOpen ? 'Hide snippet' : 'Snippet'"
        :icon="snippetOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
        size="xs"
        color="neutral"
        variant="link"
        class="ml-auto flex-none"
        :aria-expanded="snippetOpen"
        @click="snippetOpen = !snippetOpen"
      />
    </div>

    <CodeBlock v-if="snippetOpen && integration.snippet" :text="integration.snippet" :name="integration.name" data-snippet />

    <div v-if="integration.where">
      <UButton
        label="Install on this machine"
        icon="i-lucide-download"
        size="sm"
        :color="integration.installed ? 'neutral' : 'primary'"
        :variant="integration.installed ? 'outline' : 'solid'"
        :loading="installing"
        @click="install"
      />
    </div>
  </UCard>
</template>
