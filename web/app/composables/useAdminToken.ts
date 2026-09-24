const STORAGE_KEY = 'conductor.adminToken'

function readStored(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

/** The admin token typed by the operator, kept in localStorage for this browser. */
export function useAdminToken() {
  const token = useState<string>('adminToken', () => (import.meta.client ? readStored() : ''))
  const needsToken = useState<boolean>('adminTokenNeeded', () => false)

  function set(value: string) {
    token.value = value.trim()
    try {
      if (token.value) localStorage.setItem(STORAGE_KEY, token.value)
      else localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* private mode */
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
