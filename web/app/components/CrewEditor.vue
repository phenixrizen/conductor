<script setup lang="ts">
import type { AgentInfo, SessionInfo } from '~/composables/useSessions'
import { draftMember, memberNameError, memberNameFrom, type DraftCrew } from '~/utils/crews'
import { isActive } from '~/utils/attention'
import { shortCwd } from '~/utils/sessions'

/**
 * One crew as a form: its name, goal, working directory, where it runs,
 * isolation, what happens after launch and its members. The page saves,
 * launches, duplicates and deletes it; the editor only changes the draft.
 */
const crew = defineModel<DraftCrew>({ required: true })
const props = defineProps<{ agents: AgentInfo[]; dirty: boolean; saving?: boolean; launching?: boolean }>()
const emit = defineEmits<{ save: []; discard: []; duplicate: []; launch: []; delete: [] }>()

const live = useAttention()

/** The view link a launch creates lasts 8 hours when the switch is on. */
const VIEW_LINK_TTL = 8 * 3600

const nameMissing = computed(() => !crew.value.name.trim())
const memberProblems = computed(() => crew.value.members.some((m, i) => memberNameError(m.name, crew.value.members.filter((_, j) => j !== i).map((x) => x.name)) || !m.agentId))
const invalid = computed(() => nameMissing.value || memberProblems.value)
const launchLabel = computed(() => `Launch ${crew.value.members.length} ${crew.value.members.length === 1 ? 'agent' : 'agents'}`)
const launchBlocked = computed(() => {
  if (!crew.value.members.length) return 'Add a member first'
  if (crew.value.where !== 'server') return 'Only a crew that runs on the server can be launched'
  if (invalid.value) return 'Fix the fields marked in red first'
  return ''
})
const subtitle = computed(() => `${crew.value.id ? `crews/${crew.value.id}.json` : 'not saved yet'} · ${crew.value.members.length} ${crew.value.members.length === 1 ? 'agent' : 'agents'}`)

const isolationItems = [
  { label: 'Git worktree per agent', value: 'worktree' },
  { label: 'Shared working directory', value: 'none' },
]

const viewLink = computed({
  get: () => !!crew.value.viewLinkTtlSeconds,
  set: (on: boolean) => (crew.value = { ...crew.value, viewLinkTtlSeconds: on ? VIEW_LINK_TTL : undefined }),
})
const viewLinkLabel = computed(() => {
  const ttl = crew.value.viewLinkTtlSeconds
  const hours = ttl && ttl !== VIEW_LINK_TTL ? `${Math.round((ttl / 3600) * 10) / 10}h` : '8h'
  return `Create a view link (${hours})`
})

function set<K extends keyof DraftCrew>(key: K, value: DraftCrew[K]) {
  crew.value = { ...crew.value, [key]: value }
}

const taken = computed(() => crew.value.members.map((m) => m.name))

function addMember() {
  const agent = props.agents[0]
  if (!agent) return
  set('members', [...crew.value.members, draftMember({ name: memberNameFrom(agent.id, taken.value), agentId: agent.id, prompt: '', start: { when: 'immediately' } })])
}

// "Add from a running session": its agent becomes a new member, and its
// working directory the crew's when the crew has none yet.
const pickOpen = ref(false)
const running = computed(() => live.sessions.value.filter((s) => s.kind === 'server' && isActive(s)))

function addFrom(s: SessionInfo) {
  const next = { ...crew.value, members: [...crew.value.members, draftMember({ name: memberNameFrom(s.crew?.member || s.name, taken.value), agentId: s.agentId, prompt: '', start: { when: 'immediately' } })] }
  if (!next.cwd.trim()) next.cwd = s.cwd
  crew.value = next
  pickOpen.value = false
}

// Runs on: the arrow keys move the focus between the two options, and
// choose Server on the way; My machine takes the focus (its tooltip says why)
// but cannot be chosen.
const whereGroup = useTemplateRef<HTMLElement>('whereGroup')
function onWhereKey(e: KeyboardEvent) {
  const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key]
  if (!step || !whereGroup.value) return
  e.preventDefault()
  const options = [...whereGroup.value.querySelectorAll<HTMLElement>('[role="radio"]')]
  const at = options.indexOf(document.activeElement as HTMLElement)
  const next = options[(at + step + options.length) % options.length]
  next?.focus()
  if (next && next.getAttribute('aria-disabled') !== 'true') next.click()
}

const menu = computed(() => [[{ label: 'Delete crew', icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => emit('delete') }]])
</script>

