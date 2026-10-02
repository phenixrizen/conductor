<script setup lang="ts">
import { needsDotShown, railGroups, runOpen, sessionOpen, sidebarSessions } from '~/utils/sidebar'

/**
 * The sidebar as a rail: Launch, a search button that opens the full
 * sidebar on its filter, and every session as its agent's avatar with the
 * attention dot, the members of a run together under its name, which links
 * to the crew view as the full sidebar's run header does. The mark, the
 * pages, the utility buttons and the expand button are the layout's header
 * and footer.
 */
const props = defineProps<{ runId?: string; runName?: string }>()
const emit = defineEmits<{ search: [] }>()

const attention = useAttention()
const events = useEvents()
const launch = useLaunchModal()
const route = useRoute()

const shown = computed(() => sidebarSessions(attention.sessions.value, props.runId))
const names = computed(() => (props.runId && props.runName ? { ...attention.runNames.value, [props.runId]: props.runName } : attention.runNames.value))
const groups = computed(() => railGroups(shown.value, names.value))
const needsDot = computed(() => needsDotShown(events.routes.value))
</script>

<template>
  <div class="flex h-full min-h-0 flex-col items-center gap-2" data-rail>
    <UTooltip text="Launch agent" :kbds="['N']" :content="{ side: 'right' }">
      <UButton icon="i-lucide-plus" size="sm" aria-label="Launch agent" @click="launch.show()" />
    </UTooltip>
    <UTooltip text="Filter sessions" :kbds="['/']" :content="{ side: 'right' }">
      <UButton icon="i-lucide-search" color="neutral" variant="ghost" size="sm" aria-label="Filter sessions" @click="emit('search')" />
    </UTooltip>
    <div class="flex min-h-0 w-full flex-1 flex-col items-center gap-1 overflow-y-auto" data-rail-sessions>
      <template v-for="g in groups" :key="g.key">
        <UTooltip v-if="g.runId" :text="`Crew · ${g.label ?? g.runId}`" :content="{ side: 'right' }">
          <NuxtLink
            :to="`/runs/${encodeURIComponent(g.runId)}`"
            class="block w-full truncate rounded-sm px-0.5 text-center text-[11px] font-semibold"
            :class="runOpen(route.path, g.runId) ? 'text-highlighted' : 'text-muted hover:text-highlighted'"
            :aria-label="`Crew ${g.label ?? g.runId}`"
            :aria-current="runOpen(route.path, g.runId) ? 'page' : undefined"
            data-rail-run
          >{{ g.label ?? g.runId }}</NuxtLink>
        </UTooltip>
        <UTooltip v-for="it in g.items" :key="it.id" :text="it.message ? `${it.name} · ${it.message}` : it.name" :content="{ side: 'right' }">
          <NuxtLink
            :to="`/sessions/${it.id}`"
            class="relative grid place-items-center rounded-md p-0.5 transition-colors"
            :class="sessionOpen(route.path, it.id) ? 'bg-default ring-1 ring-default shadow-xs' : 'hover:bg-elevated/60'"
            :aria-label="it.name"
            :aria-current="sessionOpen(route.path, it.id) ? 'page' : undefined"
            data-rail-session
          >
            <SessionAvatar :agent-id="it.agentId" :solid="it.dot === 'needs'" :dashed="it.dot === 'exited'" />
            <span v-if="it.dot === 'needs' && needsDot" class="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-warning ring-2 ring-default" aria-hidden="true" />
            <span v-else-if="it.dot === 'running'" class="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-success ring-2 ring-default" aria-hidden="true" />
            <span v-else-if="it.dot === 'idle'" class="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-neutral-400 ring-2 ring-default" aria-hidden="true" />
          </NuxtLink>
        </UTooltip>
      </template>
    </div>
  </div>
</template>
