<script setup lang="ts">
import type { Integration } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { shortCwd } from '~/utils/sessions'

const props = defineProps<{
  integration: Integration
  /** The machine the server runs on, as this browser reaches it: where an install writes. */
  host: string
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

const status = computed<{ label: string; color: 'success' | 'warning' | 'neutral'; variant: 'subtle' | 'outline' }>(() => {
  const it = props.integration
  if (it.installed) {
    const n = it.events.length
    return { label: `${n} ${n === 1 ? 'hook' : 'hooks'} installed`, color: 'success', variant: 'subtle' }
  }
  if (it.launchInjection) return { label: 'injected at launch', color: 'success', variant: 'outline' }
  if (it.experimental) return { label: 'experimental', color: 'neutral', variant: 'outline' }
  return { label: `not configured on ${props.host}`, color: 'warning', variant: 'subtle' }
})

async function install() {
  const it = props.integration
  installing.value = true
  try {
    const changed = await api.installIntegration(it.id)
    if (changed.length) {
      toast.add({ title: `Installed ${it.name} hooks`, description: changed.map(shortCwd).join('\n'), icon: 'i-lucide-download', color: 'success', ui: { description: 'whitespace-pre-line break-all' } })
    } else {
      toast.add({ title: 'Nothing to change', description: `${it.name} already has Conductor's hooks on ${props.host}.`, icon: 'i-lucide-check', color: 'neutral' })
    }
  } catch (e) {
    // The server says what is left to do; the snippet on this card is how.
    const byHand = e instanceof ApiError && e.code === 'no_file_route'
    if (byHand) snippetOpen.value = true
    toast.add({ title: byHand ? `${it.name}: finish by hand` : `Installing ${it.name} failed`, description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: byHand ? 'warning' : 'error' })
  } finally {
    installing.value = false
    emit('changed')
  }
}

async function copy() {
  try {
    await navigator.clipboard.writeText(props.integration.snippet)
    toast.add({ title: 'Snippet copied', description: props.integration.name, icon: 'i-lucide-clipboard-check', color: 'success' })
  } catch {
    toast.add({ title: 'Copy failed', description: 'Select the snippet and copy it by hand.', color: 'warning' })
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
      <span v-else>{{ integration.launchInjection ? 'Wired at launch by this server; anywhere else, paste the snippet.' : 'No file to install into: paste the snippet.' }}</span>
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

    <div v-if="snippetOpen && integration.snippet" class="relative rounded-md bg-forest-950 text-forest-100" data-snippet>
      <pre class="max-h-64 overflow-auto p-3 pe-20 font-mono text-xs leading-relaxed select-text">{{ integration.snippet }}</pre>
      <UButton label="Copy" icon="i-lucide-copy" size="xs" color="neutral" variant="ghost" class="absolute top-1.5 end-1.5 text-active-300 hover:bg-forest-900 hover:text-active-200" @click="copy" />
    </div>

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