<template>
  <div class="flex min-w-0 flex-col gap-5" data-crew-editor>
    <div class="flex flex-wrap items-start gap-3">
      <div class="flex min-w-60 flex-1 flex-col gap-0.5">
        <UInput
          :model-value="crew.name"
          variant="ghost"
          maxlength="60"
          placeholder="Crew name"
          aria-label="Crew name"
          :color="nameMissing ? 'error' : undefined"
          :highlight="nameMissing"
          :ui="{ base: 'px-1 -mx-1 text-lg md:text-lg font-semibold text-highlighted' }"
          class="max-w-md"
          @update:model-value="set('name', String($event))"
        />
        <span class="font-mono text-xs text-muted">{{ subtitle }}</span>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <template v-if="dirty">
          <UButton v-if="crew.id" label="Discard" color="neutral" variant="ghost" :disabled="saving" @click="emit('discard')" />
          <UButton label="Save" icon="i-lucide-save" color="neutral" variant="outline" :loading="saving" :disabled="invalid || launching" @click="emit('save')" />
        </template>
        <UButton v-if="crew.id" label="Duplicate" icon="i-lucide-copy" color="neutral" variant="outline" :disabled="saving || launching" @click="emit('duplicate')" />
        <UTooltip :text="launchBlocked" :disabled="!launchBlocked">
          <UButton :label="launchLabel" icon="i-lucide-play" :loading="launching" :disabled="!!launchBlocked || saving" data-launch @click="emit('launch')" />
        </UTooltip>
        <UDropdownMenu v-if="crew.id" :items="menu" :content="{ align: 'end' }">
          <UButton icon="i-lucide-ellipsis" color="neutral" variant="ghost" aria-label="More" />
        </UDropdownMenu>
      </div>
    </div>

    <div class="grid gap-4 lg:grid-cols-[1.4fr_1fr_1fr]">
      <UFormField label="Goal" hint="shared as $GOAL" name="goal">
        <UTextarea :model-value="crew.goal" :rows="3" autoresize :maxrows="8" maxlength="2000" placeholder="What the crew is for. Role prompts reach it as $GOAL." class="w-full" @update:model-value="set('goal', String($event))" />
      </UFormField>

      <div class="flex flex-col gap-2">
        <UFormField label="Working directory" hint="allowed root" name="cwd">
          <UInput :model-value="crew.cwd" placeholder="server default" autocapitalize="off" spellcheck="false" :ui="{ base: 'font-mono' }" class="w-full" @update:model-value="set('cwd', String($event))" />
        </UFormField>
        <!-- One tab stop (the option chosen); the arrow keys move between the options. -->
        <div ref="whereGroup" class="grid grid-cols-2 rounded-md bg-elevated p-0.5 text-sm" role="radiogroup" aria-label="Runs on" @keydown="onWhereKey">
          <button
            type="button"
            role="radio"
            :aria-checked="crew.where === 'server'"
            :tabindex="crew.where === 'host' ? -1 : 0"
            class="rounded py-1.5 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-primary"
            :class="crew.where === 'server' ? 'bg-default font-semibold shadow-xs ring-1 ring-default' : 'text-muted'"
            @click="set('where', 'server')"
          >
            Server
          </button>
          <UTooltip text="hosted crews come later">
            <button
              type="button"
              role="radio"
              :aria-checked="crew.where === 'host'"
              aria-disabled="true"
              :tabindex="crew.where === 'host' ? 0 : -1"
              class="rounded py-1.5 cursor-not-allowed outline-none focus-visible:ring-2 focus-visible:ring-primary"
              :class="crew.where === 'host' ? 'bg-default font-semibold shadow-xs ring-1 ring-default' : 'text-dimmed'"
            >
              My machine
            </button>
          </UTooltip>
        </div>
      </div>

      <div class="flex flex-col gap-2.5">
        <span class="text-sm font-medium text-default">Isolation &amp; after launch</span>
        <USelect :model-value="crew.isolation" :items="isolationItems" aria-label="Isolation" class="w-full" @update:model-value="set('isolation', $event as DraftCrew['isolation'])" />
        <USwitch :model-value="crew.openAfterLaunch" label="Open the crew view after launch" size="sm" @update:model-value="set('openAfterLaunch', $event)" />
        <USwitch v-model="viewLink" :label="viewLinkLabel" size="sm" />
      </div>
    </div>

    <p v-if="crew.isolation === 'worktree'" class="-mt-2 text-xs text-muted">
      Each agent works in <code>.conductor/worktrees/&lt;run&gt;/&lt;member&gt;</code> of the repository, on its own branch <code>crew/&lt;run&gt;/&lt;member&gt;</code>. The working directory must be in a git repository with a commit.
    </p>

    <CrewMembersTable :model-value="crew.members" :agents="agents" @update:model-value="set('members', $event)" @add="addMember" @add-from-session="pickOpen = true" />

    <UModal v-model:open="pickOpen" title="Add from a running session" description="Its agent becomes a new member, and its working directory the crew's when the crew has none yet.">
      <template #body>
        <p v-if="!running.length" class="text-sm text-muted">No session is running on the server.</p>
        <ul v-else class="flex flex-col gap-1">
          <li v-for="s in running" :key="s.id">
            <button type="button" class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors hover:bg-elevated/60" @click="addFrom(s)">
              <SessionAvatar :agent-id="s.agentId" />
              <span class="min-w-0 flex-1 flex flex-col">
                <span class="truncate text-sm font-medium">{{ s.name }}</span>
                <span class="truncate font-mono text-[11px] text-muted">{{ s.agentId }} · {{ shortCwd(s.cwd) }}</span>
              </span>
              <UIcon name="i-lucide-plus" class="size-4 text-muted" />
            </button>
          </li>
        </ul>
      </template>
    </UModal>
  </div>
</template>
