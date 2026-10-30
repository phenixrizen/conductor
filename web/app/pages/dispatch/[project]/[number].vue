<script setup lang="ts">
import { projectPath } from '~/utils/dispatch'

// A ticket's page at /dispatch/<KEY>/<n> (docs/plans/dispatch.md, Owner decisions, 9): the ticket CND-12 at /dispatch/CND/12. Escape goes
// back to the project's list (its filter kept when the list is where this page was opened from), its row focused again; it pauses in
// a text field, a terminal and an open menu or dialog, as every page key does. The page's group in the palette: back, the key copied,
// a new ticket in the project.
const route = useRoute()
const router = useRouter()
const project = computed(() => String(route.params.project ?? '').toUpperCase())
const ticketKey = computed(() => `${project.value}-${String(route.params.number ?? '')}`)

/** The ticket the list focuses again when it shows it (pages/dispatch/[project]/index.vue). */
const backFocus = useState<string>('dispatchBackFocus', () => '')
function goBack() {
  backFocus.value = ticketKey.value
  const list = projectPath(project.value)
  const from = (window.history.state as { back?: unknown } | null)?.back
  if (typeof from === 'string' && (from === list || from.startsWith(`${list}?`))) router.back()
  else void router.push(list)
}
defineShortcuts({
  escape: () => {
    // A dialog's, a menu's or a list of choices' Escape is its own (a picker's: the status, the owner…); a tooltip shown is none of them.
    if (document.querySelector('[role="dialog"], [role="menu"], [role="listbox"]')) return
    goBack()
  },
})

const copy = useCopy()
const palette = usePalette()
palette.registerPage(() => ({
  label: `${ticketKey.value} · Dispatch`,
  rows: [
    { key: 'page:back', label: `Back to ${project.value}`, icon: 'i-lucide-arrow-left', kbds: ['escape'], run: goBack },
    { key: 'page:copy-key', label: `Copy ${ticketKey.value}`, icon: 'i-lucide-copy', run: () => void copy(ticketKey.value, `${ticketKey.value} copied`) },
    { key: 'page:copy-link', label: 'Copy the link to this ticket', icon: 'i-lucide-link', run: () => void copy(window.location.href, 'Link copied') },
    { key: 'page:new-ticket', label: `New ticket in ${project.value}`, icon: 'i-lucide-ticket-plus', run: () => palette.askNewTicket(project.value) },
  ],
}))
</script>

<template>
  <DispatchTicketPage :project="project" :ticket-key="ticketKey" />
</template>
