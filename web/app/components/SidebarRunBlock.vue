<script setup lang="ts">
import type { RunBlock } from '~/utils/sidebar'

/** A run as one block (design 3b): its header, then its members joined by a line, an exited member inside with Resume. */
withDefaults(defineProps<{ block: RunBlock; now: number; needsDot?: boolean; open?: boolean }>(), { needsDot: true, open: false })
</script>

<template>
  <li class="flex flex-col gap-0.5" :data-sidebar-run-block="block.runId" :data-sidebar-row="`r:${block.runId}`" data-row-kind="run" :data-row-state="block.state">
    <SidebarRunHeader :block="block" :needs-dot="needsDot" :open="open" />
    <!-- The line joining the members is neutral, never a status colour: the state is each row's own. -->
    <ol class="ml-4 flex flex-col gap-0.5 border-l border-default pl-1">
      <SidebarSessionRow v-for="m in block.members" :key="m.id" :row="m" :now="now" member :needs-dot="needsDot" />
    </ol>
  </li>
</template>
