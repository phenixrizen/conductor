<script setup lang="ts">
import { COLOR_TEXT, EVENT_INFO, type FeedEntry } from '~/utils/events'

const props = defineProps<{ entries: FeedEntry[] }>()

// Newest first. Each line's time, detail and link were worked out once, when
// it arrived (routeEntry). Text only: nothing an agent sends is rendered as
// HTML, and `link` is set only for a URL linkableUrl allows.
const lines = computed(() => [...props.entries].reverse())
</script>

<template>
  <section class="flex flex-col rounded-md border border-default" aria-labelledby="event-feed-title" data-event-feed>
    <div class="flex items-center gap-2 border-b border-default px-4 py-2">
      <h3 id="event-feed-title" class="text-[11px] font-semibold uppercase tracking-wider text-muted">Live</h3>
      <span v-if="entries.length" class="font-mono text-[11px] text-muted">{{ entries.length }}</span>
      <code class="ml-auto truncate font-mono text-[11px] text-muted">POST /api/sessions/:id/events</code>
    </div>
    <p v-if="!lines.length" class="px-4 py-6 text-sm text-muted">Nothing yet. Events show up here as agents report them.</p>
    <ol v-else class="h-96 overflow-y-auto px-4 py-2 font-mono text-xs leading-6">
      <li v-for="e in lines" :key="e.seq" class="flex gap-3 whitespace-nowrap" :data-event="e.event">
        <span class="flex-none text-dimmed">{{ e.time }}</span>
        <span class="w-24 flex-none truncate font-medium" :class="COLOR_TEXT[EVENT_INFO[e.event].color]">{{ e.event }}</span>
        <span class="min-w-0 truncate" :title="[e.sessionName, e.detail, e.url].filter(Boolean).join(' · ')">
          <NuxtLink :to="`/sessions/${e.sessionId}`" class="text-highlighted hover:underline">{{ e.sessionName }}</NuxtLink>
          <template v-if="e.detail"> · {{ e.detail }}</template>
          <template v-if="e.url">
            ·
            <a v-if="e.link" :href="e.link" target="_blank" rel="noopener noreferrer" class="text-primary underline underline-offset-2" data-event-link>{{ e.url }}</a>
            <span v-else>{{ e.url }}</span>
          </template>
        </span>
      </li>
    </ol>
  </section>
</template>
