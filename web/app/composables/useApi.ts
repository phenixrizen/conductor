import type { FetchError } from 'ofetch'

export interface ApiErrorBody {
  error?: { code: string; message: string }
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

/** Base URL helpers shared by HTTP and WebSocket clients. */
export function useApiBase() {
  const config = useRuntimeConfig()
  const httpBase = computed(() => (config.public.apiBase as string) || '')
  const wsBase = computed(() => {
    const base = httpBase.value
    if (base) return base.replace(/^http/, 'ws')
    if (import.meta.client) {
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
      return `${proto}//${location.host}`
    }
    return ''
  })
  return { httpBase, wsBase }
}

/** $fetch wrapper that attaches the admin token and normalises errors. */
export function useApi() {
  const { httpBase } = useApiBase()
  const admin = useAdminToken()

  async function request<T>(path: string, opts: { method?: string; body?: unknown; token?: string; query?: Record<string, string> } = {}): Promise<T> {
    const token = opts.token ?? admin.token.value
    try {
      return await $fetch<T>(httpBase.value + path, {
        method: (opts.method ?? 'GET') as 'GET',
        body: opts.body as Record<string, unknown> | undefined,
        query: opts.query,
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      })
    } catch (e) {
      const fe = e as FetchError<ApiErrorBody>
      const status = fe.statusCode ?? 0
      const code = fe.data?.error?.code ?? (status === 0 ? 'network' : 'http_error')
      const message = fe.data?.error?.message ?? (status === 0 ? 'Cannot reach the server' : fe.message)
      if (status === 401 && opts.token === undefined) admin.needsToken.value = true
      throw new ApiError(status, code, message)
    }
  }

  return { request }
}
