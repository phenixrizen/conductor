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
 * authentication: access still comes from the admin token or a share link.
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

  return { name, set }
}
