import type { Attention, Role } from '~/utils/protocol'

export type SessionKind = 'server' | 'hosted'
export type SessionStatus = 'starting' | 'running' | 'exited' | 'stopped' | 'host_disconnected'

export interface SessionInfo {
  id: string
  name: string
  kind: SessionKind
  agentId: string
  command: string[]
  cwd: string
  status: SessionStatus
  exitCode?: number
  cols: number
  rows: number
  viewers: number
  hostName?: string
  attention?: Attention
  createdAt: string
  endedAt?: string
}

export interface AgentInfo {
  id: string
  name: string
  description?: string
  command: string[]
  allowArgs: boolean
  cwd?: string
  icon?: string
}

export interface ShareLink {
  id: string
  sessionId: string
  label?: string
  role: Role
  createdAt: string
  expiresAt?: string
  revoked: boolean
}

export interface JoinInfo {
  session: Pick<SessionInfo, 'id' | 'name' | 'agentId' | 'kind' | 'status' | 'cols' | 'rows' | 'hostName'>
  role: Role
  label?: string
}

export function useSessions() {
  const { request } = useApi()

  return {
    list: () => request<{ sessions: SessionInfo[] }>('/api/sessions').then((r) => r.sessions ?? []),
    get: (id: string, token?: string) => request<{ session: SessionInfo; role: Role; links?: ShareLink[] }>(`/api/sessions/${encodeURIComponent(id)}`, { token }),
    create: (body: { agentId: string; name?: string; cwd?: string; args?: string[]; cols?: number; rows?: number }) =>
      request<SessionInfo>('/api/sessions', { method: 'POST', body }),
    stop: (id: string) => request<SessionInfo | void>(`/api/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    catalog: () => request<{ agents: AgentInfo[] }>('/api/catalog').then((r) => r.agents ?? []),
    links: (id: string) => request<{ links: ShareLink[] }>(`/api/sessions/${encodeURIComponent(id)}/links`).then((r) => r.links ?? []),
    createLink: (id: string, body: { role: Role; label?: string; ttlSeconds?: number }) =>
      request<{ link: ShareLink; token: string; url: string }>(`/api/sessions/${encodeURIComponent(id)}/links`, { method: 'POST', body }),
    revokeLink: (id: string, linkId: string) =>
      request<void>(`/api/sessions/${encodeURIComponent(id)}/links/${encodeURIComponent(linkId)}`, { method: 'DELETE' }),
    join: (token: string) => request<JoinInfo>(`/api/join/${encodeURIComponent(token)}`, { token }),
    setAttention: (id: string, state: 'needs_input' | 'working' | 'done' | 'clear', message?: string) =>
      request<{ attention: Attention }>(`/api/sessions/${encodeURIComponent(id)}/attention`, { method: 'POST', body: { state, message } }),
  }
}

export function statusColor(status: SessionStatus): 'success' | 'neutral' | 'warning' | 'error' | 'info' {
  switch (status) {
    case 'running':
      return 'success'
    case 'starting':
      return 'info'
    case 'host_disconnected':
      return 'warning'
    case 'exited':
      return 'neutral'
    case 'stopped':
      return 'neutral'
  }
  return 'neutral'
}

export function statusLabel(status: SessionStatus): string {
  return status.replace('_', ' ')
}
