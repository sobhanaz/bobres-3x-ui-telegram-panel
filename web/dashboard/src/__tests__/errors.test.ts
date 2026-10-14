import { describe, expect, it } from 'vitest'
import fa from '../locales/fa.json'
import en from '../locales/en.json'
import { ApiError } from '../api'
import { errorText, fieldError } from '../errors'

// The key itself stands in for its text.
const t = (key: string) => key

describe('error texts', () => {
  it('asks to wait when requests come too fast', () => {
    expect(errorText(new ApiError(429, 'rate_limited', 'at most 10 connection tests a minute'), t)).toEqual({ text: 'error.rate_limited', detail: '' })
    // a 429 under another name still means: wait
    expect(errorText(new ApiError(429, 'error', 'Too Many Requests'), t)).toEqual({ text: 'error.rate_limited', detail: '' })
    expect(fa.error.rate_limited).toBe(fa.login.rate_limited)
    expect(en.error.rate_limited).toBe(en.login.rate_limited)
  })
  it('keeps the server’s reason only where it explains', () => {
    expect(errorText(new ApiError(409, 'state', 'the server still has clients'), t)).toEqual({ text: 'error.state', detail: 'the server still has clients' })
    expect(errorText(new ApiError(400, 'invalid', 'weight: 1 to 1000', { field: 'weight' }), t)).toEqual({ text: 'error.invalid', detail: 'weight: 1 to 1000' })
    // a field this role may not set says why
    const owner = 'only the store owner can let BOBRES reach a panel on a private network'
    expect(errorText(new ApiError(403, 'forbidden', owner, { error: 'forbidden', field: 'allow_private', message: owner }), t)).toEqual({ text: 'error.forbidden', detail: owner })
    // a plain refusal does not
    expect(errorText(new ApiError(403, 'forbidden', 'your role may not do this'), t)).toEqual({ text: 'error.forbidden', detail: '' })
    expect(errorText(new ApiError(500, 'internal', 'boom'), t)).toEqual({ text: 'error.generic', detail: '' })
    expect(errorText(new Error('x'), t)).toEqual({ text: 'error.generic', detail: '' })
  })
  it('says which limit refused a new action, and to buy or renew a license', () => {
    const refused = (gate: string, message: string) => new ApiError(409, 'state', message, { error: 'state', license: gate, message })
    const words = 'the free tier allows 1 server: buy or renew a license to add servers'
    expect(errorText(refused('max_servers', words), t)).toEqual({ text: 'license.refused.max_servers', detail: words })
    for (const gate of ['max_servers', 'max_active_services', 'dashboard_staff_seats', 'resellers', 'gateways', 'crypto_watcher', 'other']) {
      for (const [lang, page] of [['fa', fa], ['en', en]] as const) {
        const text = (page.license.refused as Record<string, string>)[gate]
        expect(text, `${lang} ${gate}`).toBeTruthy()
      }
    }
    // a gate this page does not know still gets a sentence
    expect(errorText(refused('something_new', 'x'), t)).toEqual({ text: 'license.refused.other', detail: 'x' })
    // every sentence says to buy or renew a license, in both languages
    for (const text of Object.values(en.license.refused)) expect(text).toContain('Buy or renew a license')
    for (const text of Object.values(fa.license.refused)) expect(text).toContain('بخرید یا تمدید کنید')
    // a refusal that is not about a license keeps the plain state text
    expect(errorText(new ApiError(409, 'state', 'the server still has clients', { error: 'state' }), t)).toEqual({ text: 'error.state', detail: 'the server still has clients' })
  })
  it('names the field core refused, as input or for this role', () => {
    expect(fieldError(new ApiError(400, 'invalid', 'a whole number', { field: 'weight' }))).toEqual({ weight: 'a whole number' })
    expect(fieldError(new ApiError(403, 'forbidden', 'owner only', { field: 'allow_private' }))).toEqual({ allow_private: 'owner only' })
    expect(fieldError(new ApiError(403, 'forbidden', 'your role may not do this'))).toEqual({})
    expect(fieldError(new ApiError(409, 'state', 'open payments', { field: 'merchant_id' }))).toEqual({})
    expect(fieldError(new ApiError(400, 'invalid', 'bad', { field: 7 }))).toEqual({})
    expect(fieldError(new Error('x'))).toEqual({})
  })
})
