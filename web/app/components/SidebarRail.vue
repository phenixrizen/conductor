<script setup lang="ts">
import { railGroups } from '~/utils/sidebar'

/**
 * The sidebar as a rail: Launch, a search button that opens the full
 * sidebar on its filter, and every session as its agent's avatar with the
 * attention dot, the members of a run together under its name. The mark,
 * the pages, the utility buttons and the expand button are the layout's
 * header and footer.
 */
const props = defineProps<{ runId?: string; runName?: string }>()
const emit = defineEmits<{ search: [] }>()

const attention = useAttention()
const events = useEvents()
const launch = useLaunchModal()
const route = useRoute()
const runNames = useState<Record<string, string>>('crewRunNames', () => ({}))

const shown = computed(() => (props.runId ? attention.sessions.value.filter((s) => s.crew?.runId === props.runId) : attention.sessions.value))
const names = computed(() => (props.runId && props.runName ? { ...runNames.value, [props.runId]: props.runName } : runNames.value))
const groups = computed(() => railGroups(shown.value, names.value))
/** The amber dot follows the Events page's Badge route for needs_input, as in the full sidebar. */
const needsDot = computed(() => events.routes.value.needs_input.badge)

function active(id: string) {
  return route.path === `/sessions/${id}`
}
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
        <span v-if="g.label" class="w-full truncate px-0.5 text-center text-[9px] font-semibold uppercase tracking-wider text-muted" :title="g.label">{{ g.label }}</span>
        <UTooltip v-for="it in g.items" :key="it.id" :text="it.message ? `${it.name} · ${it.message}` : it.name" :content="{ side: 'right' }">
          <NuxtLink
            :to="`/sessions/${it.id}`"
            class="relative grid place-items-center rounded-md p-0.5 transition-colors"
            :class="active(it.id) ? 'bg-default ring-1 ring-default shadow-xs' : 'hover:bg-elevated/60'"
            :aria-label="it.name"
            :aria-current="active(it.id) ? 'page' : undefined"
            data-rail-session
          >
            <SessionAvatar :agent-id="it.agentId" :solid="it.dot === 'needs'" :dashed="it.dot === 'exited'" />
            <span v-if="it.dot === 'needs' && needsDot" class="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-warning ring-2 ring-default" aria-hidden="true" />
            <span v-else-if="it.dot === 'running'" class="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-success ring-2 ring-default" aria-hidden="true" />
          </NuxtLink>
        </UTooltip>
      </template>
    </div>
  </div>
</template>
