import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, resetSession } from '../api'
import { errorKey } from '../errors'
import { messages } from '../i18n'
import { cardDigits, cleanTxid, isManual, networkFor, networksOf, notYetKey, plainAmount, txidPattern } from '../pay'
import { openInvoice } from '../telegram'
import { json, noTelegram, signedIn, telegram } from './helpers'

afterEach(() => {
  vi.restoreAllMocks()
  noTelegram()
})

describe('payment helpers', () => {
  it('knows the manual methods', () => {
    for (const m of ['manual_card', 'manual_crypto', 'manual_zarinpal']) expect(isManual(m)).toBe(true)
    for (const m of ['stars', 'zarinpal', 'idpay', 'nextpay', 'usdt_trc20_auto', 'wallet', '', 'manual_']) expect(isManual(m)).toBe(false)
  })

  it('says what is not there yet in the words of the method, and warns a Stars payer not to pay twice', () => {
    expect(notYetKey('stars')).toBe('stars.not_yet')
    expect(notYetKey('manual_card')).toBe('manual.still_waiting')
    expect(notYetKey('manual_crypto')).toBe('manual.still_waiting')
    expect(notYetKey('zarinpal')).toBe('pay.not_yet')
    expect(messages.en['stars.not_yet']).toContain('do not pay twice')
    expect(messages.fa['stars.not_yet']).toBeTruthy()
  })

  it('copies a card number as digits', () => {
    expect(cardDigits('6037-9911-2222-3333')).toBe('6037991122223333')
    expect(cardDigits('6037 9911 2222 3333')).toBe('6037991122223333')
    expect(cardDigits('IR06 0540 1234')).toBe('IR0605401234')
  })

  it('writes an amount as a plain decimal', () => {
    expect(plainAmount(2_500_000, 6)).toBe('2.5')
    expect(plainAmount(1_000_000, 6)).toBe('1')
    expect(plainAmount(1, 6)).toBe('0.000001')
    expect(plainAmount(3_340_000, 6)).toBe('3.34')
    expect(plainAmount(150_000, 0)).toBe('150000')
    expect(plainAmount(0, 6)).toBe('0')
  })

  it('reads a transaction hash', () => {
    const hash = 'ab12'.repeat(16)
    expect(txidPattern.test(hash)).toBe(true)
    expect(txidPattern.test('0x' + hash)).toBe(true)
    for (const bad of ['', hash.slice(1), hash + '0', 'g'.repeat(64), '0x' + hash.slice(2), ' ' + hash]) expect(txidPattern.test(bad)).toBe(false)
    expect(cleanTxid(' ab12 ab12\n' + 'ab12'.repeat(14) + ' ')).toBe(hash)
  })

  it('picks the network a hash is for', () => {
    expect(networksOf({ usdt_trc20: 'T', usdt_erc20: '0x' })).toEqual(['TRC20', 'ERC20'])
    expect(networksOf({ usdt_erc20: '0x' })).toEqual(['ERC20'])
    expect(networksOf({})).toEqual([])
    expect(networkFor('ab12', ['TRC20', 'ERC20'])).toBe('TRC20')
    expect(networkFor('0xab12', ['TRC20', 'ERC20'])).toBe('ERC20')
    expect(networkFor('ab12', ['ERC20'])).toBe('ERC20') // the only one the store takes
    expect(networkFor('0xab12', ['TRC20'])).toBe('TRC20')
    expect(networkFor('ab12', [])).toBe('')
  })
})

