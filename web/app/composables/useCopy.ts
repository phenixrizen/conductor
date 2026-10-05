/**
 * Copies text to the clipboard and says so in a toast. A page served over
 * plain HTTP has no clipboard API: the failure toast says to copy by hand, and
 * every block offering Copy keeps its text selectable.
 */
export function useCopy() {
  const toast = useToast()
  return async function copy(text: string, title = 'Copied', description?: string) {
    try {
      await navigator.clipboard.writeText(text)
      toast.add({ title, description, icon: 'i-lucide-clipboard-check', color: 'success' })
    } catch {
      toast.add({ title: 'Copy failed', description: 'Select the text and copy it by hand.', icon: 'i-lucide-clipboard-x', color: 'warning' })
    }
  }
}
