import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, setCsrf, whenAuthLost } from '../api'

function reply(status: number, body: unknown) {
  return vi.fn(async () => new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrf('')
})

describe('api', () => {
  it('sends the CSRF token on changes only', async () => {
    const fetchMock = reply(200, { ok: true })
    vi.stubGlobal('fetch', fetchMock)
    setCsrf('tok')
    await api('GET', '/me')
    await api('POST', '/auth/logout', {})
    const headers = (i: number) => (fetchMock.mock.calls[i] as unknown as [string, RequestInit])[1].headers as Record<string, string>
    expect(headers(0)['X-CSRF-Token']).toBeUndefined()
    expect(headers(1)['X-CSRF-Token']).toBe('tok')
    expect((fetchMock.mock.calls[1] as unknown as [string])[0]).toBe('/api/v1/auth/logout')
  })
  it('turns error answers into ApiError and reports a lost session', async () => {
    vi.stubGlobal('fetch', reply(401, { error: 'unauthorized', message: 'log in first' }))
    const lost = vi.fn()
    whenAuthLost(lost)
    await expect(api('GET', '/overview')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(lost).toHaveBeenCalledOnce()
    lost.mockClear()
    await expect(api('POST', '/auth/login', {})).rejects.toBeInstanceOf(ApiError)
    expect(lost).not.toHaveBeenCalled() // a failed login is not a lost session
  })
  it('handles empty and network answers', async () => {
    vi.stubGlobal('fetch', reply(204, null))
    await expect(api('POST', '/auth/logout')).resolves.toBeUndefined()
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline') }))
    await expect(api('GET', '/me')).rejects.toMatchObject({ code: 'network' })
  })
})
