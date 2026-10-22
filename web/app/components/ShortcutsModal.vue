<script setup lang="ts">
const shortcuts = useShortcutsModal()
const desktop = useDesktop()
const systemName = computed(() => ({ mac: 'macOS', windows: 'Windows', linux: 'Linux' })[shortcuts.system.value])
const surfaceName = computed(() => (desktop.isDesktop.value ? 'the desktop app' : 'a browser'))
/** Every row's columns: the action, then the two key columns at one fixed width each, so they line up down the table. */
const COLUMNS = 'grid grid-cols-1 sm:grid-cols-[minmax(0,1fr)_10.5rem_10.5rem] sm:gap-x-0'
</script>

<template>
  <!-- One column template for every row (round 14: each row sized its own key columns, so nothing lined up and the owner could not
       tell the columns apart): the action, then two tinted columns of fixed width for the keys; each group repeats the column names.
       On a phone a row stacks: the action, then its two key sets, each named. -->
  <UModal
    v-model:open="shortcuts.open.value"
    title="Keyboard shortcuts"
    description="Three columns: the action, its keys while typing into an agent (the terminal hands these Alt chords back to the page), and its keys when no terminal or text field has the focus. Every action is also a button."
    :ui="{ content: 'sm:max-w-3xl', header: 'pe-14', description: 'text-pretty' }"
  >
    <template #body>
      <div class="flex flex-col gap-6" data-shortcuts>
        <div :class="[COLUMNS, 'hidden text-[11px] font-semibold uppercase tracking-wide text-muted sm:grid']" data-shortcuts-columns>
          <span>Action</span>
          <span class="px-3 text-center">In a terminal</span>
          <span class="px-3 text-center">Outside</span>
        </div>
        <section v-for="(group, gi) in shortcuts.groups.value" :key="group.title" :data-shortcuts-group="group.title">
          <div :class="[COLUMNS, 'mb-1 items-end border-b border-default pb-1']">
            <h3 class="text-xs font-semibold uppercase tracking-wide text-highlighted">{{ group.title }}</h3>
            <template v-if="gi > 0">
              <span class="hidden px-3 text-center text-[10px] font-medium uppercase tracking-wide text-muted sm:block" data-shortcuts-group-columns>In a terminal</span>
              <span class="hidden px-3 text-center text-[10px] font-medium uppercase tracking-wide text-muted sm:block">Outside</span>
            </template>
          </div>
          <ul class="divide-y divide-default">
            <li v-for="row in group.rows" :key="row.label" :class="[COLUMNS, 'items-center gap-y-1 py-1.5 text-sm']" :data-shortcut="row.label">
              <span class="text-pretty">{{ row.label }}</span>
              <span class="flex items-center gap-1 sm:-my-1.5 sm:h-full sm:justify-center sm:bg-elevated/60 sm:px-3 sm:py-1.5">
                <span class="me-1 w-16 text-[11px] text-muted sm:hidden">Terminal</span>
                <span class="flex items-center gap-1" data-shortcut-terminal>
                  <template v-if="row.terminal">
                    <UKbd v-for="(key, i) in row.terminal" :key="i" :value="key" size="md" />
                  </template>
                  <span v-else class="text-xs text-muted" aria-label="none">·</span>
                </span>
              </span>
              <span class="flex items-center gap-1 sm:-my-1.5 sm:h-full sm:justify-center sm:bg-elevated/30 sm:px-3 sm:py-1.5">
                <span class="me-1 w-16 text-[11px] text-muted sm:hidden">Outside</span>
                <span class="flex items-center gap-1" data-shortcut-outside>
                  <span v-if="!row.keys.length" class="text-xs text-muted" aria-label="none">·</span>
                  <template v-for="(key, i) in row.keys" :key="i">
                    <span v-if="i > 0 && row.keys[0] === 'G' && group.title === 'Everywhere'" class="text-xs text-muted">then</span>
                    <UKbd :value="key" size="md" />
                  </template>
                </span>
              </span>
            </li>
          </ul>
        </section>
        <!-- What the system the page runs on takes before the app sees it (#312), and the chord that stands in. -->
        <section v-if="shortcuts.notes.value.length" data-shortcuts-notes :data-shortcuts-system="shortcuts.system.value">
          <h3 class="mb-1 border-b border-default pb-1 text-xs font-semibold uppercase tracking-wide text-highlighted">On {{ systemName }}, in {{ surfaceName }}</h3>
          <ul class="flex flex-col gap-1 text-sm">
            <li v-for="n in shortcuts.notes.value" :key="`${n.chord}:${n.browser ?? ''}`" class="text-pretty" :data-shortcut-note="n.chord"><b class="font-medium">{{ n.chord }}</b><span v-if="n.browser" class="text-muted"> ({{ n.browser === 'firefox' ? 'Firefox' : n.browser }})</span>: <span class="text-muted">{{ n.note }}</span></li>
          </ul>
        </section>
      </div>
    </template>
  </UModal>
</template>
