<script setup lang="ts">
import type { HistoryEntry } from '~/utils/dispatch'
import { relativeTime } from '~/utils/sessions'

/**
 * A ticket's history as the store recorded it, field by field (GET /api/dispatch/tickets/{key}/history): one entry per operation, oldest
 * first, who made it and when, then each field it set, added or removed in words (utils/dispatch.ts, historyEntries). More reads the
 * next page.
 */
defineProps<{ entries: HistoryEntry[]; now: number; more: boolean; loading?: boolean }>()
const emit = defineEmits<{ more: [] }>()
</script>

<template>
  <section class="flex flex-col gap-2" data-ticket-history>
    <h3 class="text-xs font-semibold uppercase tracking-wide text-muted">History</h3>
    <ol v-if="entries.length" class="flex flex-col">
      <li v-for="e in entries" :key="e.key" class="relative border-l border-default pb-3 pl-4 last:pb-0" data-history-entry>
        <span class="absolute top-1.5 -left-[3.5px] size-1.5 rounded-full bg-neutral-400" aria-hidden="true" />
        <p class="text-xs text-muted">
          <span class="font-medium text-default" data-history-who>{{ e.who }}</span>
          <span class="mx-1">·</span>
          <time :datetime="e.at" :title="new Date(e.at).toLocaleString()">{{ relativeTime(e.at, now, { suffix: true }) }}</time>
        </p>
        <ul class="mt-0.5 flex flex-col gap-0.5 text-[13px] text-default">
          <li v-for="(line, i) in e.lines" :key="i" class="break-words" data-history-line>{{ line }}</li>
        </ul>
      </li>
    </ol>
    <p v-else class="text-sm text-dimmed">{{ loading ? 'Reading the history…' : 'Nothing recorded yet.' }}</p>
    <UButton v-if="more" label="More history" icon="i-lucide-chevrons-down" color="neutral" variant="ghost" size="xs" class="self-start" :loading="loading" data-history-more @click="emit('more')" />
  </section>
</template>
