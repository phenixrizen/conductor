/**
 * What the server says of itself on GET /api/whoami, read once per app: its host name (for the "Not installed on <host>" notes) and
 * whether it is a switchyard, a coordinator of hosted sessions that launches nothing (the Agents and Crews pages say so). Empty and
 * false until known or when the server cannot tell.
 */
export function useServerHost() {
  const host = useState<string>('serverHost', () => '')
  const switchyard = useState<boolean>('serverSwitchyard', () => false)
  const asked = useState<boolean>('serverHostAsked', () => false)
  const api = useSessions()
  async function load() {
    if (asked.value) return
    asked.value = true
    try {
      const me = await api.whoami()
      host.value = me.host ?? ''
      switchyard.value = me.switchyard === true
    } catch {
      asked.value = false
    }
  }
  return { host, switchyard, load }
}
