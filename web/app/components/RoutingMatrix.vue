<script setup lang="ts">
import { EVENT_INFO, EVENT_TYPES, type EventType, type RouteRow } from '~/utils/events'

const props = defineProps<{
  routes: Record<EventType, RouteRow>
  /** Webhooks configured in conductor.json, shown read-only: the server sends them the events they list. */
  webhooks?: { url: string; events: string[] }[]
}>()
const emit = defineEmits<{ update: [type: EventType, key: keyof RouteRow, value: boolean] }>()

const columns: Array<{ key: keyof RouteRow; label: string; hint: string }> = [
  { key: 'badge', label: 'Badge', hint: 'A badge on the session in the sidebar and on its wall tile' },
  { key: 'browser', label: 'Browser', hint: 'A browser notification and the chime, as switched on under Alerts' },
  { key: 'wall', label: 'Wall jump', hint: 'The carousel jumps to the session while Follow is on' },
  { key: 'feed', label: 'Feed', hint: 'A line in the live feed' },
]

function webhooked(t: EventType): boolean {
  return !!props.webhooks?.some((w) => w.events.includes(t))
}
</script>

<template>
  <div class="overflow-x-auto rounded-md border border-default" data-routing-matrix>
    <table class="w-full min-w-[34rem] text-sm">
      <thead class="bg-elevated/50 text-xs text-muted">
        <tr>
          <th scope="col" class="px-4 py-2.5 text-left font-medium">Event</th>
          <th v-for="c in columns" :key="c.key" scope="col" class="px-2 py-2.5 text-center font-medium whitespace-nowrap">
            <span :title="c.hint">{{ c.label }}</span>
          </th>
          <th scope="col" class="px-2 py-2.5 text-center font-medium">Webhook</th>
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
              <UCheckbox :model-value="routes[t][c.key]" :aria-label="`${t}: ${c.label}`" :data-cell="`${t}.${c.key}`" @update:model-value="(v) => emit('update', t, c.key, v === true)" />
            </div>
          </td>
          <td class="px-2 py-2">
            <div class="flex justify-center">
              <UTooltip text="configure webhooks in conductor.json">
                <span class="inline-flex" data-webhook-cell>
                  <UCheckbox :model-value="webhooked(t)" disabled class="pointer-events-none" :aria-label="`${t}: Webhook (configure webhooks in conductor.json)`" />
                </span>
              </UTooltip>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
