<script setup lang="ts">
const shortcuts = useShortcutsModal()
const desktop = useDesktop()
const systemName = computed(() => ({ mac: 'macOS', windows: 'Windows', linux: 'Linux' })[shortcuts.system.value])
const surfaceName = computed(() => (desktop.isDesktop.value ? 'the desktop app' : 'a browser'))
/** Every row's columns: the action, then the two key columns at one fixed width each (wider at xl), so they line up down the table. */
const COLUMNS = 'grid grid-cols-1 sm:grid-cols-[minmax(0,1fr)_10.5rem_10.5rem] xl:grid-cols-[minmax(0,1fr)_12rem_12rem] sm:gap-x-0'
</script>

<template>
  <!-- One column template for every row (round 14: each row sized its own key columns, so nothing lined up and the owner could not
       tell the columns apart): the action, then two columns of fixed width for the keys, the caps centred in each. The column names
       stand once, in a header that stays over the scroll (#328: they repeated under one group and not the other, the cells were
       tinted with the chords left-aligned in one column and centred in the other, a column with no chord showed a dot, and the
       description ran under the close button). On a phone a row stacks: the action, then its two key sets, each named. -->
  <UModal
    v-model:open="shortcuts.open.value"
    title="Keyboard shortcuts"
    description="The action, its keys while typing into an agent, and its keys when no terminal or text field has the focus. Every action is also a button."
    :ui="{ content: 'sm:max-w-3xl xl:max-w-5xl', header: 'pe-14', description: 'pe-14 text-pretty' }"
  >
    <template #body>
      <div class="flex flex-col gap-6" data-shortcuts>
        <div :class="[COLUMNS, 'sticky -top-4 z-10 -mt-4 hidden bg-default pb-1 pt-4 text-[11px] font-semibold uppercase tracking-wide text-muted sm:-top-6 sm:-mt-6 sm:grid sm:pt-6']" data-shortcuts-columns>
          <span>Action</span>
          <span class="px-3 text-center">In a terminal</span>
          <span class="px-3 text-center">Outside</span>
        </div>
        <section v-for="group in shortcuts.groups.value" :key="group.title" :data-shortcuts-group="group.title">
          <h3 class="mb-1 border-b border-default pb-1 text-xs font-semibold uppercase tracking-wide text-highlighted">{{ group.title }}</h3>
          <ul class="divide-y divide-default">
            <li v-for="row in group.rows" :key="row.label" :class="[COLUMNS, 'items-center gap-y-1 py-1.5 text-sm']" :data-shortcut="row.label">
              <span class="text-pretty">{{ row.label }}</span>
              <span class="flex items-center gap-1 sm:justify-center sm:px-3">
                <span class="me-1 w-16 text-[11px] text-muted sm:hidden">Terminal</span>
                <span class="flex items-center gap-1" data-shortcut-terminal>
                  <template v-if="row.terminal">
                    <template v-for="(key, i) in row.terminal" :key="i">
                      <UKbd v-if="key === 'enter'" size="md">Enter</UKbd>
                      <UKbd v-else :value="key" size="md" />
                    </template>
                  </template>
                  <span v-else aria-label="none" />
                </span>
              </span>
              <span class="flex items-center gap-1 sm:justify-center sm:px-3">
                <span class="me-1 w-16 text-[11px] text-muted sm:hidden">Outside</span>
                <span class="flex items-center gap-1" data-shortcut-outside>
                  <span v-if="!row.keys.length" aria-label="none" />
                  <template v-for="(key, i) in row.keys" :key="i">
                    <span v-if="i > 0 && row.keys[0] === 'G' && group.title === 'Everywhere'" class="text-sm text-muted">then</span>
                    <UKbd v-if="key === 'enter'" size="md">Enter</UKbd>
                    <UKbd v-else :value="key" size="md" />
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
