import { desktopBridge } from '~/utils/desktop'

const STORAGE_KEY = 'conductor.adminToken'

function readStored(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

/**
 * The admin token typed by the operator, kept in localStorage for this browser; in the desktop app, the token the shell minted for
 * this run, read from its bridge and kept in memory only.
 */
export function useAdminToken() {
  const desktop = import.meta.client ? desktopBridge() : null
  const token = useState<string>('adminToken', () => (import.meta.client && !desktop ? readStored() : ''))
  const needsToken = useState<boolean>('adminTokenNeeded', () => false)
  const asked = useState<boolean>('adminTokenAskedDesktop', () => false)
  if (desktop && !asked.value) {
    asked.value = true
    desktop
      .token()
      .then((t) => {
        if (t) {
          token.value = t
          needsToken.value = false
        }
      })
      .catch(() => {})
  }

  function set(value: string) {
    token.value = value.trim()
    if (!desktop) {
      try {
        if (token.value) localStorage.setItem(STORAGE_KEY, token.value)
        else localStorage.removeItem(STORAGE_KEY)
      } catch {
        /* private mode */
      }
    }
    if (token.value) needsToken.value = false
  }

  function clear() {
    set('')
  }

  return {
    token,
    hasToken: computed(() => token.value.length > 0),
    needsToken,
    set,
    clear,
  }
}
