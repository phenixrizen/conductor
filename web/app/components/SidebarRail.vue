<script setup lang="ts">
import { joinedOpen, joinedPath } from '~/utils/joined'
import { needsDotShown, railGroups, runOpen, sessionOpen, sidebarModel } from '~/utils/sidebar'

/**
 * The sidebar as a rail: Launch, a search button that opens the full
 * sidebar on its filter, and every session as its agent's avatar with the
 * attention dot, the members of a run together under its name, which links
 * to the run page as the full sidebar's run header does. The order is the
 * list's (sidebarModel): needs you, running, shared, exited. The mark, the
 * pages, the utility buttons and the expand button are the layout's header
 * and footer.
 */
const emit = defineEmits<{ search: [] }>()

const attention = useAttention()
const events = useEvents()
const launch = useLaunchModal()
const route = useRoute()

const groups = computed(() => railGroups(sidebarModel(attention.sessions.value, attention.runOf)))
const needsDot = computed(() => needsDotShown(events.routes.value))
const joined = useJoined()
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
        <UTooltip v-if="g.runId" :text="`Run · ${g.label ?? g.runId}`" :content="{ side: 'right' }">
          <NuxtLink
            :to="`/runs/${encodeURIComponent(g.runId)}`"
            class="block w-full truncate rounded-sm px-0.5 text-center text-[11px] font-semibold"
            :class="runOpen(route.path, g.runId) ? 'text-highlighted' : 'text-muted hover:text-highlighted'"
            :aria-label="`Run ${g.label ?? g.runId}`"
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
      <!-- Shared with you comes after your own sessions, as in the list. -->
      <template v-if="joined.list.value.length">
        <UTooltip text="Shared with you" :content="{ side: 'right' }">
          <span class="grid w-full place-items-center text-muted" aria-label="Shared with you" data-rail-shared><UIcon name="i-lucide-globe" class="size-3.5" /></span>
        </UTooltip>
        <UTooltip v-for="e in joined.list.value" :key="e.id" :text="`${e.name} · ${e.role === 'control' ? 'control' : 'view only'} · through ${e.host}`" :content="{ side: 'right' }">
          <NuxtLink
            :to="joinedPath(e)"
            class="grid place-items-center rounded-md p-0.5 transition-colors"
            :class="joinedOpen(route.path, e.token) ? 'bg-default ring-1 ring-default shadow-xs' : 'hover:bg-elevated/60'"
            :aria-label="`${e.name}, shared with you`"
            :aria-current="joinedOpen(route.path, e.token) ? 'page' : undefined"
            :data-rail-shared-entry="e.id"
          >
            <SessionAvatar v-if="e.kind === 'session'" :agent-id="e.agentId ?? ''" :dashed="e.lastStatus === 'revoked' || e.lastStatus === 'gone'" />
            <span v-else class="grid size-6 place-items-center rounded-md bg-elevated text-primary"><UIcon name="i-lucide-users" class="size-3.5" /></span>
          </NuxtLink>
        </UTooltip>
      </template>
    </div>
  </div>
</template>
