/** The server's host name from GET /api/integrations, read once per app for the "Not installed on <host>" notes; empty until known or when the server cannot tell. */
export function useServerHost() {
  const host = useState<string>('serverHost', () => '')
  const asked = useState<boolean>('serverHostAsked', () => false)
  const api = useSessions()
  async function load() {
    if (asked.value) return
    asked.value = true
    try {
      host.value = (await api.integrations()).host
    } catch {
      asked.value = false
    }
  }
  return { host, load }
}
