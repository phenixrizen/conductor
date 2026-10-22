<script setup lang="ts">
import { injectContextMenuRootContext } from 'reka-ui'

/**
 * A closer for the UContextMenu it sits in (#330, Codex's second round): rendered inside the menu's trigger, it reaches Reka's
 * root and closes the menu from outside Reka's own Escape, which listens on the window where the page's shortcuts would hear
 * the key too; and it says whether the menu is open now, from the root itself.
 */
const ctx = injectContextMenuRootContext()
defineExpose({ close: () => ctx.onOpenChange(false), open: () => ctx.open.value })
</script>

<template>
  <span hidden />
</template>
