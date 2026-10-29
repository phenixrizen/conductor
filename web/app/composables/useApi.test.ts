import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, ref } from 'vue'
import { ApiError, useApi } from './useApi'

// useApi reads Nuxt's auto-imports (useRuntimeConfig, computed, useWorkbenchToken, $fetch) as globals: each is stubbed here.
describe('requestText', () => {
  const raw = vi.fn()
  const fetchFn = Object.assign(vi.fn(), { raw })
  const needsToken = ref(false)

  beforeEach(() => {
    raw.mockReset()
    fetchFn.mockReset()
    needsToken.value = false
    vi.stubGlobal('useRuntimeConfig', () => ({ public: { apiBase: 'http://api.test' } }))
    vi.stubGlobal('computed', computed)
    vi.stubGlobal('useWorkbenchToken', () => ({ token: ref('workbench-token'), needsToken }))
    vi.stubGlobal('$fetch', fetchFn)
  })
  afterEach(() => vi.unstubAllGlobals())

  it('returns the body as text with the reply headers, the bearer header sent', async () => {
    raw.mockResolvedValue({ _data: 'set-up\n', headers: new Headers({ 'X-Conductor-Log-SHA256': 'ab12' }) })
    const { requestText } = useApi()
    const reply = await requestText('/api/runs/r-1/prerequisites/tools/1/log')
    expect(reply.text).toBe('set-up\n')
    expect(reply.headers.get('X-Conductor-Log-SHA256')).toBe('ab12')
    expect(raw).toHaveBeenCalledWith('http://api.test/api/runs/r-1/prerequisites/tools/1/log', {
      method: 'GET',
      responseType: 'text',
      headers: { Authorization: 'Bearer workbench-token' },
    })
  })

  it('reads an empty log as empty text', async () => {
    raw.mockResolvedValue({ _data: undefined, headers: new Headers({ 'X-Conductor-Log-SHA256': 'e3b0' }) })
    const reply = await useApi().requestText('/log')
    expect(reply.text).toBe('')
    expect(reply.headers.get('X-Conductor-Log-SHA256')).toBe('e3b0')
  })

  it('normalises an error as request does', async () => {
    const gone = Object.assign(new Error('Gone'), { statusCode: 410, data: { error: { code: 'log_dropped', message: 'the log was not kept' } } })
    raw.mockRejectedValue(gone)
    fetchFn.mockRejectedValue(gone)
    const { request, requestText } = useApi()
    const fromText = await requestText('/log').catch((e: unknown) => e)
    const fromJSON = await request('/log').catch((e: unknown) => e)
    for (const e of [fromText, fromJSON]) {
      expect(e).toBeInstanceOf(ApiError)
      expect(e).toMatchObject({ status: 410, code: 'log_dropped', message: 'the log was not kept', details: { code: 'log_dropped' } })
    }
  })

  it('says the server cannot be reached, and asks for the token on a 401 only without one of its own', async () => {
    raw.mockRejectedValueOnce(new Error('fetch failed'))
    const unreachable = await useApi().requestText('/log').catch((e: unknown) => e)
    expect(unreachable).toMatchObject({ status: 0, code: 'network', message: 'Cannot reach the server' })
    const unauthorized = Object.assign(new Error('Unauthorized'), { statusCode: 401, data: { error: { code: 'unauthorized', message: 'workbench token required' } } })
    raw.mockRejectedValueOnce(unauthorized)
    await useApi().requestText('/log', { token: 'a-link-token' }).catch(() => {})
    expect(needsToken.value).toBe(false)
    raw.mockRejectedValueOnce(unauthorized)
    await useApi().requestText('/log').catch(() => {})
    expect(needsToken.value).toBe(true)
  })
})
