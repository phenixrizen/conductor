import type { Ref } from 'vue'

/**
 * A media query as a ref, followed as the window changes. For what CSS alone
 * cannot decide: which component a width gets (a sheet over the terminal on
 * a phone, the panel beside it above), or whether a panel hidden by a class
 * is in front of the person at all.
 */
export function useMedia(query: string): Readonly<Ref<boolean>> {
  const matches = ref(import.meta.client ? window.matchMedia(query).matches : false)
  let media: MediaQueryList | undefined
  const update = () => (matches.value = !!media?.matches)
  onMounted(() => {
    media = window.matchMedia(query)
    media.addEventListener('change', update)
    update()
  })
  onBeforeUnmount(() => media?.removeEventListener('change', update))
  return readonly(matches)
}

/** Below Tailwind's `sm` (640 px): a phone, where the chat opens as a sheet over the terminal (design 2c). */
export function useIsPhone(): Readonly<Ref<boolean>> {
  return useMedia('(max-width: 639.98px)')
}
