import { desktopBridge } from '~/utils/desktop'
import { inviteRoute } from '~/utils/invite'

/**
 * An invite (`conductor://…`) the desktop app is handed while its window is open comes over the bridge: the workbench routes to its
 * join page in place, keeping its terminals and its live store, where the app used to load the page anew.
 */
export default defineNuxtPlugin(() => {
  const bridge = desktopBridge()
  if (!bridge?.onInvite) return
  const router = useRouter()
  // The invite is taken once the router has navigated to its join page. One this page refuses, or a navigation that fails or is
  // superseded, is left to the app (false), which loads the join page for it, where the page says what is wrong.
  bridge.onInvite(async (invite) => {
    const to = inviteRoute(invite)
    if (!to) return false
    try {
      return !(await router.push(to))
    } catch {
      return false
    }
  })
})
