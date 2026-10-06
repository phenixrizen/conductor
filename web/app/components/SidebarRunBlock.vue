<script setup lang="ts">
import type { RunBlock, SessionRow } from '~/utils/sidebar'

/** A run as one block (design 3b): its header, then its members joined by a line, an exited member inside with Resume. The members' and the header's actions pass up to the list. */
withDefaults(defineProps<{ block: RunBlock; now: number; needsDot?: boolean; open?: boolean; busy?: Set<string>; focusedId?: string | null; confirmingId?: string | null }>(), { needsDot: true, open: false, busy: () => new Set(), focusedId: null, confirmingId: null })
const emit = defineEmits<{ shareRun: [block: RunBlock]; stopRun: [block: RunBlock]; share: [row: SessionRow]; stop: [row: SessionRow]; yard: [row: SessionRow]; openRun: [row: SessionRow]; answer: [row: SessionRow, index: number]; reply: [row: SessionRow, text: string]; confirm: [id: string | null] }>()
</script>

<template>
  <li class="flex flex-col gap-0.5" :data-sidebar-run-block="block.runId" :data-sidebar-row="`r:${block.runId}`" data-row-kind="run" :data-row-state="block.state">
    <SidebarRunHeader
      :block="block"
      :needs-dot="needsDot"
      :open="open"
      :busy="busy.has(`run:${block.runId}`)"
      :focused="focusedId === `r:${block.runId}`"
      :confirming="confirmingId === `r:${block.runId}`"
      @update:confirming="emit('confirm', $event ? `r:${block.runId}` : null)"
      @share="emit('shareRun', $event)"
      @stop="emit('stopRun', $event)"
    />
    <!-- The line joining the members is neutral, never a status colour: the state is each row's own. -->
    <ol class="ml-4 flex flex-col gap-0.5 border-l border-default pl-1">
      <SidebarSessionRow
        v-for="m in block.members"
        :key="m.id"
        :row="m"
        :now="now"
        member
        :needs-dot="needsDot"
        :busy="busy.has(m.id)"
        :focused="focusedId === `s:${m.id}`"
        :confirming="confirmingId === `s:${m.id}`"
        @update:confirming="emit('confirm', $event ? `s:${m.id}` : null)"
        @share="emit('share', $event)"
        @stop="emit('stop', $event)"
        @yard="emit('yard', $event)"
        @open-run="emit('openRun', $event)"
        @answer="(row, i) => emit('answer', row, i)"
        @reply="(row, text) => emit('reply', row, text)"
      />
    </ol>
  </li>
</template>
