<script setup lang="ts">
import type { FeedEntry } from '~/composables/useEvents'
import { COLOR_TEXT, EVENT_INFO, eventDetail, linkableUrl } from '~/utils/events'

const props = defineProps<{ entries: FeedEntry[] }>()

// Newest first. Text only: nothing an agent sends is rendered as HTML, and a
// URL is a link only when linkableUrl allows it.
const lines = computed(() =>
  [...props.entries].reverse().map((e) => ({ e, detail: eventDetail(e), link: linkableUrl(e.url), color: COLOR_TEXT[EVENT_INFO[e.event].color] })),
)

function time(at: string) {
  return new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}
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
      <li v-for="{ e, detail, link, color } in lines" :key="e.seq" class="flex gap-3 whitespace-nowrap" :data-event="e.event">
        <span class="flex-none text-dimmed">{{ time(e.at) }}</span>
        <span class="w-24 flex-none truncate font-medium" :class="color">{{ e.event }}</span>
        <span class="min-w-0 truncate" :title="[e.sessionName, detail, e.url].filter(Boolean).join(' · ')">
          <NuxtLink :to="`/sessions/${e.sessionId}`" class="text-highlighted hover:underline">{{ e.sessionName }}</NuxtLink>
          <template v-if="detail"> · {{ detail }}</template>
          <template v-if="e.url">
            ·
            <a v-if="link" :href="link" target="_blank" rel="noopener noreferrer" class="text-primary underline underline-offset-2" data-event-link>{{ e.url }}</a>
            <span v-else>{{ e.url }}</span>
          </template>
        </span>
      </li>
    </ol>
  </section>
</template>
