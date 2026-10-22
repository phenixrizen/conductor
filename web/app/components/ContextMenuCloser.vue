<script setup lang="ts">
import { injectContextMenuRootContext } from 'reka-ui'

/**
 * A closer for the UContextMenu it sits in (#330, Codex's second round): rendered inside the menu's trigger, it reaches Reka's
 * root and closes the menu from outside Reka's own Escape, which listens on the window where the page's shortcuts would hear
 * the key too.
 */
const ctx = injectContextMenuRootContext()
defineExpose({ close: () => ctx.onOpenChange(false) })
</script>

<template>
  <span hidden />
</template>
