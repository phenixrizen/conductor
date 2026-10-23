<script setup lang="ts">
import type { CrewCheck } from '~/composables/useSessions'
import { TASK_LIMITS, checkProblems } from '~/utils/crews'

/**
 * The commands of a task's checks or a crew's setup (#334): an id, an argv
 * as chips (ArgvInput: never a shell line, a quoted argument one chip) and a
 * timeout in seconds. The list's owner names what they are.
 */
const checks = defineModel<CrewCheck[]>({ default: () => [] })
const props = defineProps<{ what: string; max?: number }>()

const max = computed(() => props.max ?? TASK_LIMITS.checks)

function update(i: number, patch: Partial<CrewCheck>) {
  checks.value = checks.value.map((c, j) => (j === i ? { ...c, ...patch } : c))
}

function remove(i: number) {
  checks.value = checks.value.filter((_, j) => j !== i)
}

function add() {
  const ids = checks.value.map((c) => c.id)
  let id = 'check'
  for (let n = 2; ids.includes(id); n++) id = `check-${n}`
  checks.value = [...checks.value, { id, argv: [] }]
}

/** The first problem of a row, for the mark under it. */
function problem(c: CrewCheck, i: number): string {
  const taken = checks.value.filter((_, j) => j !== i).map((x) => x.id)
  return checkProblems(c, props.what, taken)[0]?.replace(`${props.what}: `, '') ?? ''
}

function timeoutOf(c: CrewCheck): string {
  return c.timeoutSeconds ? String(c.timeoutSeconds) : ''
}

function setTimeout(i: number, text: string) {
  const n = Number.parseInt(text, 10)
  update(i, { timeoutSeconds: Number.isFinite(n) && n > 0 ? n : undefined })
}
</script>

<template>
  <div class="flex flex-col gap-2" :data-crew-checks="what">
    <div v-for="(c, i) in checks" :key="i" class="flex flex-col gap-1" data-crew-check>
      <div class="flex flex-wrap items-start gap-2">
        <UInput :model-value="c.id" placeholder="id" maxlength="40" autocapitalize="off" spellcheck="false" :aria-label="`Id of ${what} ${i + 1}`" :ui="{ base: 'font-mono' }" class="w-28" @update:model-value="update(i, { id: String($event) })" />
        <div class="min-w-48 flex-1">
          <ArgvInput :model-value="c.argv" :placeholder="`program and arguments; a quoted argument is one`" :invalid="!c.argv.length" :aria-label="`Command of ${what} ${i + 1}`" @update:model-value="update(i, { argv: $event })" />
        </div>
        <UInput :model-value="timeoutOf(c)" type="number" inputmode="numeric" min="1" :max="TASK_LIMITS.timeout" :placeholder="String(TASK_LIMITS.timeoutDefault)" :aria-label="`Timeout in seconds of ${what} ${i + 1}`" class="w-24" @update:model-value="setTimeout(i, String($event))">
          <template #trailing><span class="text-xs text-muted">s</span></template>
        </UInput>
        <UButton icon="i-lucide-x" color="neutral" variant="ghost" size="xs" :aria-label="`Remove ${what} ${i + 1}`" @click="remove(i)" />
      </div>
      <span v-if="problem(c, i)" class="text-xs leading-snug text-error" data-check-error>{{ problem(c, i) }}</span>
    </div>
    <UButton :label="`Add ${what}`" icon="i-lucide-plus" color="secondary" variant="link" size="sm" class="self-start px-0" :disabled="checks.length >= max" data-add-check @click="add" />
  </div>
</template>
