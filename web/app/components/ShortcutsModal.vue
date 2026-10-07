<script setup lang="ts">
const shortcuts = useShortcutsModal()
</script>

<template>
  <UModal
    v-model:open="shortcuts.open.value"
    title="Keyboard shortcuts"
    description="Three columns: the action, its keys while typing into an agent (the terminal hands these Alt chords back to the page), and its keys when no terminal or text field has the focus. Every action is also a button."
    :ui="{ header: 'pe-14', description: 'text-pretty' }"
  >
    <template #body>
      <div class="flex flex-col gap-5" data-shortcuts>
        <div class="grid grid-cols-[1fr_auto_auto] items-center gap-x-6 text-[11px] font-semibold uppercase tracking-wide text-muted" data-shortcuts-columns>
          <span>Action</span>
          <span class="text-right">In a terminal</span>
          <span class="text-right">Outside</span>
        </div>
        <section v-for="group in shortcuts.groups.value" :key="group.title" :data-shortcuts-group="group.title">
          <h3 class="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{{ group.title }}</h3>
          <ul class="divide-y divide-default">
            <li v-for="row in group.rows" :key="row.label" class="grid grid-cols-[1fr_auto_auto] items-center gap-x-6 py-1.5 text-sm" :data-shortcut="row.label">
              <span class="text-pretty">{{ row.label }}</span>
              <span class="flex items-center justify-end gap-1" data-shortcut-terminal>
                <template v-if="row.terminal">
                  <UKbd v-for="(key, i) in row.terminal" :key="i" :value="key" size="md" />
                </template>
                <span v-else class="text-xs text-muted" aria-label="none">·</span>
              </span>
              <span class="flex items-center justify-end gap-1" data-shortcut-outside>
                <template v-for="(key, i) in row.keys" :key="i">
                  <span v-if="i > 0 && row.keys[0] === 'G' && group.title === 'Everywhere'" class="text-xs text-muted">then</span>
                  <UKbd :value="key" size="md" />
                </template>
              </span>
            </li>
          </ul>
        </section>
      </div>
    </template>
  </UModal>
</template>
