const STORAGE_KEY = 'conductor.displayName'
const MAX_NAME = 40

function readStored(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

/**
 * The display name other people on a session see. It is a label, not
 * authentication: access still comes from the workbench token or a share link.
 */
export function useIdentity() {
  const name = useState<string>('displayName', () => (import.meta.client ? readStored() : ''))

  function set(value: string) {
    name.value = value.trim().slice(0, MAX_NAME)
    try {
      if (name.value) localStorage.setItem(STORAGE_KEY, name.value)
      else localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* private mode */
    }
  }

  /**
   * Fills in a default from the server's OS user when nothing is stored.
   * Runs at most once per page; failures leave the name empty.
   */
  const defaulted = useState<boolean>('displayNameDefaulted', () => false)
  async function ensureDefault(fetchUser: () => Promise<{ user: string }>) {
    if (name.value || defaulted.value) return
    defaulted.value = true
    try {
      const { user } = await fetchUser()
      if (user && !name.value) set(user)
    } catch {
      /* stays empty; the user can set one in the sidebar */
    }
  }

  return { name, set, ensureDefault }
}
