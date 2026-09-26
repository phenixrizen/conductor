<script setup lang="ts">
const shortcuts = useShortcutsModal()
</script>

<template>
  <UModal v-model:open="shortcuts.open.value" title="Keyboard shortcuts" description="Shortcuts pause while a terminal or a text field has focus; the same actions are always available as buttons.">
    <template #body>
      <div class="flex flex-col gap-5">
        <section v-for="group in shortcuts.groups.value" :key="group.title">
          <h3 class="text-xs font-semibold uppercase tracking-wide text-muted mb-2">{{ group.title }}</h3>
          <ul class="divide-y divide-default">
            <li v-for="row in group.rows" :key="row.label" class="flex items-center justify-between gap-4 py-1.5 text-sm">
              <span>{{ row.label }}</span>
              <span class="flex items-center gap-1">
                <template v-for="(key, i) in row.keys" :key="i">
                  <span v-if="i > 0 && group.title === 'Everywhere' && row.keys[0] === 'G'" class="text-xs text-muted">then</span>
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
