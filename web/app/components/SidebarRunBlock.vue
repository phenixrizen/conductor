<script setup lang="ts">
import type { RunBlock, SessionRow } from '~/utils/sidebar'

/** A run as one block (design 3b): its header, then its members joined by a line, an exited member inside with Resume. The members' and the header's actions pass up to the list. */
withDefaults(defineProps<{ block: RunBlock; now: number; needsDot?: boolean; open?: boolean; busy?: Set<string> }>(), { needsDot: true, open: false, busy: () => new Set() })
const emit = defineEmits<{ shareRun: [block: RunBlock]; stopRun: [block: RunBlock]; share: [row: SessionRow]; stop: [row: SessionRow]; yard: [row: SessionRow]; openRun: [row: SessionRow]; answer: [row: SessionRow, index: number]; reply: [row: SessionRow, text: string] }>()
</script>

<template>
  <li class="flex flex-col gap-0.5" :data-sidebar-run-block="block.runId" :data-sidebar-row="`r:${block.runId}`" data-row-kind="run" :data-row-state="block.state">
    <SidebarRunHeader :block="block" :needs-dot="needsDot" :open="open" :busy="busy.has(`run:${block.runId}`)" @share="emit('shareRun', $event)" @stop="emit('stopRun', $event)" />
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
