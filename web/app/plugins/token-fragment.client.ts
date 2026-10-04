import { tokenFromFragment, withoutTokenFragment } from '~/utils/desktop'

/**
 * "Open in browser" from the desktop app opens the workbench with the workbench token in the URL fragment, which never reaches the
 * server: the token is taken into the workbench token store and dropped from the address bar before anything else runs.
 */
export default defineNuxtPlugin(() => {
  const token = tokenFromFragment(location.hash)
  if (!token) return
  useWorkbenchToken().set(token)
  history.replaceState(history.state, '', withoutTokenFragment(location.href))
})
