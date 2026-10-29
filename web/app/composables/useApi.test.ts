import { $fetch as ofetch } from 'ofetch'
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

  // Through ofetch itself, the app's $fetch, against a server's reply: under `responseType: 'text'` it hands back an error's JSON body
  // as text, which a stub that rejects with the body already parsed hid (the reviews of #422).
  it('normalises an error as request does, the body ofetch hands a text request as text', async () => {
    const reply = (status: number, body: string, type: string) => vi.fn(async () => new Response(body, { status, headers: { 'Content-Type': type } }))
    const gone = reply(410, JSON.stringify({ error: { code: 'log_dropped', message: 'the log was not kept' } }), 'application/json')
    vi.stubGlobal('$fetch', ofetch.create({}, { fetch: gone }))
    const asText = await ofetch.create({}, { fetch: gone }).raw('/log', { responseType: 'text' }).catch((e: { data?: unknown }) => e.data)
    expect(typeof asText).toBe('string')
    const { request, requestText } = useApi()
    const fromText = await requestText('/log').catch((e: unknown) => e)
    const fromJSON = await request('/log').catch((e: unknown) => e)
    for (const e of [fromText, fromJSON]) {
      expect(e).toBeInstanceOf(ApiError)
      expect(e).toMatchObject({ status: 410, code: 'log_dropped', message: 'the log was not kept', details: { code: 'log_dropped' } })
    }
    // A body that is no JSON (a proxy's page) is no error body: the status alone.
    vi.stubGlobal('$fetch', ofetch.create({}, { fetch: reply(404, 'nothing here', 'text/plain') }))
    const plain = await useApi().requestText('/log').catch((e: unknown) => e)
    expect(plain).toMatchObject({ status: 404, code: 'http_error' })
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
