import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, resetSession, signIn } from '../api'
import { errorKey, gateFor } from '../errors'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const ME = { user: { id: 'u', username: '', language: 'fa' }, wallets: [], brand: {}, maintenance: false, top_up: {}, features: {}, token: 'tok1', expires_in: 3600 }

beforeEach(() => {
  resetSession()
  window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
})
afterEach(() => vi.restoreAllMocks())

describe('api', () => {
  it('signs in with the init data and then sends the bearer token', async () => {
    const calls: { url: string; init: RequestInit }[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      calls.push({ url: String(url), init: init ?? {} })
      return String(url).endsWith('/auth') ? json(200, ME) : json(200, { plans: [] })
    })
    await api.plans()
    expect(calls[0].url).toBe('/api/mini/v1/auth')
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ init_data: 'user=%7B%7D&hash=abc' })
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBeUndefined()
    expect((calls[1].init.headers as Record<string, string>).Authorization).toBe('Bearer tok1')
  })

  it('renews one expired session and repeats the call', async () => {
    let auths = 0
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      if (String(url).endsWith('/auth')) {
        auths++
        return json(200, { ...ME, token: 'tok' + auths })
      }
      const h = (init?.headers ?? {}) as Record<string, string>
      return h.Authorization === 'Bearer tok1' ? json(401, { error: 'unauthorized', message: 'x' }) : json(200, { services: [] })
    })
    expect(await api.services()).toEqual([])
    expect(auths).toBe(2)
  })

  it('does not loop when signing in keeps failing', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json(401, { error: 'unauthorized', message: 'bad' }))
    await expect(api.plans()).rejects.toBeInstanceOf(ApiError)
  })

  it('refuses to sign in outside Telegram', async () => {
    window.location.hash = ''
    const spy = vi.spyOn(globalThis, 'fetch')
    await expect(signIn()).rejects.toMatchObject({ status: 401 })
    expect(spy).not.toHaveBeenCalled()
  })

  it('turns a network failure into an ApiError', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('offline'))
    await expect(signIn()).rejects.toMatchObject({ code: 'network' })
  })

  it('carries the discount reason', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) =>
      String(url).endsWith('/auth') ? json(200, ME) : json(400, { error: 'discount', message: 'no', reason: 'expired' }),
    )
    await expect(api.quote({ plan_id: 'p' })).rejects.toMatchObject({ code: 'discount', reason: 'expired' })
  })
})

describe('errors', () => {
  it('words customer-safe failures', () => {
    expect(errorKey(new ApiError(402, 'insufficient_funds', ''))).toBe('err.insufficient_funds')
    expect(errorKey(new ApiError(0, 'network', ''))).toBe('err.network')
    expect(errorKey(new ApiError(500, 'weird', ''))).toBe('err.unknown')
    expect(errorKey(new Error('x'))).toBe('err.unknown')
  })

  it('maps full-screen states', () => {
    expect(gateFor(new ApiError(503, 'maintenance', ''))).toBe('maintenance')
    expect(gateFor(new ApiError(403, 'banned', ''))).toBe('banned')
    expect(gateFor(new ApiError(404, 'disabled', ''))).toBe('off')
    expect(gateFor(new ApiError(401, 'auth', ''))).toBe('expired')
    expect(gateFor(new ApiError(404, 'not_found', ''))).toBe('')
    expect(gateFor(new ApiError(400, 'invalid', ''))).toBe('')
  })
})
