<script setup lang="ts">
import type { PathEntry } from '~/composables/useSessions'
import { DIR_DEBOUNCE_MS, dirQuery, gitMark, matchingEntries } from '~/utils/dirInput'

/**
 * A working-directory field completed from the server: as the text changes,
 * GET /api/paths lists the child directories of the longest existing
 * directory in it (under the allowed roots), each marked when it is a git
 * repository with a commit. Nuxt UI's UInputMenu in autocomplete mode is the
 * combobox: the text is the model and stays editable, the server's entries
 * are its items (its own filter is off: the server filters by what is
 * typed), the arrow keys move, Enter or a click picks (the entry's path
 * replaces the text and the list opens again on its children), Esc closes.
 * While the list is open, Enter picks the highlighted entry (the first until
 * the arrows move); with it closed, Enter belongs to the form around. Until
 * the listing of what is typed arrives, only the entries under it stay, so
 * Enter never puts an entry the text has moved past in its place. Tab moves
 * on and closes the list. The model is the text, whatever is picked.
 */
const model = defineModel<string>({ default: '' })
defineProps<{ placeholder?: string; name?: string }>()

const api = useSessions()
const admin = useAdminToken()

const root = useTemplateRef<HTMLElement>('root')
const menu = useTemplateRef<{ inputRef?: HTMLInputElement; viewportRef?: HTMLElement }>('menu')
const open = ref(false)
const entries = ref<PathEntry[]>([])
const truncated = ref(false)
const loading = ref(false)
// A listing is due after the pause.
const pending = ref(false)
const problem = ref('')
let timer: number | undefined
// Replies may come back out of order, or after the text changed again: only the reply to the latest text counts.
let seq = 0
// The text (trimmed) the entries or the problem shown answer.
let listedFor: string | undefined
// The text became an entry's path in the field: picked (Enter, or a click, which takes the focus away), or typed in full.
// Only then does a reply open the list again; Esc or leaving the field forgets it.
let picked = false

async function fetchNow() {
  pending.value = false
  if (!admin.hasToken.value) return
  const n = ++seq
  const query = dirQuery(model.value)
  loading.value = true
  try {
    const r = await api.listPaths(query)
    if (n !== seq) return
    entries.value = r.entries
    truncated.value = r.truncated
    problem.value = ''
  } catch (e) {
    if (n !== seq) return
    entries.value = []
    truncated.value = false
    problem.value = (e as Error).message
  } finally {
    if (n === seq) {
      listedFor = query
      loading.value = false
      // A pick closes the list: it opens again on the picked directory's children, the focus back in the field.
      // Typing opens it by itself; a reply after Esc leaves it closed.
      const input = menu.value?.inputRef
      if (input && picked) {
        picked = false
        input.focus()
        open.value = true
      }
    }
  }
}

// Lists the text after a pause; a reply to an earlier text, still on its way, no longer counts.
function schedule() {
  seq++
  pending.value = true
  window.clearTimeout(timer)
  timer = window.setTimeout(fetchNow, DIR_DEBOUNCE_MS)
}

// Typing and picking in the field change the text; a change from the form around (a crew discarded) is no pick.
// A pick of the text as it stands changes nothing: its children are listed all the same.
function onText(text: string) {
  if (entries.value.some((e) => e.path === text)) picked = true
  if (text === model.value) schedule()
  model.value = text
}

// Any change of the text: the entries it has moved past go at once, and it is listed again after a pause.
watch(model, (text) => {
  entries.value = matchingEntries(entries.value, text)
  schedule()
})

// The field's focus, or the one UInputMenu reports when a keystroke opens the list (before the text
// changes): it lists only a text neither listed nor due, so that keystroke sends one request, not two.
function onFocus() {
  if (!pending.value && !loading.value && listedFor !== dirQuery(model.value)) fetchNow()
}

// UInputMenu keeps its list open when the focus leaves the field (its content
// prevents focus-outside), so Tab would leave it over the fields below: the
// focus going anywhere but the field or the list closes it. A click on an
// option focuses the option, inside the list, and picks as before.
function onBlur(e: FocusEvent) {
  const to = e.relatedTarget as Node | null
  if (to && !root.value?.contains(to) && !menu.value?.viewportRef?.parentElement?.contains(to)) {
    picked = false
    open.value = false
  }
}

function onEscape() {
  picked = false
}

onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <div ref="root" data-dir-input @keydown.esc="onEscape">
    <UInputMenu
      ref="menu"
      :model-value="model"
      v-model:open="open"
      mode="autocomplete"
      :items="entries"
      value-key="path"
      label-key="name"
      ignore-filter
      open-on-focus
      :loading="loading"
      :placeholder="placeholder"
      :name="name"
      autocapitalize="off"
      autocomplete="off"
      spellcheck="false"
      :ui="{ base: 'font-mono' }"
      class="w-full"
      @update:model-value="onText"
      @focus="onFocus"
      @blur="onBlur"
    >
      <template #item-leading>
        <UIcon name="i-lucide-folder" class="size-4 flex-none text-muted" />
      </template>
      <template #item-label="{ item }">
        <span class="font-mono text-xs">{{ item.name }}</span>
      </template>
      <template #item-trailing="{ item }">
        <UBadge v-if="gitMark(item.git).label" :label="gitMark(item.git).label" :color="gitMark(item.git).tone" variant="subtle" size="sm" />
      </template>
      <template #empty>
        <span aria-live="polite" :class="{ 'text-warning': problem && !pending && !loading }">{{ pending || loading ? 'Looking…' : problem || 'No directory here.' }}</span>
      </template>
      <template #content-bottom>
        <div aria-live="polite">
          <p v-if="truncated" class="px-2 py-1 text-[11px] text-muted">More here than listed: keep typing to narrow it.</p>
        </div>
      </template>
    </UInputMenu>
  </div>
</template>
