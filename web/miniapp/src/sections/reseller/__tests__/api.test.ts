import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../../api'
import { setLang } from '../../../i18n'
import { errText, filters, isGone, resellerApi } from '../api'
import { cleanLabel, cleanNote, length, MAX_LABEL, MAX_NOTE, oneLine } from '../text'
import { fakeCore, json, svc } from './helpers'

afterEach(() => vi.restoreAllMocks())

describe('the reseller calls', () => {
  it('ask for the list with the group, the search and the page, and leave out what is empty', async () => {
    const calls = fakeCore(() => ({ services: [], total: 0, page: 1, size: 10, counts: {} }))
    await resellerApi.services()
    await resellerApi.services({ status: 'expired', q: 'ali baba #a1b2c3', page: 2 })
    await resellerApi.services({ status: '', q: '', page: 1, size: 5 })
    expect(calls.map((c) => c.method + ' ' + c.path)).toEqual([
      'GET /reseller/services',
      'GET /reseller/services?status=expired&q=ali+baba+%23a1b2c3&page=2',
      'GET /reseller/services?page=1&size=5',
    ])
  })

  it('read the panel, the customers and the statement', async () => {
    const calls = fakeCore(() => ({}))
    await resellerApi.panel()
    await resellerApi.customers()
    await resellerApi.customers(3)
    await resellerApi.statement(2)
    expect(calls.map((c) => c.method + ' ' + c.path)).toEqual([
      'GET /reseller', 'GET /reseller/customers?page=1', 'GET /reseller/customers?page=3', 'GET /reseller/statement?page=2',
    ])
  })

  it('change a service with its own call and hand back the service as it is now', async () => {
    const id = '01a156a6-0fa9-7182-959e-98b79df62d88'
    const calls = fakeCore((c) => (c.path.endsWith('/delete') ? { ok: true } : { service: svc({ label: 'Reza' }) }))
    expect((await resellerApi.pause(id)).label).toBe('Reza')
    await resellerApi.resume(id)
    await resellerApi.rename(id, 'Reza')
    await resellerApi.note(id, 'pays on the 5th')
    expect(await resellerApi.remove(id)).toEqual({ ok: true })
    const base = `POST /reseller/services/${id}/`
    expect(calls.map((c) => c.method + ' ' + c.path)).toEqual([base + 'pause', base + 'resume', base + 'label', base + 'note', base + 'delete'])
    expect(calls.map((c) => c.body)).toEqual([{}, {}, { label: 'Reza' }, { note: 'pays on the 5th' }, { confirm: true }])
  })

  it('never send a price or an amount, or another user', async () => {
    const calls = fakeCore(() => ({ service: svc(), services: [], customers: [], entries: [] }))
    const id = '01a156a6-0fa9-7182-959e-98b79df62d88'
    await resellerApi.panel()
    await resellerApi.services({ status: 'active', q: 'x', page: 3 })
    await resellerApi.pause(id)
    await resellerApi.resume(id)
    await resellerApi.rename(id, 'n')
    await resellerApi.note(id, 'n')
    await resellerApi.remove(id)
    await resellerApi.customers(2)
    await resellerApi.statement(2)
    for (const c of calls) {
      const sent = JSON.stringify(c.body ?? {}) + c.path
      for (const forbidden of ['price', 'amount', 'total', 'user_id', 'reseller_id', 'tier', 'commission', 'credit']) {
        expect(sent, `${c.method} ${c.path}`).not.toContain(forbidden)
      }
    }
  })

  it('know the groups', () => {
    expect(filters).toEqual(['', 'active', 'expiring_soon', 'expired', 'paused'])
  })
})

describe('what a failed call says', () => {
  it('words the reseller-only failures in the reseller language, both ways', () => {
    for (const lang of ['en', 'fa'] as const) {
      setLang(lang)
      const said = new Set(
        ['paused_by_staff', 'not_found', 'forbidden', 'state'].map((code) => errText(new ApiError(409, code, 'x'))),
      )
      expect(said.size).toBe(4)
      for (const s of said) expect(s.startsWith('reseller.'), `${lang}: ${s}`).toBe(false) // a real sentence, not a missing key
    }
    setLang('en')
    expect(errText(new ApiError(409, 'paused_by_staff', ''))).toContain('Support paused')
    expect(errText(new ApiError(429, 'rate_limited', ''))).toBe('Too many requests. Wait a minute.')
    expect(errText(new ApiError(0, 'network', ''))).toBe('Could not connect. Check your internet.')
    expect(errText(new Error('boom'))).toBe('Something went wrong. Try again.')
  })

  it('tells a service that is gone', () => {
    expect(isGone(new ApiError(404, 'not_found', ''))).toBe(true)
    expect(isGone(new ApiError(409, 'state', ''))).toBe(false)
    expect(isGone(new Error('x'))).toBe(false)
  })

  it('reaches core through the app session (one sign-in, then the token)', async () => {
    const calls = fakeCore(() => ({}))
    await resellerApi.panel()
    const fetchSpy = globalThis.fetch as unknown as { mock: { calls: [string, RequestInit][] } }
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/mini/v1/auth')
    expect((fetchSpy.mock.calls[1][1].headers as Record<string, string>).Authorization).toBe('Bearer tok1')
    expect(calls).toHaveLength(1)
  })

  it('shows a refusal from core as an error, not as data', async () => {
    fakeCore(() => json(403, { error: 'forbidden', message: 'nope' }))
    await expect(resellerApi.panel()).rejects.toMatchObject({ status: 403, code: 'forbidden' })
  })
})

describe('what a reseller types', () => {
  it('is one line', () => {
    expect(oneLine('  Ali’s \n  phone\t2 ')).toBe('Ali’s phone 2')
  })

  it('keeps a name to 64 characters and a note to 200, counting characters not bytes', () => {
    expect(cleanLabel('  گوشی   علی ')).toBe('گوشی علی')
    expect(cleanLabel('ب'.repeat(MAX_LABEL))).toBe('ب'.repeat(MAX_LABEL))
    expect(cleanLabel('ب'.repeat(MAX_LABEL + 1))).toBeNull()
    expect(cleanLabel('   ')).toBe('') // an empty name removes the name
    expect(cleanNote('ب'.repeat(MAX_NOTE))).toHaveLength(MAX_NOTE)
    expect(cleanNote('x'.repeat(MAX_NOTE + 1))).toBeNull()
    expect(cleanNote('a\nb')).toBe('a b')
    expect(length('😀😀')).toBe(2) // two characters, four UTF-16 units
    expect(cleanLabel('😀'.repeat(MAX_LABEL))).not.toBeNull()
  })
})