describe('proofs', () => {
  beforeEach(() => resetSession())

  it('sends a receipt as a form: the image, the number, and no JSON type', async () => {
    const calls = signedIn(() => ({ intent_id: 'i1', status: 'confirming' }))
    const image = new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 1, 2, 3])], { type: 'image/png' })
    const out = await api.sendReceipt('i1', image, 'receipt.png', '998877')
    expect(out).toEqual({ intent_id: 'i1', status: 'confirming' })
    expect(calls).toHaveLength(1)
    const c = calls[0]
    expect(c.method).toBe('POST')
    expect(c.path).toBe('/payments/i1/proof')
    const headers = c.init.headers as Record<string, string>
    expect(headers['Content-Type']).toBeUndefined() // the browser writes it, with the boundary
    expect(headers.Authorization).toBe('Bearer tok1')
    const body = c.init.body as FormData
    expect(body).toBeInstanceOf(FormData)
    expect(body.get('reference')).toBe('998877')
    const part = body.get('receipt') as File
    expect(part.name).toBe('receipt.png')
    expect(part.size).toBe(7)
    expect(part.type).toBe('image/png')
  })

  it('leaves the number out when there is none', async () => {
    const calls = signedIn(() => ({ intent_id: 'i1', status: 'confirming' }))
    await api.sendReceipt('i1', new Blob([new Uint8Array([1])]), 'receipt.png', '')
    expect((calls[0].init.body as FormData).has('reference')).toBe(false)
  })

  it('sends a hash as JSON', async () => {
    const calls = signedIn(() => ({ intent_id: 'i2', status: 'confirming' }))
    await api.sendTxid('i2', 'ab12'.repeat(16), 'TRC20')
    expect(calls[0].path).toBe('/payments/i2/proof')
    expect((calls[0].init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ txid: 'ab12'.repeat(16), network: 'TRC20' })
  })

  it('sends the same form again after one expired session is renewed', async () => {
    let sessions = 0
    const bodies: unknown[] = []
    window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      if (String(url).endsWith('/auth')) {
        sessions++
        return json(200, { user: {}, wallets: [], brand: {}, top_up: {}, features: {}, token: 'tok' + sessions })
      }
      bodies.push(init?.body)
      const h = (init?.headers ?? {}) as Record<string, string>
      return h.Authorization === 'Bearer tok1' ? json(401, { error: 'unauthorized', message: 'x' }) : json(200, { intent_id: 'i', status: 'confirming' })
    })
    await api.sendReceipt('i', new Blob([new Uint8Array([1, 2, 3])]), 'receipt.png', '1')
    expect(sessions).toBe(2)
    expect(bodies).toHaveLength(2)
    for (const b of bodies) expect((b as FormData).get('receipt')).toBeInstanceOf(Blob)
  })

  it('words every failure the proof call can answer, in both languages', () => {
    const codes = [
      'method_off', 'too_large', 'bad_type', 'empty', 'no_receipt', 'one_receipt', 'reference_required', 'bad_txid',
      'already_submitted', 'payment_closed', 'duplicate', 'txid_announced', 'chat_closed', 'invoice_refused', 'busy', 'bot_unavailable',
    ]
    for (const code of codes) {
      const key = errorKey(new ApiError(409, code, ''))
      expect(key, code).toBe(`err.${code}`)
      expect(messages.fa[key], `fa ${key}`).toBeTruthy()
      expect(messages.en[key], `en ${key}`).toBeTruthy()
    }
    expect(errorKey(new ApiError(500, 'something_else', ''))).toBe('err.unknown')
    // payments or the store behind core away for a moment: said as busy, not as an unknown failure
    expect(errorKey(new ApiError(503, 'unavailable', ''))).toBe('err.busy')
  })

  it('has a name for each payment method in both languages', () => {
    for (const m of ['zarinpal', 'idpay', 'nextpay', 'usdt_trc20_auto', 'stars', 'manual_card', 'manual_crypto', 'manual_zarinpal']) {
      expect(messages.fa['method.' + m], `fa ${m}`).toBeTruthy()
      expect(messages.en['method.' + m], `en ${m}`).toBeTruthy()
    }
  })
})

describe('openInvoice', () => {
  it('opens the invoice in Telegram and passes on how it ended', () => {
    const open = vi.fn()
    telegram({ openInvoice: open, isVersionAtLeast: (v: string) => v === '6.1' })
    const done = vi.fn()
    expect(openInvoice('https://t.me/$abc', done)).toBe(true)
    expect(open).toHaveBeenCalledWith('https://t.me/$abc', done)
  })

  it('says it cannot where Telegram is too old, is not there, or fails', () => {
    const done = vi.fn()
    expect(openInvoice('https://t.me/$abc', done)).toBe(false) // not in Telegram
    const open = vi.fn()
    telegram({ openInvoice: open, isVersionAtLeast: () => false })
    expect(openInvoice('https://t.me/$abc', done)).toBe(false)
    expect(open).not.toHaveBeenCalled()
    telegram({}) // no such method
    expect(openInvoice('https://t.me/$abc', done)).toBe(false)
    telegram({
      openInvoice: () => {
        throw new Error('WebAppMethodUnsupported')
      },
    })
    expect(openInvoice('https://t.me/$abc', done)).toBe(false)
  })

  it('opens it where the version cannot be asked', () => {
    const open = vi.fn()
    telegram({ openInvoice: open })
    expect(openInvoice('https://t.me/$abc', vi.fn())).toBe(true)
  })
})
