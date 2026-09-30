<script setup lang="ts">
import type { WebhookInfo } from '~/composables/useSessions'
import { EVENT_INFO, EVENT_TYPES, type EventType, type RouteRow, webhookHosts } from '~/utils/events'

const props = defineProps<{
  routes: Record<EventType, RouteRow>
  /** Webhooks configured in conductor.json, shown read-only: the server sends them the events they list. */
  webhooks?: WebhookInfo[]
}>()
const emit = defineEmits<{ update: [type: EventType, key: keyof RouteRow, value: boolean] }>()

const WEBHOOK_HINT = 'configure webhooks in conductor.json'
const WEBHOOK_COLUMN_HINT = 'What the server POSTs to the webhooks in conductor.json: set there, not here'

const columns: Array<{ key: keyof RouteRow; label: string; hint: string }> = [
  { key: 'badge', label: 'Badge', hint: 'A badge on the session in the sidebar and on its wall tile' },
  { key: 'browser', label: 'Browser', hint: 'A browser notification and the chime, as switched on under Alerts' },
  { key: 'wall', label: 'Wall jump', hint: 'The carousel jumps to the session while it follows routed events' },
  { key: 'feed', label: 'Feed', hint: 'A line in the live feed' },
]

/** Cells that are not a choice, and why: `working` clears a session's badge, it never sets one. */
function fixed(t: EventType, key: keyof RouteRow): string | null {
  return t === 'working' && key === 'badge' ? 'clears the badge' : null
}

/** Per event type, the hosts of the webhooks that get it: a checked cell names them. */
const webhookRows = computed(() => {
  const rows = {} as Record<EventType, { on: boolean; hint: string }>
  for (const t of EVENT_TYPES) {
    const hosts = webhookHosts(props.webhooks, t)
    rows[t] = { on: hosts.length > 0, hint: hosts.length ? `POSTed to ${hosts.join(', ')}` : WEBHOOK_HINT }
  }
  return rows
})
</script>

<template>
  <div class="overflow-x-auto rounded-md border border-default" data-routing-matrix>
    <table class="w-full min-w-[34rem] text-sm">
      <thead class="bg-elevated/50 text-xs text-muted">
        <tr>
          <th scope="col" class="px-4 py-2.5 text-left font-medium">Event</th>
          <th v-for="c in [...columns, { key: 'webhook', label: 'Webhook', hint: WEBHOOK_COLUMN_HINT }]" :key="c.key" scope="col" class="px-2 py-2.5 text-center font-medium whitespace-nowrap">
            <UPopover :content="{ side: 'top' }" arrow>
              <button type="button" class="cursor-help font-medium underline decoration-dotted underline-offset-4 hover:text-default" :data-hint="c.key">{{ c.label }}</button>
              <template #content>
                <p class="max-w-56 p-2.5 text-xs text-default">{{ c.hint }}</p>
              </template>
            </UPopover>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="t in EVENT_TYPES" :key="t" class="border-t border-default" :data-route="t">
          <th scope="row" class="px-4 py-2 text-left font-normal">
            <span class="block font-mono text-[12.5px] font-medium text-highlighted">{{ t }}</span>
            <span class="block text-xs text-muted">{{ EVENT_INFO[t].source }}</span>
          </th>
          <td v-for="c in columns" :key="c.key" class="px-2 py-2">
            <div class="flex justify-center">
              <UTooltip v-if="fixed(t, c.key)" :text="fixed(t, c.key)!">
                <span class="inline-flex" data-fixed-cell>
                  <UCheckbox :model-value="false" disabled class="pointer-events-none" :aria-label="`${t}: ${c.label} (${fixed(t, c.key)})`" :data-cell="`${t}.${c.key}`" />
                </span>
              </UTooltip>
              <UCheckbox v-else :model-value="routes[t][c.key]" :aria-label="`${t}: ${c.label}`" :data-cell="`${t}.${c.key}`" @update:model-value="(v) => emit('update', t, c.key, v === true)" />
            </div>
          </td>
          <td class="px-2 py-2">
            <div class="flex justify-center">
              <UTooltip :text="webhookRows[t].hint">
                <span class="inline-flex" data-webhook-cell>
                  <UCheckbox :model-value="webhookRows[t].on" disabled class="pointer-events-none" :aria-label="`${t}: Webhook (${webhookRows[t].hint})`" />
                </span>
              </UTooltip>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
