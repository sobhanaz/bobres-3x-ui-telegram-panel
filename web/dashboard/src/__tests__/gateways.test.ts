import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref, type Component } from 'vue'
import PrimeVue from 'primevue/config'
import fa from '../locales/fa.json'
import en from '../locales/en.json'
import { ApiError } from '../api'
import { dateTime } from '../format'
import {
  apiKeyMax,
  apiKeyValid,
  blockedByOpen,
  checkErrors,
  depositKey,
  depositNetwork,
  depositNoteMax,
  healthState,
  isKeyedProvider,
  keyedBlockedByOpen,
  keyedBody,
  keyedDirty,
  keyedFormOf,
  keyedHasSandbox,
  keyedOrder,
  keyedProblems,
  keyedProviders,
  keyedSecretValid,
  keyedTestBody,
  keyedUsesPin,
  merchantValid,
  methodCards,
  methods,
  methodState,
  normalizeKeyedSecret,
  normalizeMerchant,
  normalizePin,
  openPayments,
  overviewGatewayState,
  pinFieldMax,
  pinValid,
  proxyValid,
  reportRange,
  reportRows,
  resolveBody,
  sandboxPin,
  tonAddressError,
  tonApiKeyValid,
  tonAuto,
  tonBlockedByOpen,
  tonBody,
  tonDirty,
  tonFormOf,
  tonPriceState,
  tonProblems,
  tonTestBody,
  tonUnmatchedReasons,
  tonviewerUrl,
  tronscanUrl,
  unmatchedReason,
  unmatchedReasons,
  usdtAddressError,
  usdtBlockedByOpen,
  usdtBody,
  usdtDirty,
  usdtFormOf,
  usdtProblems,
  usdtTestBody,
  zarinpalBody,
  zarinpalDirty,
  zarinpalFormOf,
  zarinpalProblems,
  zarinpalTestBody,
} from '../gateways'
import type { GatewayConfig, GatewayReport, SettingItem } from '../types'

const t0 = 1_760_000_000
const merchant = '1a2b3c4d-0000-4000-8000-00000000ab12'
// An Aqayepardakht PIN: capitals, small letters and symbols, nothing like an id.
const pin = 'AbC123xYz-Q_9'
const config = (over: Partial<GatewayConfig> = {}): GatewayConfig => ({
  provider: 'zarinpal',
  enabled: false,
  configured: true,
  sandbox: false,
  merchant_hint: 'ab12',
  proxy_set: true,
  key_missing: false,
  health: { healthy: true, open: 2, oldest_open_at: t0 - 3600, last_paid_at: t0 - 600, last_error_at: null, last_error: '' },
  ...over,
})
// Real TRON addresses (base58check): the store's, a new one, a customer's.
const store = 'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r'
const newAddress = 'TSbS3MrjbYDWGmAjvqzwTVCpiqsu7uR9Fb'
const sender = 'TK6wsfvEmv3S45xFxQXE2DqEtu8SVAXTut'
// the store's address with one character wrong
const typo = store.slice(0, 10) + (store[10] === 'z' ? 'y' : 'z') + store.slice(11)
const apiKey = '11111111-2222-4333-8444-555555555555'
const usdtConfig = (over: Partial<GatewayConfig> = {}): GatewayConfig => ({
  provider: 'usdt_trc20_auto',
  enabled: true,
  configured: true,
  offered: true,
  problem: '',
  sandbox: false,
  merchant_hint: '',
  proxy_set: false,
  address: store,
  key_set: true,
  key_missing: false,
  health: { healthy: true, open: 1, oldest_open_at: t0 - 600, last_paid_at: t0 - 300, last_error_at: null, last_error: '' },
  ...over,
})
// Real TON addresses (public data from TON's documentation): the store's, in the forms a wallet shows, a new one, and a
// customer's. The stored (canonical) form is the non-bounceable one, UQ…
const tonStoreRaw = '0:ed1691307050047117b998b561d8de82d31fbf84910ced6eb5fc92e7485ef8a7'
const tonStoreEQ = 'EQDtFpEwcFAEcRe5mLVh2N6C0x-_hJEM7W61_JLnSF74p4q2'
const tonStore = 'UQDtFpEwcFAEcRe5mLVh2N6C0x-_hJEM7W61_JLnSF74p9dz'
const tonNewRaw = '0:83dfd552e63729b472fcbcc8c45ebcc6691702558b68ec7527e1ba403a0f31a8'
const tonNewEQ = 'EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N'
const tonNew = 'UQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqEBI'
// the store's address with one character wrong
const tonTypo = tonStore.slice(0, 20) + (tonStore[20] === 'Z' ? 'Y' : 'Z') + tonStore.slice(21)
// A friendly address of any flag byte and workchain with a sound checksum, built apart from the code under test.
function tonFriendly(tag: number, workchain: number): string {
  const raw = Buffer.alloc(36)
  raw[0] = tag
  raw[1] = workchain
  Buffer.from('ab'.repeat(32), 'hex').copy(raw, 2)
  let crc = 0
  for (const b of raw.subarray(0, 34)) {
    crc ^= b << 8
    for (let i = 0; i < 8; i++) crc = crc & 0x8000 ? ((crc << 1) ^ 0x1021) & 0xffff : (crc << 1) & 0xffff
  }
  raw.writeUInt16BE(crc, 34)
  return raw.toString('base64url')
}
// a customer's wallet
const tonSender = tonFriendly(0x51, 0)
const tonTestnet = tonFriendly(0x51 | 0x80, 0) // 0Q…
const tonMasterchain = tonFriendly(0x11, 0xff)
const tonKey = 'A1b2C3d4_e5F6-g7H8i9J0k1L2m3N4o5'
const tonConfig = (over: Partial<GatewayConfig> = {}): GatewayConfig => ({
  provider: 'ton_auto',
  enabled: true,
  configured: true,
  offered: true,
  problem: '',
  sandbox: false,
  merchant_hint: '',
  proxy_set: false,
  address: tonStore,
  key_set: true,
  key_missing: false,
  health: { healthy: true, open: 1, oldest_open_at: t0 - 600, last_paid_at: t0 - 300, last_error_at: null, last_error: '', last_scan_at: t0 - 20, last_scan_ok_at: t0 - 20, unmatched_count: 3 },
  // read ten minutes ago, by the page's own clock
  price: { usdt_per_ton: '3.2145', source: 'binance', at: Math.floor(Date.now() / 1000) - 600, fresh: true },
  ...over,
})
const setting = (key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem => ({
  key, group: 'payments', kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra,
})
const toggle = (key: string, value = '') => setting(key, 'bool', { default: 'true', value })
const payments = (over: Record<string, string> = {}): SettingItem[] =>
  [
    setting('payments.card_number', 'text', { value: '6037 9911 2345 6789' }),
    setting('payments.card_holder', 'text', { value: 'علی رضایی' }),
    setting('payments.usdt_trc20', 'text'),
    setting('payments.usdt_erc20', 'text'),
    setting('payments.usdt_rate', 'int', { min: 1, max: 1e12 }),
    setting('payments.stars_rate', 'int', { min: 1, max: 1e9, value: '1500' }),
    setting('payments.zarinpal_link', 'url', { value: 'https://zarinp.al/shop' }),
    toggle('payments.manual_card.enabled'),
    toggle('payments.manual_crypto.enabled'),
    toggle('payments.manual_zarinpal.enabled', 'false'),
    toggle('payments.stars.enabled'),
  ].map((i) => (i.key in over ? { ...i, value: over[i.key] } : i))

describe('idpay and nextpay form', () => {
  const idpay = (over: Partial<GatewayConfig> = {}) => config({ provider: 'idpay', merchant_hint: '', ...over })
  const nextpay = (over: Partial<GatewayConfig> = {}) => config({ provider: 'nextpay', merchant_hint: '', ...over })
  it('knows the keyed providers and that only IDPay has a sandbox', () => {
    expect(isKeyedProvider('idpay')).toBe(true)
    expect(isKeyedProvider('nextpay')).toBe(true)
    expect(isKeyedProvider('zarinpal')).toBe(false)
    expect(isKeyedProvider('usdt_trc20_auto')).toBe(false)
    expect(keyedHasSandbox('idpay')).toBe(true)
    expect(keyedHasSandbox('nextpay')).toBe(false)
  })
  it('starts from the stored switches and empty secrets; NextPay never shows a sandbox', () => {
    expect(keyedFormOf(idpay({ enabled: true, sandbox: true }))).toEqual({ enabled: true, sandbox: true, apiKey: '', proxy: '', clearProxy: false })
    expect(keyedFormOf(nextpay({ sandbox: true })).sandbox).toBe(false)
  })
  it('checks the form as core will', () => {
    const c = idpay()
    const f = keyedFormOf(c)
    expect(keyedProblems(f, c)).toEqual([])
    expect(keyedProblems({ ...f, apiKey: 'abc', proxy: 'ftp://x' }, c)).toEqual(['api_key', 'proxy'])
    const blank = idpay({ configured: false })
    expect(keyedProblems({ ...f, enabled: true }, blank)).toEqual(['enabled'])
    expect(keyedProblems({ ...f, enabled: true, apiKey: merchant }, blank)).toEqual([])
    expect(keyedProblems({ ...f, sandbox: true }, idpay({ sandbox_allowed: false }))).toEqual(['sandbox'])
  })
  it('knows when something would change', () => {
    const c = idpay()
    const f = keyedFormOf(c)
    expect(keyedDirty(f, c)).toBe(false)
    expect(keyedDirty({ ...f, enabled: true }, c)).toBe(true)
    expect(keyedDirty({ ...f, apiKey: ' ' }, c)).toBe(false)
    expect(keyedDirty({ ...f, apiKey: merchant }, c)).toBe(true)
    expect(keyedDirty({ ...f, clearProxy: true }, c)).toBe(true)
  })
  it('builds the bodies: the key normalized, no sandbox for NextPay, "" keeps a secret', () => {
    const f = keyedFormOf(idpay())
    expect(keyedBody(f, 'idpay')).toEqual({ enabled: false, sandbox: false, api_key: '', proxy: '', clear_proxy: false })
    expect(keyedBody({ ...f, sandbox: true, apiKey: ' ' + merchant.toUpperCase() + ' ', proxy: ' http://p.example.com:3128 ' }, 'idpay')).toEqual({
      enabled: false, sandbox: true, api_key: merchant, proxy: 'http://p.example.com:3128', clear_proxy: false,
    })
    expect(keyedBody({ ...f, sandbox: true }, 'nextpay').sandbox).toBe(false)
    expect(keyedBody({ ...f, proxy: 'http://p.example.com:3128', clearProxy: true }, 'nextpay')).toMatchObject({ proxy: '', clear_proxy: true })
    expect(keyedTestBody({ ...f, enabled: true, apiKey: merchant }, 'idpay')).toEqual({ sandbox: false, api_key: merchant, proxy: '', clear_proxy: false })
  })
  it('refuses a new key or sandbox while payments are open', () => {
    const c = idpay()
    const f = keyedFormOf(c)
    expect(keyedBlockedByOpen(f, c)).toBe(false)
    expect(keyedBlockedByOpen({ ...f, apiKey: merchant }, c)).toBe(true)
    expect(keyedBlockedByOpen({ ...f, sandbox: true }, c)).toBe(true)
    expect(keyedBlockedByOpen({ ...f, apiKey: merchant }, idpay({ health: { ...c.health!, open: 0 } }))).toBe(false)
    expect(keyedBlockedByOpen({ ...f, apiKey: merchant }, idpay({ configured: false }))).toBe(false)
  })
  it('names every keyed gateway in both languages', () => {
    for (const lang of [en, fa]) {
      for (const p of keyedProviders) {
        expect(lang.payment.provider[p], p).toBeTruthy()
        expect((lang.gateway as Record<string, unknown>)[p + '_desc'], p).toBeTruthy()
      }
    }
  })
  it('keeps IDPay and NextPay on the API key rule, whatever Aqayepardakht does', () => {
    // a key is an id: lowercased, and only a 36-character one passes
    expect(keyedUsesPin('idpay')).toBe(false)
    expect(keyedUsesPin('nextpay')).toBe(false)
    for (const p of ['idpay', 'nextpay']) {
      expect(keyedSecretValid(merchant.toUpperCase(), p), p).toBe(true)
      expect(keyedSecretValid('AbCd1234', p), p).toBe(false)
      expect(normalizeKeyedSecret(' ' + merchant.toUpperCase() + ' ', p), p).toBe(merchant)
    }
    expect(keyedBody({ ...keyedFormOf(idpay()), apiKey: merchant.toUpperCase() }, 'nextpay').api_key).toBe(merchant)
    // the PIN "sandbox" is no business of theirs
    expect(keyedProblems({ ...keyedFormOf(idpay()), apiKey: 'sandbox' }, idpay({ sandbox_allowed: false }))).toEqual(['api_key'])
  })
})

// Aqayepardakht: the credential is the gateway's PIN, which is no id (any 4 to
// 100 printable characters, case counts), and the test system is the PIN
// "sandbox" itself, so there is no sandbox switch.
describe('aqayepardakht form', () => {
  const aqayepardakht = (over: Partial<GatewayConfig> = {}) => config({ provider: 'aqayepardakht', merchant_hint: '', ...over })
  const proxy = 'http://p.example.com:3128'

  it('is a keyed gateway whose card comes after NextPay, with a PIN and no sandbox switch', () => {
    expect(isKeyedProvider('aqayepardakht')).toBe(true)
    expect(keyedUsesPin('aqayepardakht')).toBe(true)
    expect(keyedHasSandbox('aqayepardakht')).toBe(false)
    expect(keyedOrder('idpay')).toBeLessThan(keyedOrder('nextpay'))
    expect(keyedOrder('nextpay')).toBeLessThan(keyedOrder('aqayepardakht'))
    expect(keyedOrder('zarinpal')).toBe(-1)
  })
  it('reads a PIN as 4 to 100 printable characters without spaces', () => {
    expect(pinValid('abcd')).toBe(true)
    expect(pinValid('abc')).toBe(false)
    expect(pinValid('a'.repeat(100))).toBe(true)
    expect(pinValid('a'.repeat(101))).toBe(false)
    expect(pinValid(pin)).toBe(true)
    // every printable ASCII character but the space
    expect(pinValid('!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~')).toBe(true)
    // trimmed, but a space inside is not a PIN
    expect(pinValid('  ' + pin + '\n')).toBe(true)
    expect(pinValid('ab cd')).toBe(false)
    expect(pinValid('ab\u00a0cd')).toBe(false)
    expect(pinValid('')).toBe(false)
    expect(pinValid('    ')).toBe(false)
    // letters of another script, and digits in another script, are not converted: they are refused
    expect(pinValid('پین-درگاه')).toBe(false)
    expect(pinValid('۱۲۳۴۵۶')).toBe(false)
    // what the field takes is more than a PIN may be, so a value that is too long shows as invalid
    expect(pinFieldMax).toBeGreaterThan(100)
  })
  it('keeps the case of a PIN, and still lowercases the other gateways\' keys', () => {
    expect(normalizePin('  AbC-1_x \t')).toBe('AbC-1_x')
    expect(normalizeKeyedSecret(' ' + pin + ' ', 'aqayepardakht')).toBe(pin)
    expect(normalizeKeyedSecret(' ' + merchant.toUpperCase() + ' ', 'idpay')).toBe(merchant)
    // a PIN is not an id: the 36-character rule would refuse it
    expect(merchantValid(pin)).toBe(false)
    expect(keyedSecretValid(pin, 'aqayepardakht')).toBe(true)
    expect(keyedSecretValid(pin, 'idpay')).toBe(false)
    expect(keyedSecretValid(pin, 'nextpay')).toBe(false)
  })
  it('starts from the stored switches and an empty PIN, never with the sandbox on', () => {
    expect(keyedFormOf(aqayepardakht({ enabled: true }))).toEqual({ enabled: true, sandbox: false, apiKey: '', proxy: '', clearProxy: false })
    // the sandbox core reports (the stored PIN is "sandbox") is not a switch of this form
    expect(keyedFormOf(aqayepardakht({ sandbox: true })).sandbox).toBe(false)
  })
  it('checks the form as payments will', () => {
    const c = aqayepardakht()
    const f = keyedFormOf(c)
    expect(keyedProblems(f, c)).toEqual([])
    expect(keyedProblems({ ...f, apiKey: pin }, c)).toEqual([])
    expect(keyedProblems({ ...f, apiKey: 'abc', proxy: 'ftp://x' }, c)).toEqual(['api_key', 'proxy'])
    expect(keyedProblems({ ...f, apiKey: 'with space' }, c)).toEqual(['api_key'])
    // turning it on needs a PIN, stored or typed
    const blank = aqayepardakht({ configured: false })
    expect(keyedProblems({ ...f, enabled: true }, blank)).toEqual(['enabled'])
    expect(keyedProblems({ ...f, enabled: true, apiKey: pin }, blank)).toEqual([])
    expect(keyedProblems({ ...f, enabled: true }, c)).toEqual([])
  })
  it('refuses the PIN "sandbox" where production does, and only there', () => {
    expect(sandboxPin).toBe('sandbox')
    const prod = aqayepardakht({ sandbox_allowed: false })
    const f = keyedFormOf(prod)
    expect(keyedProblems({ ...f, apiKey: 'sandbox' }, prod)).toEqual(['sandbox'])
    expect(keyedProblems({ ...f, apiKey: '  sandbox ' }, prod)).toEqual(['sandbox'])
    // it is the whole PIN that counts, and case counts
    expect(keyedProblems({ ...f, apiKey: 'sandbox1' }, prod)).toEqual([])
    expect(keyedProblems({ ...f, apiKey: 'Sandbox' }, prod)).toEqual([])
    expect(keyedProblems({ ...f, apiKey: pin }, prod)).toEqual([])
    // typed nothing: a stored sandbox PIN is the problem core names, not a field
    expect(keyedProblems(f, aqayepardakht({ sandbox_allowed: false, sandbox: true }))).toEqual([])
    // elsewhere it is the test PIN, as the gateway has it
    expect(keyedProblems({ ...f, apiKey: 'sandbox' }, aqayepardakht({ sandbox_allowed: true }))).toEqual([])
    expect(keyedProblems({ ...f, apiKey: 'sandbox' }, aqayepardakht())).toEqual([])
  })
  it('knows when something would change', () => {
    // the stored PIN is "sandbox": core reports sandbox, which is no switch to change
    const c = aqayepardakht({ sandbox: true })
    const f = keyedFormOf(c)
    expect(keyedDirty(f, c)).toBe(false)
    expect(keyedDirty({ ...f, enabled: true }, c)).toBe(true)
    expect(keyedDirty({ ...f, apiKey: ' ' }, c)).toBe(false)
    expect(keyedDirty({ ...f, apiKey: pin }, c)).toBe(true)
    expect(keyedDirty({ ...f, proxy }, c)).toBe(true)
    expect(keyedDirty({ ...f, clearProxy: true }, c)).toBe(true)
  })
  it('builds the bodies: the PIN as typed but trimmed, never a sandbox, "" keeps a secret', () => {
    const f = keyedFormOf(aqayepardakht())
    expect(keyedBody(f, 'aqayepardakht')).toEqual({ enabled: false, sandbox: false, api_key: '', proxy: '', clear_proxy: false })
    expect(keyedBody({ ...f, enabled: true, apiKey: ' ' + pin + ' ', proxy: ' ' + proxy + ' ' }, 'aqayepardakht')).toEqual({
      enabled: true, sandbox: false, api_key: pin, proxy, clear_proxy: false,
    })
    // a sandbox switch that was somehow on is not sent; the test PIN is a PIN like any other
    expect(keyedBody({ ...f, sandbox: true }, 'aqayepardakht').sandbox).toBe(false)
    expect(keyedBody({ ...f, apiKey: 'sandbox' }, 'aqayepardakht')).toMatchObject({ api_key: 'sandbox', sandbox: false })
    expect(keyedBody({ ...f, proxy, clearProxy: true }, 'aqayepardakht')).toMatchObject({ proxy: '', clear_proxy: true })
    // a test changes nothing: no switches but the sandbox, which is false
    expect(keyedTestBody({ ...f, enabled: true, apiKey: pin }, 'aqayepardakht')).toEqual({ sandbox: false, api_key: pin, proxy: '', clear_proxy: false })
    expect(keyedTestBody({ ...f, proxy }, 'aqayepardakht')).toEqual({ sandbox: false, api_key: '', proxy, clear_proxy: false })
  })
  it('refuses a new PIN while payments are open', () => {
    const c = aqayepardakht() // two open payments
    const f = keyedFormOf(c)
    expect(keyedBlockedByOpen(f, c)).toBe(false)
    expect(keyedBlockedByOpen({ ...f, enabled: true, proxy }, c)).toBe(false)
    expect(keyedBlockedByOpen({ ...f, apiKey: pin }, c)).toBe(true)
    expect(keyedBlockedByOpen({ ...f, apiKey: pin }, aqayepardakht({ health: { ...c.health!, open: 0 } }))).toBe(false)
    expect(keyedBlockedByOpen({ ...f, apiKey: pin }, aqayepardakht({ configured: false }))).toBe(false)
    // the sandbox core reports is not a change either
    const sandboxed = aqayepardakht({ sandbox: true })
    expect(keyedBlockedByOpen(keyedFormOf(sandboxed), sandboxed)).toBe(false)
  })
})

describe('zarinpal form', () => {
  it('reads a merchant id as payments does', () => {
    expect(normalizeMerchant(' 1A2B3C4D-0000-4000-8000-00000000AB12 ')).toBe(merchant)
    expect(normalizeMerchant('۱a2b3c4d-0000-4000-8000-00000000ab12')).toBe(merchant)
    expect(merchantValid(merchant.toUpperCase())).toBe(true)
    expect(merchantValid(merchant.slice(1))).toBe(false)
    expect(merchantValid(merchant.replace('a', 'g'))).toBe(false)
    expect(merchantValid('')).toBe(false)
  })
  it('takes an http(s) proxy, with a user and password too', () => {
    expect(proxyValid('http://user:pw@proxy.example.com:3128')).toBe(true)
    expect(proxyValid(' https://10.0.0.1:8443 ')).toBe(true)
    expect(proxyValid('socks5://proxy.example.com:1080')).toBe(false)
    expect(proxyValid('proxy.example.com:3128')).toBe(false)
    expect(proxyValid('http://proxy example.com')).toBe(false)
    expect(proxyValid('')).toBe(false)
  })
  it('starts from the stored switches and empty secrets', () => {
    expect(zarinpalFormOf(config({ enabled: true, sandbox: true }))).toEqual({ enabled: true, sandbox: true, merchant: '', proxy: '', clearProxy: false })
  })
  it('checks the form as core will', () => {
    const c = config()
    const f = zarinpalFormOf(c)
    expect(zarinpalProblems(f, c)).toEqual([])
    expect(zarinpalProblems({ ...f, merchant: 'abc', proxy: 'ftp://x' }, c)).toEqual(['merchant_id', 'proxy'])
    // turning it on needs a merchant id, stored or typed
    const blank = config({ configured: false, merchant_hint: '' })
    expect(zarinpalProblems({ ...f, enabled: true }, blank)).toEqual(['enabled'])
    expect(zarinpalProblems({ ...f, enabled: true, merchant }, blank)).toEqual([])
    expect(zarinpalProblems({ ...f, enabled: true }, c)).toEqual([])
    // production refuses the sandbox
    expect(zarinpalProblems({ ...f, sandbox: true }, config({ sandbox_allowed: false }))).toEqual(['sandbox'])
    expect(zarinpalProblems({ ...f, sandbox: true }, c)).toEqual([])
  })
  it('knows when something would change', () => {
    const c = config()
    const f = zarinpalFormOf(c)
    expect(zarinpalDirty(f, c)).toBe(false)
    expect(zarinpalDirty({ ...f, enabled: true }, c)).toBe(true)
    expect(zarinpalDirty({ ...f, merchant: ' ' }, c)).toBe(false)
    expect(zarinpalDirty({ ...f, merchant }, c)).toBe(true)
    expect(zarinpalDirty({ ...f, clearProxy: true }, c)).toBe(true)
  })
  it('sends empty secrets to keep them, and never a proxy being removed', () => {
    const f = zarinpalFormOf(config())
    expect(zarinpalBody(f)).toEqual({ enabled: false, sandbox: false, merchant_id: '', proxy: '', clear_proxy: false })
    expect(zarinpalBody({ ...f, merchant: ' ' + merchant.toUpperCase() + ' ', proxy: ' http://p.example.com:3128 ' })).toEqual({
      enabled: false, sandbox: false, merchant_id: merchant, proxy: 'http://p.example.com:3128', clear_proxy: false,
    })
    expect(zarinpalBody({ ...f, proxy: 'http://p.example.com:3128', clearProxy: true })).toMatchObject({ proxy: '', clear_proxy: true })
    // a test changes nothing: no switches but the sandbox
    expect(zarinpalTestBody({ ...f, enabled: true, merchant })).toEqual({ sandbox: false, merchant_id: merchant, proxy: '', clear_proxy: false })
  })
  it('knows what open payments block', () => {
    const c = config() // two open payments
    const f = zarinpalFormOf(c)
    expect(blockedByOpen(f, c)).toBe(false)
    expect(blockedByOpen({ ...f, enabled: true, proxy: 'http://p.example.com:3128' }, c)).toBe(false)
    expect(blockedByOpen({ ...f, merchant }, c)).toBe(true)
    expect(blockedByOpen({ ...f, sandbox: true }, c)).toBe(true)
    expect(blockedByOpen({ ...f, merchant }, config({ health: { ...c.health!, open: 0 } }))).toBe(false)
    expect(blockedByOpen({ ...f, merchant }, config({ configured: false }))).toBe(false)
  })
  it('reads how many payments block a new merchant id', () => {
    expect(openPayments(new ApiError(409, 'state', '2 open', { open: 2 }))).toBe(2)
    expect(openPayments(new ApiError(409, 'state', 'other'))).toBeNull()
    expect(openPayments(new ApiError(400, 'invalid', 'x', { open: 2 }))).toBeNull()
    expect(openPayments(new Error('x'))).toBeNull()
  })
  it('sums up health', () => {
    expect(healthState(config().health)).toBe('ok')
    expect(healthState({ ...config().health!, healthy: false, last_error: 'timeout', last_error_at: t0 })).toBe('error')
    expect(healthState({ ...config().health!, last_paid_at: null })).toBe('idle')
    expect(healthState(null)).toBe('idle')
  })
})

describe('automatic USDT form', () => {
  it('starts from the stored switch and address, and an empty key', () => {
    expect(usdtFormOf(usdtConfig({ proxy_set: true }))).toEqual({ enabled: true, address: store, apiKey: '', clearApiKey: false, proxy: '', clearProxy: false })
    expect(usdtFormOf(usdtConfig({ address: undefined, enabled: false }))).toEqual({ enabled: false, address: '', apiKey: '', clearApiKey: false, proxy: '', clearProxy: false })
  })
  it('checks the address and the key as payments will', () => {
    const c = usdtConfig()
    const f = usdtFormOf(c)
    expect(usdtProblems(f, c)).toEqual([])
    expect(usdtAddressError({ ...f, address: typo }, c)).toBe('checksum')
    expect(usdtAddressError({ ...f, address: 'T123' }, c)).toBe('format')
    expect(usdtAddressError({ ...f, address: '0x' + 'a'.repeat(40) }, c)).toBe('format')
    // a stored address is replaced, never removed
    expect(usdtAddressError({ ...f, address: '  ' }, c)).toBe('required')
    expect(usdtProblems({ ...f, address: typo, apiKey: 'not a key' }, c)).toEqual(['address', 'api_key'])
    expect(usdtProblems({ ...f, address: ' ' + newAddress + '\u200f', apiKey: ' ' + apiKey + ' ' }, c)).toEqual([])
    // turning it on needs an address, stored or typed
    const blank = usdtConfig({ configured: false, enabled: false, address: '', key_set: false })
    expect(usdtAddressError(usdtFormOf(blank), blank)).toBe('')
    expect(usdtProblems({ ...usdtFormOf(blank), enabled: true }, blank)).toEqual(['enabled'])
    expect(usdtProblems({ ...usdtFormOf(blank), enabled: true, address: store }, blank)).toEqual([])
    expect(apiKeyValid(apiKey)).toBe(true)
    expect(apiKeyValid('short')).toBe(false)
    expect(apiKeyValid('a b c d e f g h')).toBe(false)
    // as core and payments take it: letters, digits and dashes, 8 to 128
    expect(apiKeyMax).toBe(128)
    expect(apiKeyValid('a'.repeat(128))).toBe(true)
    expect(apiKeyValid('a'.repeat(129))).toBe(false)
    expect(apiKeyValid('abcd_efgh')).toBe(false)
    expect(usdtProblems({ ...f, apiKey: 'abcd_efgh_ijkl' }, c)).toEqual(['api_key'])
    // the proxy TronGrid is called through, as Zarinpal's
    expect(usdtProblems({ ...f, proxy: 'socks5://proxy.example.com:1080' }, c)).toEqual(['proxy'])
    expect(usdtProblems({ ...f, proxy: ' http://user:pw@proxy.example.com:3128 ' }, c)).toEqual([])
  })
  it('knows when something would change', () => {
    const c = usdtConfig()
    const f = usdtFormOf(c)
    expect(usdtDirty(f, c)).toBe(false)
    expect(usdtDirty({ ...f, address: ' ' + store + ' ' }, c)).toBe(false)
    expect(usdtDirty({ ...f, address: newAddress }, c)).toBe(true)
    expect(usdtDirty({ ...f, enabled: false }, c)).toBe(true)
    expect(usdtDirty({ ...f, apiKey: ' ' }, c)).toBe(false)
    expect(usdtDirty({ ...f, apiKey }, c)).toBe(true)
    expect(usdtDirty({ ...f, clearApiKey: true }, c)).toBe(true)
    expect(usdtDirty({ ...f, proxy: ' ' }, c)).toBe(false)
    expect(usdtDirty({ ...f, proxy: 'http://p.example.com:3128' }, c)).toBe(true)
    expect(usdtDirty({ ...f, clearProxy: true }, c)).toBe(true)
  })
  it('sends the whole address, empty secrets to keep them, and never a secret being removed', () => {
    const f = usdtFormOf(usdtConfig())
    const keep = { proxy: '', clear_proxy: false }
    expect(usdtBody(f)).toEqual({ enabled: true, address: store, api_key: '', clear_api_key: false, ...keep })
    expect(usdtBody({ ...f, address: '\u2066' + newAddress + ' \u2069', apiKey: ' ' + apiKey })).toEqual({ enabled: true, address: newAddress, api_key: apiKey, clear_api_key: false, ...keep })
    expect(usdtBody({ ...f, apiKey, clearApiKey: true })).toEqual({ enabled: true, address: store, api_key: '', clear_api_key: true, ...keep })
    expect(usdtBody({ ...f, proxy: ' http://p.example.com:3128 ' })).toMatchObject({ proxy: 'http://p.example.com:3128', clear_proxy: false })
    expect(usdtBody({ ...f, proxy: 'http://p.example.com:3128', clearProxy: true })).toMatchObject({ proxy: '', clear_proxy: true })
    // a test changes nothing: no switch
    expect(usdtTestBody({ ...f, enabled: false, apiKey })).toEqual({ address: store, api_key: apiKey, clear_api_key: false, ...keep })
    expect(usdtTestBody({ ...f, proxy: 'http://p.example.com:3128' })).toEqual({ address: store, api_key: '', clear_api_key: false, proxy: 'http://p.example.com:3128', clear_proxy: false })
    expect(usdtTestBody({ ...f, clearProxy: true })).toEqual({ address: store, api_key: '', clear_api_key: false, proxy: '', clear_proxy: true })
  })
  it('knows that open payments block a new address', () => {
    const c = usdtConfig() // one open payment
    const f = usdtFormOf(c)
    expect(usdtBlockedByOpen(f, c)).toBe(false)
    expect(usdtBlockedByOpen({ ...f, enabled: false, apiKey, address: ' ' + store }, c)).toBe(false)
    expect(usdtBlockedByOpen({ ...f, address: newAddress }, c)).toBe(true)
    expect(usdtBlockedByOpen({ ...f, address: newAddress }, usdtConfig({ health: { ...c.health!, open: 0 } }))).toBe(false)
    expect(usdtBlockedByOpen({ ...f, address: newAddress }, usdtConfig({ address: '' }))).toBe(false)
  })
  it('links a transaction to Tronscan only when it is one', () => {
    expect(tronscanUrl('AB'.repeat(32))).toBe('https://tronscan.org/#/transaction/' + 'ab'.repeat(32))
    expect(tronscanUrl('ab'.repeat(31))).toBe('')
    expect(tronscanUrl('javascript:alert(1)')).toBe('')
  })
  it('says why a deposit matched nothing in the staff member’s language', () => {
    for (const code of unmatchedReasons) expect(unmatchedReason({ reason: 'English words', reason_code: code })).toEqual({ key: 'gateway.usdt.reason.' + code })
    // a reason this dashboard does not know: core's words, or the code itself
    expect(unmatchedReason({ reason: 'something new', reason_code: 'new_code' })).toEqual({ raw: 'something new' })
    expect(unmatchedReason({ reason_code: 'new_code' })).toEqual({ raw: 'new_code' })
    expect(unmatchedReason({ reason: 'older core' })).toEqual({ raw: 'older core' })
    expect(unmatchedReason({})).toBeNull()
  })
  it('names a transfer by its transaction and its place in it', () => {
    const d = { txid: 'AB'.repeat(32), event_index: 2 }
    expect(depositKey(d)).toBe('ab'.repeat(32) + '#2')
    expect(depositKey({ ...d, event_index: 0 })).not.toBe(depositKey(d))
    expect(resolveBody(d, '  credited @ali by hand ')).toEqual({ txid: d.txid, event_index: 2, note: 'credited @ali by hand' })
    expect(resolveBody({ ...d, event_index: 0 }, '')).toEqual({ txid: d.txid, event_index: 0, note: '' })
    // payments counts the note in bytes (at most 500): 3 bytes a character at most
    expect(depositNoteMax * 3).toBeLessThanOrEqual(500)
  })
})

describe('automatic TON form', () => {
  it('starts from the stored switch and address, and an empty key', () => {
    expect(tonFormOf(tonConfig({ proxy_set: true }))).toEqual({ enabled: true, address: tonStore, apiKey: '', clearApiKey: false, proxy: '', clearProxy: false })
    expect(tonFormOf(tonConfig({ address: undefined, enabled: false }))).toEqual({ enabled: false, address: '', apiKey: '', clearApiKey: false, proxy: '', clearProxy: false })
    expect(tonAuto).toBe('ton_auto')
  })
  it('checks the address as core will: any form of a main network account on the basechain', () => {
    const c = tonConfig()
    const f = tonFormOf(c)
    expect(tonAddressError(f, c)).toBe('')
    for (const right of [tonStore, tonStoreEQ, tonStoreRaw, ' ' + tonStoreEQ + '‏\n', tonNewRaw]) expect(tonAddressError({ ...f, address: right }, c), right).toBe('')
    // four kinds of mistake, each its own
    expect(tonAddressError({ ...f, address: 'not an address' }, c)).toBe('format')
    expect(tonAddressError({ ...f, address: store }, c)).toBe('format') // a TRON address
    expect(tonAddressError({ ...f, address: tonTypo }, c)).toBe('checksum')
    expect(tonAddressError({ ...f, address: tonTestnet }, c)).toBe('testnet')
    expect(tonAddressError({ ...f, address: tonMasterchain }, c)).toBe('workchain')
    expect(tonAddressError({ ...f, address: '-1:' + 'ab'.repeat(32) }, c)).toBe('workchain')
    // a stored address is replaced, never removed
    expect(tonAddressError({ ...f, address: '  ' }, c)).toBe('required')
    expect(tonProblems({ ...f, address: tonTypo, apiKey: 'not a key!' }, c)).toEqual(['address', 'api_key'])
    expect(tonProblems({ ...f, address: ' ' + tonNewRaw + '‏', apiKey: ' ' + tonKey + ' ' }, c)).toEqual([])
    // turning it on needs an address, stored or typed
    const blank = tonConfig({ configured: false, enabled: false, address: '', key_set: false })
    expect(tonAddressError(tonFormOf(blank), blank)).toBe('')
    expect(tonProblems({ ...tonFormOf(blank), enabled: true }, blank)).toEqual(['enabled'])
    expect(tonProblems({ ...tonFormOf(blank), enabled: true, address: tonStoreEQ }, blank)).toEqual([])
  })
  it('takes a TON Center key of letters, digits, dashes and underscores, 8 to 128', () => {
    expect(tonApiKeyValid(tonKey)).toBe(true)
    expect(tonApiKeyValid(' ' + tonKey + ' ')).toBe(true)
    expect(tonApiKeyValid('a'.repeat(8))).toBe(true)
    expect(tonApiKeyValid('a'.repeat(7))).toBe(false)
    expect(tonApiKeyValid('a'.repeat(128))).toBe(true)
    expect(tonApiKeyValid('a'.repeat(129))).toBe(false)
    expect(tonApiKeyValid('abcd efgh ijkl')).toBe(false)
    expect(tonApiKeyValid('abcd.efgh/ijkl')).toBe(false)
    const c = tonConfig()
    const f = tonFormOf(c)
    expect(tonProblems({ ...f, apiKey: 'abcd.efgh/ijkl' }, c)).toEqual(['api_key'])
    // the proxy TON Center is called through, as the others'
    expect(tonProblems({ ...f, proxy: 'socks5://proxy.example.com:1080' }, c)).toEqual(['proxy'])
    expect(tonProblems({ ...f, proxy: ' http://user:pw@proxy.example.com:3128 ' }, c)).toEqual([])
  })
  it('knows when something would change, by the account and not by the form it is written in', () => {
    const c = tonConfig()
    const f = tonFormOf(c)
    expect(tonDirty(f, c)).toBe(false)
    expect(tonDirty({ ...f, address: ' ' + tonStore + ' ' }, c)).toBe(false)
    expect(tonDirty({ ...f, address: tonStoreEQ }, c)).toBe(false)
    expect(tonDirty({ ...f, address: tonStoreRaw }, c)).toBe(false)
    expect(tonDirty({ ...f, address: tonNewEQ }, c)).toBe(true)
    expect(tonDirty({ ...f, address: tonTypo }, c)).toBe(true)
    expect(tonDirty({ ...f, enabled: false }, c)).toBe(true)
    expect(tonDirty({ ...f, apiKey: ' ' }, c)).toBe(false)
    expect(tonDirty({ ...f, apiKey: tonKey }, c)).toBe(true)
    expect(tonDirty({ ...f, clearApiKey: true }, c)).toBe(true)
    expect(tonDirty({ ...f, proxy: ' ' }, c)).toBe(false)
    expect(tonDirty({ ...f, proxy: 'http://p.example.com:3128' }, c)).toBe(true)
    expect(tonDirty({ ...f, clearProxy: true }, c)).toBe(true)
    // an address the server stored in another form is not a change either
    expect(tonDirty(tonFormOf(tonConfig({ address: tonStoreEQ })), tonConfig({ address: tonStoreEQ }))).toBe(false)
  })
  it('sends the whole address in the canonical form, empty secrets to keep them, and never a secret being removed', () => {
    const f = tonFormOf(tonConfig())
    const keep = { proxy: '', clear_proxy: false }
    expect(tonBody(f)).toEqual({ enabled: true, address: tonStore, api_key: '', clear_api_key: false, ...keep })
    // a bounceable EQ…, a raw 0:… and a UQ… of one account are sent as the one string, the UQ… form
    for (const typed of [tonStoreEQ, tonStoreRaw, ' ' + tonStore + ' ', '⁦' + tonStoreEQ + '⁩']) expect(tonBody({ ...f, address: typed }).address, typed).toBe(tonStore)
    expect(tonBody({ ...f, address: tonNewEQ, apiKey: ' ' + tonKey })).toEqual({ enabled: true, address: tonNew, api_key: tonKey, clear_api_key: false, ...keep })
    expect(tonBody({ ...f, apiKey: tonKey, clearApiKey: true })).toEqual({ enabled: true, address: tonStore, api_key: '', clear_api_key: true, ...keep })
    expect(tonBody({ ...f, proxy: ' http://p.example.com:3128 ' })).toMatchObject({ proxy: 'http://p.example.com:3128', clear_proxy: false })
    expect(tonBody({ ...f, proxy: 'http://p.example.com:3128', clearProxy: true })).toMatchObject({ proxy: '', clear_proxy: true })
    // a test changes nothing: no switch
    expect(tonTestBody({ ...f, enabled: false, apiKey: tonKey })).toEqual({ address: tonStore, api_key: tonKey, clear_api_key: false, ...keep })
    expect(tonTestBody({ ...f, address: tonNewRaw, proxy: 'http://p.example.com:3128' })).toEqual({ address: tonNew, api_key: '', clear_api_key: false, proxy: 'http://p.example.com:3128', clear_proxy: false })
    expect(tonTestBody({ ...f, clearProxy: true })).toEqual({ address: tonStore, api_key: '', clear_api_key: false, proxy: '', clear_proxy: true })
    // what is not an address goes as typed (without the decoration of a paste): core says what is wrong
    expect(tonBody({ ...f, address: ' not an address ' }).address).toBe('notanaddress')
  })
  it('knows that open payments block a new address, in whatever form it is written', () => {
    const c = tonConfig() // one open payment
    const f = tonFormOf(c)
    expect(tonBlockedByOpen(f, c)).toBe(false)
    expect(tonBlockedByOpen({ ...f, enabled: false, apiKey: tonKey, address: tonStoreEQ }, c)).toBe(false)
    expect(tonBlockedByOpen({ ...f, address: ' ' + tonStoreRaw }, c)).toBe(false)
    expect(tonBlockedByOpen({ ...f, address: tonNew }, c)).toBe(true)
    expect(tonBlockedByOpen({ ...f, address: tonNewEQ }, c)).toBe(true)
    expect(tonBlockedByOpen({ ...f, address: tonNew }, tonConfig({ health: { ...c.health!, open: 0 } }))).toBe(false)
    expect(tonBlockedByOpen({ ...f, address: tonNew }, tonConfig({ address: '' }))).toBe(false)
  })
  it('tells whether there is a price customers can be quoted', () => {
    expect(tonPriceState(tonConfig())).toBe('fresh')
    expect(tonPriceState(tonConfig({ price: { usdt_per_ton: '3.2145', source: 'okx', at: t0, fresh: false } }))).toBe('stale')
    expect(tonPriceState(tonConfig({ price: null }))).toBe('none')
    expect(tonPriceState(tonConfig({ price: undefined }))).toBe('none')
  })
  it('links a transaction to Tonviewer only when it is a hash', () => {
    expect(tonviewerUrl('AB'.repeat(32))).toBe('https://tonviewer.com/transaction/' + 'ab'.repeat(32))
    expect(tonviewerUrl('ab'.repeat(31))).toBe('')
    expect(tonviewerUrl('javascript:alert(1)')).toBe('')
    expect(tonviewerUrl('ab'.repeat(32) + '/../x')).toBe('')
  })
  it('says why a TON deposit matched nothing in its own words, and keeps the USDT words for USDT', () => {
    expect([...tonUnmatchedReasons]).toEqual(['no_payment', 'already_paid', 'closed', 'ambiguous', 'underpaid', 'late_price', 'dust'])
    for (const code of tonUnmatchedReasons) expect(unmatchedReason({ reason: 'English words', reason_code: code }, 'TON')).toEqual({ key: 'gateway.ton.reason.' + code })
    for (const code of unmatchedReasons) expect(unmatchedReason({ reason: 'English words', reason_code: code })).toEqual({ key: 'gateway.usdt.reason.' + code })
    // the two codes only TON has are not a USDT reason
    expect(unmatchedReason({ reason: 'less than asked', reason_code: 'underpaid' })).toEqual({ raw: 'less than asked' })
    expect(unmatchedReason({ reason_code: 'late_price' })).toEqual({ raw: 'late_price' })
    // a reason this dashboard does not know: core's words, or the code itself
    expect(unmatchedReason({ reason: 'something new', reason_code: 'new_code' }, 'TON')).toEqual({ raw: 'something new' })
    expect(unmatchedReason({}, 'TON')).toBeNull()
  })
  it('names TON where every provider is named: payments, reports, the overview and the license', () => {
    // all of them ask the catalog for payment.provider.<id>
    expect(en.payment.provider.ton_auto).toBe('TON')
    expect(fa.payment.provider.ton_auto).toBe('تون (TON)')
    // the alerts the watchdog sends for it, in both languages, with the count the plural form follows
    expect(en.system.alerts.kind.ton_stuck).toContain('{count}')
    expect(fa.system.alerts.kind.ton_stuck).toContain('{count}')
    expect(en.system.alerts.kind.ton_failing).not.toContain('{')
    // the card's own title says it is automatic, the provider's name does not have to
    expect(en.gateway.ton.title).toBe('TON (automatic)')
    expect(fa.gateway.ton.title).toBe('تون (TON)')
  })
  it('resolves a TON transfer with its network, and a TRC20 one without', () => {
    const d = { txid: 'AB'.repeat(32), event_index: 0 }
    expect(depositNetwork(tonAuto)).toBe('TON')
    expect(depositNetwork('usdt_trc20_auto')).toBeUndefined()
    expect(depositNetwork('zarinpal')).toBeUndefined()
    expect(resolveBody(d, ' paid back ', 'TON')).toEqual({ txid: d.txid, event_index: 0, note: 'paid back', network: 'TON' })
    // as before TON: the body of a TRC20 deposit has no network at all
    expect(resolveBody(d, ' paid back ')).toEqual({ txid: d.txid, event_index: 0, note: 'paid back' })
    expect(Object.keys(resolveBody(d, '', undefined))).toEqual(['txid', 'event_index', 'note'])
    expect(depositKey(d)).toBe('ab'.repeat(32) + '#0')
  })
})

describe('payment method cards', () => {
  it('puts each method’s switch first, then its details', () => {
    const cards = methodCards(payments())
    expect(cards.map((c) => c.key)).toEqual(['manual_card', 'manual_crypto', 'manual_zarinpal', 'stars'])
    expect(cards[0].items.map((i) => i.key)).toEqual(['payments.manual_card.enabled', 'payments.card_number', 'payments.card_holder'])
    expect(cards[1].items.map((i) => i.key)).toEqual(['payments.manual_crypto.enabled', 'payments.usdt_trc20', 'payments.usdt_erc20', 'payments.usdt_rate'])
    expect(cards.map((c) => c.state)).toEqual(['on', 'incomplete', 'off', 'on'])
  })
  it('counts a method as offered only with what it needs', () => {
    const crypto = methods[1]
    expect(methodState(crypto, payments({ 'payments.usdt_erc20': '0x' + 'a'.repeat(40) }))).toBe('on')
    expect(methodState(crypto, payments({ 'payments.usdt_trc20': 'T' + 'a'.repeat(33) }))).toBe('on')
    expect(methodState(methods[0], payments({ 'payments.card_number': '' }))).toBe('incomplete')
    expect(methodState(methods[0], payments({ 'payments.manual_card.enabled': 'false' }))).toBe('off')
    // a core without the switches: on when the details are there
    expect(methodState(methods[3], payments().filter((i) => !i.key.endsWith('.enabled')))).toBe('on')
  })
  it('keeps payments settings no card claims in a last card', () => {
    const cards = methodCards([...payments(), setting('payments.something_new', 'text')])
    expect(cards.at(-1)).toMatchObject({ key: 'other', state: null })
    expect(cards.at(-1)?.items.map((i) => i.key)).toEqual(['payments.something_new'])
    expect(methodCards([])).toEqual([])
  })
})

describe('overview gateways', () => {
  const g = (provider: string, enabled: boolean, healthy: boolean, ready?: boolean) => ({ provider, enabled, healthy, ...(ready === undefined ? {} : { ready }) })
  it('tells a method missing its details from one that fails', () => {
    for (const m of methods) {
      // on but not set up: what the Gateways page calls "On, details missing"
      expect(overviewGatewayState(g(m.key, true, false)), m.key).toBe('incomplete')
      expect(overviewGatewayState(g(m.key, true, true)), m.key).toBe('ok')
      expect(overviewGatewayState(g(m.key, false, false)), m.key).toBe('off')
    }
    // Zarinpal can fail, and so can the other card gateways
    expect(overviewGatewayState(g('zarinpal', true, false))).toBe('down')
    for (const p of keyedProviders) {
      expect(overviewGatewayState(g(p, true, false)), p).toBe('down')
      expect(overviewGatewayState(g(p, true, true)), p).toBe('ok')
      expect(overviewGatewayState(g(p, false, false)), p).toBe('off')
      expect(overviewGatewayState(g(p, true, false, false)), p).toBe('incomplete')
    }
    expect(overviewGatewayState(g('zarinpal', true, true))).toBe('ok')
    expect(overviewGatewayState(g('zarinpal', false, true))).toBe('off')
    // core may say a method is not ready itself
    expect(overviewGatewayState(g('zarinpal', true, false, false))).toBe('incomplete')
    expect(overviewGatewayState(g('stars', true, true, false))).toBe('incomplete')
    expect(overviewGatewayState(g('zarinpal', true, false, true))).toBe('down')
    expect(overviewGatewayState(g('stars', false, false, false))).toBe('off')
  })
})

describe('gateway report', () => {
  const report = (totals: GatewayReport['totals']): GatewayReport => ({
    provider: 'zarinpal', from: 0, to: 0, totals, healthy: true, open: 0, oldest_open_at: null, last_paid_at: null, last_error_at: null, last_error: '',
  })
  it('covers the last days up to now', () => {
    expect(reportRange(7, t0 * 1000 + 999)).toEqual({ from: t0 - 7 * 86_400, to: t0 })
    expect(reportRange(30, t0 * 1000)).toEqual({ from: t0 - 30 * 86_400, to: t0 })
  })
  it('puts both periods side by side, paid first', () => {
    const week = report([{ status: 'succeeded', count: 5, amounts: [{ amount: 600000, currency: 'IRT' }] }, { status: 'failed', count: 1, amounts: [] }])
    const month = report([
      { status: 'failed', count: 4, amounts: [] },
      { status: 'succeeded', count: 20, amounts: [{ amount: 2_400_000, currency: 'IRT' }] },
      { status: 'refunded', count: 1, amounts: [] },
      { status: 'weird', count: 0, amounts: [] },
    ])
    const rows = reportRows(week, month)
    expect(rows.map((r) => r.status)).toEqual(['succeeded', 'confirming', 'pending', 'failed', 'expired', 'refunded'])
    expect(rows[0]).toEqual({ status: 'succeeded', week: { count: 5, total: [{ amount: 600000, currency: 'IRT' }] }, month: { count: 20, total: [{ amount: 2_400_000, currency: 'IRT' }] } })
    expect(rows[1]).toEqual({ status: 'confirming', week: { count: 0, total: [] }, month: { count: 0, total: [] } })
    expect(rows[5].week.count).toBe(0)
    expect(reportRows(null, null).map((r) => r.status)).toEqual(['succeeded', 'confirming', 'pending', 'failed', 'expired'])
  })
  it('reads the failed payment checks of both periods, newest first', () => {
    const iid = 'aaaaaaaa-1111-4aaa-8aaa-aaaaaaaaaaaa'
    const withChecks = (list: GatewayReport['check_errors'], count?: number): GatewayReport => ({ ...report([]), check_errors: list, check_error_count: count })
    const old = { at: t0 - 7200, intent_id: iid, detail: 'zarinpal: verify: context deadline exceeded' }
    const recent = { at: t0 - 60, intent_id: iid, detail: 'zarinpal: verify: HTTP 503' }
    // an older core does not report them, and none is nothing to say
    expect(checkErrors(report([]), report([]))).toBeNull()
    expect(checkErrors(null, null)).toBeNull()
    expect(checkErrors(withChecks([], 0), withChecks([], 0))).toBeNull()
    expect(checkErrors(withChecks(null), withChecks(null))).toBeNull()
    // the newest of the month, the count of each period, and how many more there are
    expect(checkErrors(withChecks([recent], 1), withChecks([old, recent], 45))).toEqual({ week: 1, month: 45, items: [recent, old], more: 43 })
    // a count is never below what is listed
    expect(checkErrors(withChecks([recent]), withChecks([recent, old]))).toEqual({ week: 1, month: 2, items: [recent, old], more: 0 })
    expect(checkErrors(withChecks([recent], 1), withChecks([recent], -3))).toEqual({ week: 1, month: 1, items: [recent], more: 0 })
    // only the week's list (the month's report did not come back)
    expect(checkErrors(withChecks([recent], 3), null)).toEqual({ week: 3, month: 3, items: [recent], more: 2 })
  })
})

// The write-only field on its own.
describe('secret field', () => {
  const unmountAll: (() => void)[] = []
  afterEach(() => {
    unmountAll.splice(0).forEach((f) => f())
    document.body.innerHTML = ''
  })
  async function mount(props: Record<string, unknown>) {
    const { default: SecretField } = await import('../components/SecretField.vue')
    const { i18n, setLang } = await import('../i18n')
    setLang('en')
    const value = ref('')
    const clear = ref(false)
    const root = document.createElement('div')
    document.body.appendChild(root)
    const app = createApp(
      defineComponent({
        setup: () => () =>
          // the props each test sets are not known to TypeScript here
          h(SecretField as Component, {
            id: 'secret',
            label: 'Token',
            ...props,
            modelValue: value.value,
            'onUpdate:modelValue': (v: string) => (value.value = v),
            clear: clear.value,
            'onUpdate:clear': (v: boolean) => (clear.value = v),
          }),
      }),
    )
    app.use(i18n).use(PrimeVue)
    app.mount(root)
    unmountAll.push(() => {
      app.unmount()
      setLang('fa')
    })
    await nextTick()
    const click = async (sel: string) => {
      ;(root.querySelector(sel) as HTMLButtonElement).click()
      await nextTick()
      await nextTick()
    }
    const type = async (v: string) => {
      const input = root.querySelector('#secret') as HTMLInputElement
      input.value = v
      input.dispatchEvent(new Event('input'))
      await nextTick()
    }
    return { root, value, clear, click, type }
  }

  it('asks for a value when none is stored', async () => {
    const f = await mount({ set: false })
    expect(f.root.querySelector('.state')?.textContent).toBe(en.secret.not_set)
    const input = f.root.querySelector('#secret') as HTMLInputElement
    expect(input.type).toBe('password')
    expect(input.autocomplete).toBe('new-password')
    expect(f.root.querySelector('label')?.getAttribute('for')).toBe('secret')
    expect(f.root.querySelector('button.replace')).toBeNull()
    expect(f.root.querySelector('button.clear')).toBeNull()
    await f.type('abc')
    expect(f.value.value).toBe('abc')
  })

  it('shows a stored value only as set, until staff replace it', async () => {
    const f = await mount({ set: true, hint: 'ab12', help: 'what it is for' })
    expect(f.root.querySelector('.state')?.textContent).toBe('Set · ends in \u2068ab12\u2069')
    expect(f.root.querySelector('#secret')).toBeNull()
    expect(f.root.textContent).toContain('what it is for')
    await f.click('button.replace')
    expect((f.root.querySelector('#secret') as HTMLInputElement).value).toBe('')
    await f.type('new-value')
    expect(f.value.value).toBe('new-value')
    // keep after all: the typed value is dropped
    await f.click('button.keep')
    expect(f.value.value).toBe('')
    expect(f.root.querySelector('#secret')).toBeNull()
    expect(f.root.innerHTML).not.toContain('new-value')
  })

  it('removes a stored value only where allowed, and can undo it', async () => {
    const plain = await mount({ set: true })
    expect(plain.root.querySelector('button.clear')).toBeNull()
    expect(plain.root.textContent).toContain(en.secret.keep_hint)
    const f = await mount({ set: true, clearable: true })
    await f.click('button.clear')
    expect(f.clear.value).toBe(true)
    expect(f.root.querySelector('.state')?.textContent).toBe(en.secret.will_clear)
    expect(f.root.querySelector('button.replace')).toBeNull()
    await f.click('button.undo')
    expect(f.clear.value).toBe(false)
    expect(f.root.querySelector('.state')?.textContent).toBe(en.secret.set)
  })

  it('asks for a new value when the stored one cannot be kept', async () => {
    const f = await mount({ set: true, force: true })
    expect(f.root.querySelector('#secret')).not.toBeNull()
    expect(f.root.querySelector('button.keep')).toBeNull()
    expect(f.root.textContent).not.toContain(en.secret.keep_hint)
  })

  it('keeps a typed value in sight', async () => {
    const f = await mount({ set: true })
    await f.click('button.replace')
    await f.type('typed')
    // the field closes only through Keep, which drops the value
    expect(f.root.querySelector('#secret')).not.toBeNull()
    await f.click('button.keep')
    expect(f.value.value).toBe('')
  })

  it('offers nothing to change when read only', async () => {
    const f = await mount({ set: true, clearable: true, disabled: true })
    expect(f.root.querySelector('.state')?.textContent).toBe(en.secret.set)
    expect(f.root.querySelectorAll('button')).toHaveLength(0)
  })

  it('says what is wrong with the typed value', async () => {
    const f = await mount({ set: false, error: 'not a token' })
    const input = f.root.querySelector('#secret') as HTMLInputElement
    expect(input.classList.contains('p-invalid')).toBe(true)
    expect(f.root.querySelector('[role=alert]')?.textContent).toBe('not a token')
    expect(input.getAttribute('aria-describedby')).toContain('secret-error')
  })
})

// The page, booted with the whole app against a fake core.
describe('gateways page', () => {
  const ownerPerms = ['overview.read', 'settings.write', 'servers.write', 'gateways.write', 'staff.write', 'audit.read']
  const adminPerms = ['overview.read', 'settings.write', 'servers.write', 'audit.read']
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path
  const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replace('{' + k + '}', v), s)
  const week = { totals: [{ status: 'succeeded', count: 5, amounts: [{ amount: 600000, currency: 'IRT' }] }, { status: 'expired', count: 1, amounts: [] }] }
  const month = { totals: [{ status: 'succeeded', count: 20, amounts: [{ amount: 2_400_000, currency: 'IRT' }] }, { status: 'expired', count: 3, amounts: [] }] }

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | Promise<Response> | undefined
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(path: string, permissions: string[], reply: () => unknown, answer: Answer = () => undefined) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = answer(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        const get = (init?.method ?? 'GET') === 'GET'
        if (url === '/api/v1/me') {
          return json(200, { user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'owner', language: 'fa' }, permissions, csrf: 'c', session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop' })
        }
        if (get && url === '/api/v1/gateways') return json(200, reply())
        if (get && url.startsWith('/api/v1/gateways/report?')) {
          const q = new URLSearchParams(url.split('?')[1])
          const days = (Number(q.get('to')) - Number(q.get('from'))) / 86_400
          return json(200, { provider: q.get('provider'), from: Number(q.get('from')), to: Number(q.get('to')), healthy: true, open: 0, oldest_open_at: null, last_paid_at: null, last_error_at: null, last_error: '', ...(days === 7 ? week : month) })
        }
        if (url === '/api/v1/settings' && init?.method === 'PUT') return json(200, { items: [], groups: [] })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }

  afterEach(() => {
    unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  function type(el: Element | null, value: string) {
    const input = el as HTMLInputElement
    input.value = value
    input.dispatchEvent(new Event('input'))
  }
  const sent = (calls: string[], method: string, path: string) =>
    calls.filter((c) => c.startsWith(`${method} /api/v1${path} `)).map((c) => JSON.parse(c.slice(`${method} /api/v1${path} `.length)) as Record<string, unknown>)
  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  const zp = () => document.querySelector('.zarinpal-card') as HTMLElement
  const field = (id: string) => document.getElementById(id)?.closest('.secret-field') ?? (document.querySelector(`#${id}-label`)?.closest('.secret-field') as HTMLElement)
  const save = () => zp().querySelector('button.save') as HTMLButtonElement
  const shown = async (root: Element, selector: string) =>
    vi.waitFor(() => {
      const el = root.querySelector<HTMLButtonElement>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })

  it('shows Zarinpal with its secrets only as set, and saves what changed', async () => {
    let current = config()
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [current], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/zarinpal' && init?.method === 'PUT') {
        current = { ...current, enabled: b.enabled as boolean }
        return json(200, { item: current })
      }
    })
    await found('.zarinpal-card')
    expect(calls).toContain('GET /api/v1/gateways')
    // the merchant id and proxy are only "set", with the merchant's last characters
    expect(field('zarinpal-merchant').querySelector('.state')?.textContent).toBe(fill(text('secret.set_hint'), { hint: '\u2068ab12\u2069' }))
    expect(field('zarinpal-proxy').querySelector('.state')?.textContent).toBe(text('secret.set'))
    expect(document.querySelector('#zarinpal-merchant')).toBeNull()
    expect(document.querySelector('#zarinpal-proxy')).toBeNull()
    expect(zp().querySelector('.state')?.textContent).toBe(text('gateway.state.off'))
    expect(zp().querySelector('.open-count')?.textContent).toContain(fill(text('gateway.open_count'), { n: '۲' }))
    expect(zp().querySelector('.health-state')?.textContent).toBe(text('gateway.health.ok'))
    // the report: the last 7 and 30 days, by status
    await vi.waitFor(() => expect(zp().querySelector('.week-succeeded')?.textContent).toBe('۵'))
    expect(zp().querySelector('.month-succeeded')?.textContent).toBe('۲۰')
    expect(zp().querySelector('.report-table')?.textContent).toContain('۲٬۴۰۰٬۰۰۰ تومان')
    expect(zp().querySelector('.month-expired')?.textContent).toBe('۳')
    const reports = calls.filter((c) => c.startsWith('GET /api/v1/gateways/report?'))
    expect(reports).toHaveLength(2)
    expect(reports.every((c) => c.includes('provider=zarinpal'))).toBe(true)
    // this core reports no failed checks: nothing about them
    expect(zp().querySelector('.check-errors')).toBeNull()

    expect(save().disabled).toBe(true)
    ;(document.getElementById('zarinpal-enabled') as HTMLInputElement).click()
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    save().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/zarinpal')).toEqual([{ enabled: true, sandbox: false, merchant_id: '', proxy: '', clear_proxy: false }]))
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/gateways')).toHaveLength(2))
    await vi.waitFor(() => expect(zp().querySelector('.state')?.textContent).toBe(text('gateway.state.on')))
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('gateway.saved'))
    await vi.waitFor(() => expect(save().disabled).toBe(true))
  })

  it('shows the payment checks that failed, newest first', async () => {
    const iid = (n: number) => `aaaaaaaa-1111-4aaa-8aaa-${String(n).repeat(12).slice(0, 12)}`
    const checks = [
      { at: t0 - 7200, intent_id: iid(1), detail: 'zarinpal: verify: context deadline exceeded' },
      { at: t0 - 60, intent_id: iid(2), detail: 'zarinpal: verify: HTTP 503 [address]' },
      { at: t0 - 30, intent_id: '', detail: '' },
    ]
    let count = { week: 2, month: 23 }
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [config()], settings: payments() }), (url) => {
      if (!url.startsWith('/api/v1/gateways/report?')) return undefined
      const q = new URLSearchParams(url.split('?')[1])
      const days = (Number(q.get('to')) - Number(q.get('from'))) / 86_400
      return json(200, {
        provider: 'zarinpal', from: Number(q.get('from')), to: Number(q.get('to')), healthy: false, open: 0, oldest_open_at: null, last_paid_at: null,
        last_error_at: t0 - 30, last_error: 'zarinpal: verify: HTTP 503', ...(days === 7 ? week : month),
        check_errors: days === 7 ? checks.slice(1) : checks, check_error_count: days === 7 ? count.week : count.month,
      })
    })
    await found('.zarinpal-card')
    const box = await shown(zp(), '.check-errors')
    expect(box.querySelector('.checks-title')?.textContent?.trim()).toBe(text('gateway.check_errors.title'))
    // how many in each period, in the user's digits
    expect(box.querySelector('.checks-count')?.textContent).toBe(fill(text('gateway.check_errors.counts'), { week: '۲', month: '۲۳' }))
    expect(box.querySelector('.checks-hint')?.textContent).toBe(text('gateway.check_errors.hint'))
    // the month's newest first, each with its time, payment and the error (left to right)
    const items = [...box.querySelectorAll('.check-error')] as HTMLElement[]
    expect(items).toHaveLength(3)
    expect(items.map((i) => i.querySelector('.check-at')?.textContent)).toEqual([dateTime(t0 - 30, 'fa'), dateTime(t0 - 60, 'fa'), dateTime(t0 - 7200, 'fa')])
    expect(items[0].querySelector('.check-payment')).toBeNull()
    expect(items[0].querySelector('.check-detail')).toBeNull()
    expect(items[1].querySelector('.check-payment')?.textContent).toContain('222222')
    expect(items[1].querySelector('.check-payment')?.textContent).toContain(text('gateway.check_errors.payment'))
    expect(items[1].querySelector('.check-detail')?.textContent).toBe('zarinpal: verify: HTTP 503 [address]')
    expect(items[1].querySelector('.check-detail')?.classList.contains('app-ltr')).toBe(true)
    expect(items[2].querySelector('.check-detail')?.textContent).toBe('zarinpal: verify: context deadline exceeded')
    // more than are listed
    expect(box.querySelector('.checks-more')?.textContent?.trim()).toBe(fill(text('gateway.check_errors.more'), { shown: '۳', total: '۲۳' }))

    // none left in either period: the report says nothing about them
    count = { week: 0, month: 0 }
    checks.length = 0
    ;(zp().querySelector('.report button.refresh') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(zp().querySelector('.check-errors')).toBeNull())
    expect(zp().querySelector('.week-succeeded')?.textContent).toBe('۵')
  })

  it('shows IDPay and NextPay with a write-only API key; NextPay has no sandbox', async () => {
    let idpay = config({ provider: 'idpay', merchant_hint: '', configured: false })
    const nextpay = config({ provider: 'nextpay', merchant_hint: '', configured: true, sandbox_allowed: true })
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config(), idpay, nextpay], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/idpay' && init?.method === 'PUT') {
        idpay = { ...idpay, enabled: b.enabled as boolean, configured: true }
        return json(200, { item: idpay })
      }
    })
    await found('.keyed-idpay')
    const card = (p: string) => document.querySelector('.keyed-' + p) as HTMLElement
    expect(card('idpay').querySelector('.card-title')?.textContent).toBe(text('payment.provider.idpay'))
    expect(card('nextpay').querySelector('.card-title')?.textContent).toBe(text('payment.provider.nextpay'))
    // only IDPay has a sandbox
    expect(document.getElementById('idpay-sandbox')).not.toBeNull()
    expect(document.getElementById('nextpay-sandbox')).toBeNull()
    // the stored key is only "set"; a new one is typed into a password field
    expect(field('nextpay-api-key').querySelector('.state')?.textContent).toBe(text('secret.set'))
    expect(document.querySelector('#nextpay-api-key')).toBeNull()
    const input = await found<HTMLInputElement>('#idpay-api-key')
    expect(input.type).toBe('password')
    type(input, 'abc')
    await vi.waitFor(() => expect(field('idpay-api-key').textContent).toContain(text('gateway.keyed.invalid.api_key')))
    const saveIdpay = () => card('idpay').querySelector('button.save') as HTMLButtonElement
    expect(saveIdpay().disabled).toBe(true)
    type(input, ' ' + merchant.toUpperCase() + ' ')
    ;(document.getElementById('idpay-enabled') as HTMLInputElement).click()
    await vi.waitFor(() => expect(saveIdpay().disabled).toBe(false))
    saveIdpay().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/idpay')).toEqual([{ enabled: true, sandbox: false, api_key: merchant, proxy: '', clear_proxy: false }]))
    // the typed key is gone from the page at once
    await vi.waitFor(() => expect(document.querySelector('#idpay-api-key')).toBeNull())
    expect(document.body.innerHTML.toLowerCase()).not.toContain(merchant)
  })

  // ---- Aqayepardakht: a PIN that keeps its case, and the PIN "sandbox" as its test system
  const aqName = () => text('payment.provider.aqayepardakht')
  const aqConfig = (over: Partial<GatewayConfig> = {}) => config({ provider: 'aqayepardakht', merchant_hint: '', configured: true, ...over })
  const aqCard = () => document.querySelector('.keyed-aqayepardakht') as HTMLElement
  const aqSave = () => aqCard().querySelector('button.save') as HTMLButtonElement
  const aqTest = () => aqCard().querySelector('button.test') as HTMLButtonElement
  const aqProviders = (aq: GatewayConfig) => () => ({ providers: [config(), aq], settings: payments() })

  it('shows Aqayepardakht with a write-only PIN that keeps its case, and no sandbox switch', async () => {
    let aq = aqConfig({ configured: false, sandbox_allowed: true })
    const { calls } = await boot('/admin/gateways', ownerPerms, () => aqProviders(aq)(), (url, init, b) => {
      if (url === '/api/v1/gateways/aqayepardakht' && init?.method === 'PUT') {
        aq = { ...aq, enabled: b.enabled as boolean, configured: true }
        return json(200, { item: aq })
      }
    })
    await found('.keyed-aqayepardakht')
    expect(aqCard().querySelector('.card-title')?.textContent).toBe(aqName())
    expect(aqCard().textContent).toContain(text('gateway.aqayepardakht_desc'))
    // no sandbox switch: its test system is the PIN "sandbox", which the help under the PIN names
    expect(document.getElementById('aqayepardakht-sandbox')).toBeNull()
    expect(aqCard().querySelector('.sandbox')).toBeNull()
    const input = await found<HTMLInputElement>('#aqayepardakht-api-key')
    expect(input.type).toBe('password')
    expect(field('aqayepardakht-api-key').querySelector('label')?.textContent).toBe(text('gateway.keyed.pin.label'))
    expect(field('aqayepardakht-api-key').textContent).toContain(fill(text('gateway.keyed.pin.sandbox_help'), { name: aqName() }))
    expect(field('aqayepardakht-api-key').textContent).not.toContain(text('gateway.keyed.pin.sandbox_prod'))
    // the example is no id, and the field takes more than a PIN may have
    expect(input.placeholder).not.toMatch(/[0-9a-fx]{8}-[0-9a-fx]{4}/i)
    expect(input.maxLength).toBe(pinFieldMax)
    // the PIN rule, not the API key's
    type(input, 'ab')
    await vi.waitFor(() => expect(field('aqayepardakht-api-key').textContent).toContain(text('gateway.keyed.pin.invalid.pin')))
    expect(field('aqayepardakht-api-key').textContent).not.toContain(text('gateway.keyed.invalid.api_key'))
    expect(aqSave().disabled).toBe(true)
    // switched on without a PIN
    ;(document.getElementById('aqayepardakht-enabled') as HTMLInputElement).click()
    type(input, '')
    await vi.waitFor(() => expect(aqCard().querySelector('.field-error')?.textContent).toContain(fill(text('gateway.keyed.pin.invalid.needs_pin'), { name: aqName() })))
    // a PIN with capitals and symbols is saved as typed (trimmed), without a sandbox
    type(input, ' ' + pin + ' ')
    await vi.waitFor(() => expect(aqSave().disabled).toBe(false))
    expect(field('aqayepardakht-api-key').querySelector('[role=alert]')).toBeNull()
    aqSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/aqayepardakht')).toEqual([{ enabled: true, sandbox: false, api_key: pin, proxy: '', clear_proxy: false }]))
    // the typed PIN is gone from the page at once
    await vi.waitFor(() => expect(document.querySelector('#aqayepardakht-api-key')).toBeNull())
    expect(document.body.innerHTML).not.toContain(pin)
    expect(field('aqayepardakht-api-key').querySelector('.state')?.textContent).toBe(text('secret.set'))
  })

  it('refuses the PIN sandbox in production: said under the PIN, with nothing to save or test', async () => {
    const { calls } = await boot('/admin/gateways', ownerPerms, aqProviders(aqConfig({ configured: false, sandbox_allowed: false })))
    await found('.keyed-aqayepardakht')
    // production does not offer the test PIN: the help says it is not accepted
    expect(field('aqayepardakht-api-key').textContent).toContain(text('gateway.keyed.pin.sandbox_prod'))
    expect(field('aqayepardakht-api-key').textContent).not.toContain(fill(text('gateway.keyed.pin.sandbox_help'), { name: aqName() }))
    const input = await found<HTMLInputElement>('#aqayepardakht-api-key')
    ;(document.getElementById('aqayepardakht-enabled') as HTMLInputElement).click()
    type(input, 'sandbox')
    await vi.waitFor(() => expect(field('aqayepardakht-api-key').textContent).toContain(text('gateway.keyed.pin.invalid.sandbox')))
    expect(field('aqayepardakht-api-key').querySelector('[role=alert]')?.textContent).toBe(text('gateway.keyed.pin.invalid.sandbox'))
    expect(input.classList.contains('p-invalid')).toBe(true)
    expect(aqSave().disabled).toBe(true)
    expect(aqTest().disabled).toBe(true)
    // another PIN takes the message away
    type(input, 'Sandbox-9')
    await vi.waitFor(() => expect(aqSave().disabled).toBe(false))
    expect(field('aqayepardakht-api-key').querySelector('[role=alert]')).toBeNull()
    expect(input.classList.contains('p-invalid')).toBe(false)
    expect(aqTest().disabled).toBe(false)
    expect(sent(calls, 'PUT', '/gateways/aqayepardakht')).toEqual([])
    expect(sent(calls, 'POST', '/gateways/aqayepardakht/test')).toEqual([])
  })

  it('shows what core says about the sandbox under the PIN, as there is no sandbox field', async () => {
    const said = 'the PIN sandbox is refused in production'
    const { calls } = await boot('/admin/gateways', ownerPerms, aqProviders(aqConfig({ configured: false })), (url, init) =>
      url === '/api/v1/gateways/aqayepardakht' && init?.method === 'PUT' ? json(400, { error: 'invalid', message: said, field: 'sandbox' }) : undefined,
    )
    await found('.keyed-aqayepardakht')
    const input = await found<HTMLInputElement>('#aqayepardakht-api-key')
    ;(document.getElementById('aqayepardakht-enabled') as HTMLInputElement).click()
    // this core did not say that production refuses it: the page lets it go to core
    type(input, 'sandbox')
    await vi.waitFor(() => expect(aqSave().disabled).toBe(false))
    aqSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/aqayepardakht')).toHaveLength(1))
    await vi.waitFor(() => expect(field('aqayepardakht-api-key').querySelector('[role=alert]')?.textContent).toBe(said))
    expect(input.classList.contains('p-invalid')).toBe(true)
    // under the PIN, and still no sandbox field
    expect(aqCard().querySelector('.sandbox')).toBeNull()
    expect(aqCard().querySelectorAll('.field-error')).toHaveLength(0)
    // typing again takes the answer away
    type(input, 'Another-1')
    await vi.waitFor(() => expect(field('aqayepardakht-api-key').querySelector('[role=alert]')).toBeNull())
  })

  it('says a PIN cannot change while payments are open, before saving and when payments refuses', async () => {
    const { calls } = await boot('/admin/gateways', ownerPerms, aqProviders(aqConfig()), (url, init) =>
      url === '/api/v1/gateways/aqayepardakht' && init?.method === 'PUT' ? json(409, { error: 'state', message: '2 aqayepardakht payments are still open', open: 2 }) : undefined,
    )
    await found('.keyed-aqayepardakht')
    ;(field('aqayepardakht-api-key').querySelector('button.replace') as HTMLButtonElement).click()
    const input = await found<HTMLInputElement>('#aqayepardakht-api-key')
    expect(input.type).toBe('password')
    // the stored PIN is only "set"; payments under it are open
    type(input, pin)
    const opened = fill(text('gateway.keyed.pin.open_payments'), { n: '۲', name: aqName() })
    await vi.waitFor(() => expect(aqCard().querySelector('.key-blocked')?.textContent).toContain(opened))
    await vi.waitFor(() => expect(aqSave().disabled).toBe(false))
    aqSave().click()
    await vi.waitFor(() => expect(aqCard().querySelector('.error')?.textContent).toContain(opened))
    expect(aqCard().querySelector('.error')?.textContent).toContain('2 aqayepardakht payments are still open')
    // the API key's words are not used for a PIN
    expect(opened).not.toContain('API')
    expect(sent(calls, 'PUT', '/gateways/aqayepardakht')[0]).toEqual({ enabled: false, sandbox: false, api_key: pin, proxy: '', clear_proxy: false })
  })

  it('tests the PIN as typed, without saving', async () => {
    let ok = true
    const { calls } = await boot('/admin/gateways', ownerPerms, aqProviders(aqConfig()), (url) =>
      url === '/api/v1/gateways/aqayepardakht/test' ? json(200, ok ? { ok: true, detail: '', unverified: 1 } : { ok: false, detail: 'aqayepardakht: pin not found' }) : undefined,
    )
    await found('.keyed-aqayepardakht')
    const result = () => aqCard().querySelector('.test-result')?.textContent ?? ''
    // the stored PIN, nothing typed
    aqTest().click()
    await vi.waitFor(() => expect(result()).toContain(fill(text('gateway.keyed.pin.test_ok'), { name: aqName() })))
    expect(result()).toContain(fill(text('gateway.test_unverified'), { n: '۱' }))
    expect(result()).not.toContain('API')
    // a typed one
    ;(field('aqayepardakht-api-key').querySelector('button.replace') as HTMLButtonElement).click()
    const input = await found<HTMLInputElement>('#aqayepardakht-api-key')
    type(input, ' ' + pin + ' ')
    ok = false
    await vi.waitFor(() => expect(result()).toBe(''))
    aqTest().click()
    await vi.waitFor(() => expect(result()).toContain(fill(text('gateway.keyed.test_failed'), { name: aqName() })))
    expect(result()).toContain('aqayepardakht: pin not found')
    expect(sent(calls, 'POST', '/gateways/aqayepardakht/test')).toEqual([
      { sandbox: false, api_key: '', proxy: '', clear_proxy: false },
      { sandbox: false, api_key: pin, proxy: '', clear_proxy: false },
    ])
    // a test saves nothing
    expect(sent(calls, 'PUT', '/gateways/aqayepardakht')).toEqual([])
  })

  it('tags Aqayepardakht as sandbox while its PIN is the test PIN, and does not take that for an unsaved change', async () => {
    let zarinpal = config()
    let aq = aqConfig({ sandbox_allowed: true })
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [zarinpal, aq], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/zarinpal' && init?.method === 'PUT') {
        zarinpal = { ...zarinpal, enabled: b.enabled as boolean }
        // the PIN was replaced with the test PIN meanwhile (by another owner)
        aq = { ...aq, sandbox: true }
        return json(200, { item: zarinpal })
      }
    })
    await found('.keyed-aqayepardakht')
    const tags = () => [...aqCard().querySelectorAll('.p-tag')].map((x) => x.textContent)
    expect(tags()).not.toContain(text('gateway.sandbox_on'))
    // another card saves, and the page reads the gateways again
    ;(document.getElementById('zarinpal-enabled') as HTMLInputElement).click()
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    save().click()
    await vi.waitFor(() => expect(tags()).toContain(text('gateway.sandbox_on')))
    // the sandbox is core's word for the PIN: no switch moved, so nothing is unsaved
    expect(aqSave().disabled).toBe(true)
    expect(aqCard().querySelector('.unsaved')).toBeNull()
    expect(aqCard().querySelector('.sandbox')).toBeNull()
  })

  it('says why Aqayepardakht is on but not offered, in the words of the PIN', async () => {
    const stored = { enabled: true, offered: false, sandbox: true, sandbox_allowed: false }
    await boot('/admin/gateways', ownerPerms, aqProviders(aqConfig({ ...stored, problem: 'sandbox_refused' })))
    await found('.keyed-aqayepardakht')
    expect(aqCard().querySelector('.state')?.textContent).toBe(text('gateway.state.not_offered'))
    expect(aqCard().querySelector('.problem')?.textContent).toContain(fill(text('gateway.keyed.pin.problem.sandbox_refused'), { name: aqName() }))
    // IDPay's words tell staff to turn a sandbox switch off, which this card does not have
    expect(aqCard().querySelector('.problem')?.textContent).not.toContain(text('gateway.keyed.problem.sandbox_refused'))
    expect(aqCard().textContent).toContain(text('gateway.sandbox_on'))
  })

  it('lists IDPay, NextPay and Aqayepardakht in that order, whatever order core answers in', async () => {
    const keyed = (provider: string) => config({ provider, merchant_hint: '', configured: false })
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [keyed('aqayepardakht'), keyed('nextpay'), config(), keyed('idpay')], settings: payments() }))
    await found('.keyed-aqayepardakht')
    expect([...document.querySelectorAll('.keyed-card')].map((c) => c.querySelector('.card-title')?.textContent)).toEqual([
      text('payment.provider.idpay'), text('payment.provider.nextpay'), text('payment.provider.aqayepardakht'),
    ])
    // Zarinpal stays first
    expect(document.querySelector('.cards > :first-child')?.classList.contains('zarinpal-card')).toBe(true)
  })

  it('replaces the merchant id, refused while payments are open', async () => {
    let refuse = true
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config()], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/zarinpal' && init?.method === 'PUT') {
        if (refuse) {
          refuse = false
          return json(409, { error: 'state', message: '2 zarinpal payments are still open', open: 2 })
        }
        return json(200, { item: config() })
      }
    })
    await found('.zarinpal-card')
    ;(field('zarinpal-merchant').querySelector('button.replace') as HTMLButtonElement).click()
    const input = await found<HTMLInputElement>('#zarinpal-merchant')
    expect(input.type).toBe('password')
    type(input, 'abc')
    await vi.waitFor(() => expect(field('zarinpal-merchant').textContent).toContain(text('gateway.invalid.merchant')))
    expect(save().disabled).toBe(true)
    type(input, ' ' + merchant.toUpperCase() + ' ')
    // payments under the old id are open: said before saving
    await vi.waitFor(() => expect(zp().querySelector('.merchant-blocked')?.textContent).toContain(fill(text('gateway.open_payments'), { n: '۲' })))
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    save().click()
    await vi.waitFor(() => expect(zp().querySelector('.error')?.textContent).toContain(fill(text('gateway.open_payments'), { n: '۲' })))
    expect(zp().querySelector('.error')?.textContent).toContain('2 zarinpal payments are still open')
    save().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/zarinpal')).toHaveLength(2))
    expect(sent(calls, 'PUT', '/gateways/zarinpal')[1]).toEqual({ enabled: false, sandbox: false, merchant_id: merchant, proxy: '', clear_proxy: false })
    // the typed id is gone from the page at once
    await vi.waitFor(() => expect(document.querySelector('#zarinpal-merchant')).toBeNull())
    expect(document.body.innerHTML.toLowerCase()).not.toContain(merchant)
  })

  it('removes the proxy, and tests the typed values without saving', async () => {
    let ok = true
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config()], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/zarinpal/test') return json(200, ok ? { ok: true, detail: '', unverified: 1 } : { ok: false, detail: 'zarinpal: -11 merchant not found' })
      if (url === '/api/v1/gateways/zarinpal' && init?.method === 'PUT') return json(200, { item: config({ proxy_set: false }) })
    })
    await found('.zarinpal-card')
    const proxy = field('zarinpal-proxy')
    ;(proxy.querySelector('button.clear') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(proxy.querySelector('.state')?.textContent).toBe(text('secret.will_clear')))
    ;(zp().querySelector('button.test') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(zp().querySelector('.test-result')?.textContent).toContain(text('gateway.test_ok')))
    expect(zp().querySelector('.test-result')?.textContent).toContain(fill(text('gateway.test_unverified'), { n: '۱' }))
    expect(sent(calls, 'POST', '/gateways/zarinpal/test')).toEqual([{ sandbox: false, merchant_id: '', proxy: '', clear_proxy: true }])
    // a test saves nothing
    expect(sent(calls, 'PUT', '/gateways/zarinpal')).toEqual([])

    // A typed proxy is checked first, then tested as typed.
    ;(proxy.querySelector('button.undo') as HTMLButtonElement).click()
    ;(await shown(proxy, 'button.replace')).click()
    const input = await found<HTMLInputElement>('#zarinpal-proxy')
    type(input, 'socks5://proxy.example.com:1080')
    await vi.waitFor(() => expect(proxy.textContent).toContain(text('gateway.invalid.proxy')))
    expect((zp().querySelector('button.test') as HTMLButtonElement).disabled).toBe(true)
    type(input, 'http://user:pw@proxy.example.com:3128')
    ok = false
    await vi.waitFor(() => expect((zp().querySelector('button.test') as HTMLButtonElement).disabled).toBe(false))
    ;(zp().querySelector('button.test') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(zp().querySelector('.test-result')?.textContent).toContain(text('gateway.test_failed')))
    expect(zp().querySelector('.test-result')?.textContent).toContain('zarinpal: -11 merchant not found')
    expect(sent(calls, 'POST', '/gateways/zarinpal/test')[1]).toEqual({ sandbox: false, merchant_id: '', proxy: 'http://user:pw@proxy.example.com:3128', clear_proxy: false })

    // Remove it after all, and save.
    ;(proxy.querySelector('button.keep') as HTMLButtonElement).click()
    ;(await shown(proxy, 'button.clear')).click()
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    save().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/zarinpal')).toEqual([{ enabled: false, sandbox: false, merchant_id: '', proxy: '', clear_proxy: true }]))
    expect(document.body.innerHTML).not.toContain('user:pw')
  })

  it('asks to wait when tests come too fast', async () => {
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config()], settings: payments() }), (url) =>
      url === '/api/v1/gateways/zarinpal/test' ? json(429, { error: 'rate_limited', message: 'at most 10 connection tests a minute' }) : undefined,
    )
    await found('.zarinpal-card')
    ;(zp().querySelector('button.test') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(zp().querySelector('.test-result')?.textContent).toContain(text('error.rate_limited')))
    expect(zp().querySelector('.test-result')?.textContent).not.toContain(text('error.generic'))
    expect(sent(calls, 'POST', '/gateways/zarinpal/test')).toHaveLength(1)
  })

  it('says why Zarinpal is on but not offered', async () => {
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [config({ enabled: true, offered: false, problem: 'public_url' })], settings: payments() }))
    await found('.zarinpal-card')
    expect(zp().querySelector('.state')?.textContent).toBe(text('gateway.state.not_offered'))
    expect(zp().querySelector('.problem')?.textContent).toContain(text('gateway.problem.public_url'))
    // a sandbox change would be refused while payments are open: said before saving
    ;(document.getElementById('zarinpal-sandbox') as HTMLInputElement).click()
    await vi.waitFor(() => expect(zp().querySelector('.merchant-blocked')?.textContent).toContain(fill(text('gateway.open_payments'), { n: '۲' })))
  })

  it('is read only while the payments key is missing', async () => {
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config({ key_missing: true, enabled: true })], settings: payments() }), (url) => {
      if (url === '/api/v1/gateways/zarinpal/test') return json(200, { ok: true, detail: '' })
    })
    await found('.zarinpal-card')
    expect(document.querySelector('.key-missing-banner')?.textContent).toContain(text('gateway.key_missing_banner'))
    expect(zp().querySelector('.key-missing')?.textContent).toContain(text('gateway.key_missing'))
    expect((document.getElementById('zarinpal-enabled') as HTMLInputElement).disabled).toBe(true)
    expect(zp().querySelector('button.replace')).toBeNull()
    expect(zp().querySelector('button.clear')).toBeNull()
    expect(save()).toBeNull()
    // the stored settings can still be tested
    ;(zp().querySelector('button.test') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/zarinpal/test')).toEqual([{}]))
    // the manual methods are bot settings: still editable
    expect((document.getElementById('setting-payments-card-number') as HTMLInputElement).readOnly).toBe(false)
  })

  it('shows a card per manual method and Stars, saved as settings', async () => {
    let values: Record<string, string> = {}
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config()], settings: payments(values) }), (url, init, b) => {
      if (url === '/api/v1/settings' && init?.method === 'PUT') {
        values = { ...values, ...(b.values as Record<string, string>) }
        return json(200, { items: [], groups: [] })
      }
    })
    await vi.waitFor(() => expect(document.querySelectorAll('.method-card')).toHaveLength(4))
    const card = (key: string) => document.querySelector('.method-' + key) as HTMLElement
    expect(card('manual_card').textContent).toContain(text('gateway.method.manual_card.title'))
    expect(card('manual_card').querySelector('.method-state')?.textContent).toBe(text('gateway.method_state.on'))
    expect(card('manual_crypto').querySelector('.method-state')?.textContent).toBe(text('gateway.method_state.incomplete'))
    expect(card('manual_zarinpal').querySelector('.method-state')?.textContent).toBe(text('gateway.method_state.off'))
    expect(card('stars').querySelector('.method-state')?.textContent).toBe(text('gateway.method_state.on'))
    expect(card('stars').textContent).toContain(text('settings.field.payments.stars.enabled.label'))

    type(document.getElementById('setting-payments-card-number'), '۶۰۳۷ ۹۹۱۱ ۲۳۴۵ ۰۰۰۰')
    const cardSave = card('manual_card').querySelector('button[type=submit]') as HTMLButtonElement
    await vi.waitFor(() => expect(cardSave.disabled).toBe(false))
    cardSave.click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/settings')).toEqual([{ values: { 'payments.card_number': '6037 9911 2345 0000' } }]))
    // the page reads the gateways again
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/gateways')).toHaveLength(2))
    await vi.waitFor(() => expect((document.getElementById('setting-payments-card-number') as HTMLInputElement).value).toBe('6037 9911 2345 0000'))

    ;(document.getElementById('setting-payments-stars-enabled') as HTMLInputElement).click()
    const starsSave = card('stars').querySelector('button[type=submit]') as HTMLButtonElement
    await vi.waitFor(() => expect(starsSave.disabled).toBe(false))
    starsSave.click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/settings')[1]).toEqual({ values: { 'payments.stars.enabled': 'false' } }))
    await vi.waitFor(() => expect(card('stars').querySelector('.method-state')?.textContent).toBe(text('gateway.method_state.off')))
  })

  // ---- Automatic USDT (TRC20)
  const usdt = () => document.querySelector('.usdt-card') as HTMLElement
  const usdtSave = () => usdt().querySelector('button.save') as HTMLButtonElement
  const usdtTest = () => usdt().querySelector('button.test') as HTMLButtonElement
  const txid = (n: number) => String(n).repeat(64).slice(0, 64)
  type Deposit = { txid: string; event_index: number; from: string; amount: { amount: number; currency: string }; at: number; reason?: string; reason_code?: string }
  // The report of the automatic provider names the deposits still to handle
  // (the newest, whatever the period), and how many there are in all.
  const deposits: Deposit[] = [
    { txid: txid(1), event_index: 1, from: sender, amount: { amount: 12_340_000, currency: 'USDT' }, at: t0 - 7200, reason: 'no open payment has exactly this amount', reason_code: 'no_payment' },
    { txid: txid(2).toUpperCase(), event_index: 0, from: sender, amount: { amount: 5_000_123, currency: 'USDT' }, at: t0 - 60 },
  ]
  const usdtReportOf = (list: () => Deposit[], count?: () => number) => (url: string) => {
    if (!url.startsWith('/api/v1/gateways/report?') || !url.includes('provider=usdt_trc20_auto')) return undefined
    const q = new URLSearchParams(url.split('?')[1])
    const days = (Number(q.get('to')) - Number(q.get('from'))) / 86_400
    return json(200, {
      provider: 'usdt_trc20_auto', from: Number(q.get('from')), to: Number(q.get('to')), healthy: true, open: 1, oldest_open_at: t0 - 600, last_paid_at: t0 - 300, last_error_at: null, last_error: '',
      totals: [{ status: 'succeeded', count: days === 7 ? 2 : 9, amounts: [{ amount: days === 7 ? 10_000_246 : 45_001_107, currency: 'USDT' }] }],
      unmatched: list(), unmatched_count: count ? count() : list().length,
    })
  }
  // 57 to handle; the list holds the newest
  const usdtReport = usdtReportOf(() => deposits, () => 57)
  const keep = { proxy: '', clear_proxy: false }

  it('shows automatic USDT with its address, the key only as set, and its unmatched deposits', async () => {
    let current = usdtConfig()
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config(), current], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/usdt_trc20_auto' && init?.method === 'PUT') {
        current = { ...current, enabled: b.enabled as boolean, key_set: true }
        return json(200, { provider: current })
      }
      return usdtReport(url)
    })
    await found('.usdt-card')
    expect(usdt().querySelector('.card-title')?.textContent).toBe(text('payment.provider.usdt_trc20_auto'))
    expect(usdt().querySelector('.state')?.textContent).toBe(text('gateway.state.on'))
    // the address is public: it is shown, left to right; the key is only "set"
    const address = document.getElementById('usdt-address') as HTMLInputElement
    expect(address.value).toBe(store)
    expect(address.dir).toBe('ltr')
    expect(field('usdt-api-key').querySelector('.state')?.textContent).toBe(text('secret.set'))
    expect(document.querySelector('#usdt-api-key')).toBeNull()
    expect(usdt().querySelector('.open-count')?.textContent).toContain(fill(text('gateway.open_count'), { n: '۱' }))
    expect(usdt().querySelector('.health-state')?.textContent).toBe(text('gateway.health.ok'))

    // the report asks for this provider, and lists the unmatched deposits still to handle, the newest first
    await vi.waitFor(() => expect(usdt().querySelector('.month-succeeded')?.textContent).toBe('۹'))
    const reports = calls.filter((c) => c.startsWith('GET /api/v1/gateways/report?') && c.includes('provider=usdt_trc20_auto'))
    expect(reports).toHaveLength(2)
    const table = await shown(usdt(), '.unmatched-table')
    // whatever their age: not "the last 30 days"; and all of them are counted, not only those listed
    expect(usdt().querySelector('#usdt-unmatched-title')?.textContent).toBe(text('gateway.usdt.unmatched'))
    expect(text('gateway.usdt.unmatched')).not.toContain('۳۰')
    expect(en.gateway.usdt.unmatched).not.toContain('30')
    expect(usdt().querySelector('.unmatched-count')?.textContent).toBe('۵۷')
    expect(usdt().querySelector('.unmatched-more')?.textContent?.trim()).toBe(fill(text('gateway.usdt.unmatched_more'), { shown: '۲', n: '۵۷' }))
    expect(usdt().querySelector('.unmatched')?.textContent).toContain(text('gateway.usdt.unmatched_help'))
    const rows = [...table.querySelectorAll('.p-datatable-tbody tr')]
    expect(rows).toHaveLength(2)
    // exact amounts, in Latin digits, as a wallet shows them
    expect(rows[0].querySelector('.amount')?.textContent).toBe('5.000123 USDT')
    expect(rows[1].querySelector('.amount')?.textContent).toBe('12.34 USDT')
    // why, in the staff member's language (core's English words are not shown)
    expect(rows[1].querySelector('.reason')?.textContent).toBe(text('gateway.usdt.reason.no_payment'))
    expect(rows[1].querySelector('.reason')?.classList.contains('app-ltr')).toBe(false)
    expect(rows[1].textContent).not.toContain('no open payment has exactly this amount')
    expect(rows[0].querySelector('.reason')).toBeNull()
    expect(rows[0].querySelector('button.resolve')?.textContent).toContain(text('gateway.usdt.resolve'))
    expect(rows[0].querySelector('.from')?.textContent).toBe(sender)
    expect(rows[0].querySelector('.txid')?.textContent).toBe(txid(2).toUpperCase())
    const link = rows[0].querySelector('a.tronscan') as HTMLAnchorElement
    expect(link.href).toBe('https://tronscan.org/#/transaction/' + txid(2))
    expect(link.rel).toContain('noopener')
    expect(link.target).toBe('_blank')
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    ;(rows[1].querySelector('button.copy') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(txid(1)))
    // Zarinpal's report has no such list
    expect(zp().querySelector('.unmatched')).toBeNull()

    // A new key: checked first, sent once, then gone from the page.
    ;(field('usdt-api-key').querySelector('button.replace') as HTMLButtonElement).click()
    const key = await found<HTMLInputElement>('#usdt-api-key')
    expect(key.type).toBe('password')
    type(key, 'not a key')
    await vi.waitFor(() => expect(field('usdt-api-key').textContent).toContain(text('gateway.usdt.invalid.api_key')))
    expect(usdtSave().disabled).toBe(true)
    type(key, apiKey)
    await vi.waitFor(() => expect(usdtSave().disabled).toBe(false))
    usdtSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')).toEqual([{ enabled: true, address: store, api_key: apiKey, clear_api_key: false, ...keep }]))
    await vi.waitFor(() => expect(document.querySelector('#usdt-api-key')).toBeNull())
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('gateway.usdt.saved'))
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/gateways')).toHaveLength(2))
    expect(document.body.innerHTML).not.toContain(apiKey)
    // Zarinpal was not touched
    expect(sent(calls, 'PUT', '/gateways/zarinpal')).toEqual([])
  })

  it('refuses a mistyped address, and a new one while automatic payments are open', async () => {
    let refuse = true
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig()], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/usdt_trc20_auto' && init?.method === 'PUT') {
        if (refuse) {
          refuse = false
          return json(409, { error: 'state', message: '1 open usdt_trc20_auto payments: the address cannot change', open: 1 })
        }
        return json(200, { provider: usdtConfig({ address: newAddress }) })
      }
      return usdtReport(url)
    })
    await found('.usdt-card')
    const address = document.getElementById('usdt-address') as HTMLInputElement
    type(address, typo)
    await vi.waitFor(() => expect(usdt().textContent).toContain(text('gateway.usdt.invalid.address_checksum')))
    expect(address.getAttribute('aria-describedby')).toContain('usdt-address-error')
    // the address's own problem comes first
    expect(usdt().querySelector('.address-blocked')).toBeNull()
    expect(usdtSave().disabled).toBe(true)
    expect(usdtTest().disabled).toBe(true)
    type(address, 'TR7NHq')
    await vi.waitFor(() => expect(usdt().textContent).toContain(text('gateway.usdt.invalid.address_format')))
    type(address, '')
    await vi.waitFor(() => expect(usdt().textContent).toContain(text('gateway.usdt.invalid.address_required')))
    expect(usdtSave().disabled).toBe(true)

    // A real new address: said before saving that open payments block it.
    type(address, ' ' + newAddress + ' ')
    await vi.waitFor(() => expect(usdt().querySelector('.address-blocked')?.textContent).toContain(fill(text('gateway.usdt.open_payments'), { n: '۱' })))
    await vi.waitFor(() => expect(usdtSave().disabled).toBe(false))
    usdtSave().click()
    await vi.waitFor(() => expect(usdt().querySelector('.error')?.textContent).toContain(fill(text('gateway.usdt.open_payments'), { n: '۱' })))
    expect(usdt().querySelector('.error')?.textContent).toContain('the address cannot change')
    usdtSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')).toHaveLength(2))
    expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')[1]).toEqual({ enabled: true, address: newAddress, api_key: '', clear_api_key: false, ...keep })
  })

  it('names the field core refused', async () => {
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig()], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/usdt_trc20_auto' && init?.method === 'PUT') return json(400, { error: 'invalid', message: 'address: not a TRON address', field: 'address' })
      return usdtReport(url)
    })
    await found('.usdt-card')
    type(document.getElementById('usdt-address'), newAddress)
    await vi.waitFor(() => expect(usdtSave().disabled).toBe(false))
    usdtSave().click()
    await vi.waitFor(() => expect(usdt().querySelector('#usdt-address-error')?.textContent).toBe('address: not a TRON address'))
  })

  it('tests TronGrid with the typed values, without saving', async () => {
    let ok = true
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig()], settings: payments() }), (url) => {
      if (url === '/api/v1/gateways/usdt_trc20_auto/test') return json(200, ok ? { ok: true, detail: '' } : { ok: false, detail: 'trongrid: 401 invalid api key' })
      return usdtReport(url)
    })
    await found('.usdt-card')
    usdtTest().click()
    await vi.waitFor(() => expect(usdt().querySelector('.test-result')?.textContent).toContain(text('gateway.usdt.test_ok')))
    expect(sent(calls, 'POST', '/gateways/usdt_trc20_auto/test')).toEqual([{ address: store, api_key: '', clear_api_key: false, ...keep }])

    // a typed key and address are tested as typed
    ok = false
    ;(field('usdt-api-key').querySelector('button.replace') as HTMLButtonElement).click()
    type(await found('#usdt-api-key'), apiKey)
    type(document.getElementById('usdt-address'), newAddress)
    await vi.waitFor(() => expect(usdt().querySelector('.test-result')).toBeNull())
    usdtTest().click()
    await vi.waitFor(() => expect(usdt().querySelector('.test-result')?.textContent).toContain(text('gateway.usdt.test_failed')))
    expect(usdt().querySelector('.test-result')?.textContent).toContain('trongrid: 401 invalid api key')
    expect(sent(calls, 'POST', '/gateways/usdt_trc20_auto/test')[1]).toEqual({ address: newAddress, api_key: apiKey, clear_api_key: false, ...keep })

    // and without the stored key
    ;(field('usdt-api-key').querySelector('button.keep') as HTMLButtonElement).click()
    ;(await shown(field('usdt-api-key'), 'button.clear')).click()
    await vi.waitFor(() => expect(field('usdt-api-key').querySelector('.state')?.textContent).toBe(text('secret.will_clear')))
    usdtTest().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/usdt_trc20_auto/test')).toHaveLength(3))
    expect(sent(calls, 'POST', '/gateways/usdt_trc20_auto/test')[2]).toEqual({ address: newAddress, api_key: '', clear_api_key: true, ...keep })
    // a test saves nothing
    expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')).toEqual([])
    expect(document.body.innerHTML).not.toContain(apiKey)
  })

  it('needs an address to turn automatic USDT on', async () => {
    const blank = usdtConfig({ enabled: false, configured: false, offered: false, address: '', key_set: false, health: null })
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [blank], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/usdt_trc20_auto' && init?.method === 'PUT') return json(200, { provider: usdtConfig() })
      return usdtReport(url)
    })
    await found('.usdt-card')
    expect(usdt().querySelector('.state')?.textContent).toBe(text('gateway.state.not_set'))
    expect(field('usdt-api-key').querySelector('.state')?.textContent).toBe(text('secret.not_set'))
    expect(usdtTest().disabled).toBe(true)
    ;(document.getElementById('usdt-enabled') as HTMLInputElement).click()
    await vi.waitFor(() => expect(usdt().textContent).toContain(text('gateway.usdt.invalid.needs_address')))
    expect(usdtSave().disabled).toBe(true)
    type(document.getElementById('usdt-address'), store)
    await vi.waitFor(() => expect(usdtSave().disabled).toBe(false))
    usdtSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')).toEqual([{ enabled: true, address: store, api_key: '', clear_api_key: false, ...keep }]))
  })

  it('says why automatic USDT is on but not offered', async () => {
    let problem = 'unreadable'
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig({ offered: false, problem })], settings: payments() }), usdtReport)
    await found('.usdt-card')
    expect(usdt().querySelector('.state')?.textContent).toBe(text('gateway.state.not_offered'))
    expect(usdt().querySelector('.problem')?.textContent).toContain(text('gateway.usdt.problem.unreadable'))
    problem = 'something_new'
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig({ offered: false, problem })], settings: payments() }), usdtReport)
    await found('.usdt-card')
    expect(usdt().querySelector('.problem')?.textContent).toContain(text('gateway.usdt.problem.invalid'))
  })

  it('marks an unmatched deposit handled, with a note, and reads the list again', async () => {
    // one transaction carrying two transfers, and a reason this dashboard does not know
    const second: Deposit = { ...deposits[0], event_index: 0, amount: { amount: 1_500_000, currency: 'USDT' }, reason: 'new_reason', reason_code: 'new_reason' }
    let open: Deposit[] = [...deposits, second]
    let refuse = false
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig()], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/deposits/resolve' && init?.method === 'POST') {
        if (refuse) return json(409, { error: 'state', message: 'this transfer is manual, not unmatched' })
        const d = open.find((x) => x.txid === b.txid && x.event_index === b.event_index)
        open = open.filter((x) => x !== d)
        return json(200, { deposit: { ...d, outcome: 'resolved', resolved_at: t0, note: b.note } })
      }
      return usdtReportOf(() => open)(url)
    })
    await found('.usdt-card')
    await shown(usdt(), '.unmatched-table')
    const rows = () => [...usdt().querySelectorAll('.unmatched-table .p-datatable-tbody tr')]
    const row = (amount: string) => rows().find((r) => r.querySelector('.amount')?.textContent === amount) as HTMLElement
    const reports = () => calls.filter((c) => c.startsWith('GET /api/v1/gateways/report?') && c.includes('provider=usdt_trc20_auto'))
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    expect(usdt().querySelector('.unmatched-count')?.textContent).toBe('۳')
    expect(usdt().querySelector('.unmatched-more')).toBeNull()
    // an unknown reason: core's words, left to right
    expect(row('1.5 USDT').querySelector('.reason')?.textContent).toBe('new_reason')
    expect(row('1.5 USDT').querySelector('.reason')?.classList.contains('app-ltr')).toBe(true)

    // The second transfer of the shared transaction: what it is, a note, a confirm.
    ;(row('12.34 USDT').querySelector('button.resolve') as HTMLButtonElement).click()
    const dialog = await found<HTMLElement>('.p-dialog')
    expect(dialog.textContent).toContain(text('gateway.usdt.resolve_title'))
    expect(dialog.textContent).toContain(text('gateway.usdt.resolve_message'))
    expect(dialog.querySelector('.resolve-facts')?.textContent).toContain('12.34 USDT')
    expect(dialog.querySelector('.resolve-facts')?.textContent).toContain(txid(1))
    const note = dialog.querySelector('textarea') as HTMLTextAreaElement
    expect(note.maxLength).toBe(depositNoteMax)
    type(note, '  به کیف پول ali اضافه شد ')
    const confirm = () => [...document.querySelectorAll('.p-dialog .p-dialog-footer button')].pop() as HTMLButtonElement
    expect(confirm().textContent).toContain(text('gateway.usdt.resolve'))
    expect(confirm().disabled).toBe(false)
    confirm().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/deposits/resolve')).toEqual([{ txid: txid(1), event_index: 1, note: 'به کیف پول ali اضافه شد' }]))
    // the list is read again: the deposit is gone, its sibling stays
    await vi.waitFor(() => expect(rows()).toHaveLength(2))
    expect(reports()).toHaveLength(4)
    expect(row('12.34 USDT')).toBeUndefined()
    expect(row('1.5 USDT')).toBeDefined()
    expect(usdt().querySelector('.unmatched-count')?.textContent).toBe('۲')
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('gateway.usdt.resolved'))
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())

    // Claimed by a manual proof meanwhile: said, and the list is read again.
    refuse = true
    ;(row('1.5 USDT').querySelector('button.resolve') as HTMLButtonElement).click()
    const again = await found<HTMLElement>('.p-dialog')
    expect((again.querySelector('textarea') as HTMLTextAreaElement).value).toBe('')
    confirm().click()
    await vi.waitFor(() => expect(again.textContent).toContain(text('error.state')))
    expect(again.textContent).toContain('this transfer is manual, not unmatched')
    expect(sent(calls, 'POST', '/gateways/deposits/resolve')[1]).toEqual({ txid: txid(1), event_index: 0, note: '' })
    await vi.waitFor(() => expect(reports()).toHaveLength(6))
  })

  it('sets, tests and clears the proxy TronGrid is called through', async () => {
    let current = usdtConfig()
    const proxy = 'http://user:secret-pw@proxy.example.com:3128'
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [current], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/usdt_trc20_auto' && init?.method === 'PUT') {
        current = { ...current, proxy_set: b.clear_proxy ? false : current.proxy_set || !!b.proxy }
        return json(200, { provider: current })
      }
      if (url === '/api/v1/gateways/usdt_trc20_auto/test') return json(200, { ok: true, detail: '' })
      return usdtReport(url)
    })
    await found('.usdt-card')
    expect(field('usdt-proxy').querySelector('.state')?.textContent).toBe(text('secret.not_set'))
    expect(field('usdt-proxy').textContent).toContain(text('gateway.usdt.proxy_help'))
    const input = await found<HTMLInputElement>('#usdt-proxy')
    expect(input.type).toBe('password')
    type(input, 'socks5://proxy.example.com:1080')
    await vi.waitFor(() => expect(field('usdt-proxy').textContent).toContain(text('gateway.invalid.proxy')))
    expect(usdtSave().disabled).toBe(true)
    expect(usdtTest().disabled).toBe(true)
    type(input, ' ' + proxy + ' ')
    await vi.waitFor(() => expect(usdtTest().disabled).toBe(false))
    usdtTest().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/usdt_trc20_auto/test')).toEqual([{ address: store, api_key: '', clear_api_key: false, proxy, clear_proxy: false }]))
    expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')).toEqual([])
    usdtSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')).toEqual([{ enabled: true, address: store, api_key: '', clear_api_key: false, proxy, clear_proxy: false }]))
    // stored: only "set" is shown, never the address and its password
    await vi.waitFor(() => expect(field('usdt-proxy').querySelector('.state')?.textContent).toBe(text('secret.set')))
    expect(document.querySelector('#usdt-proxy')).toBeNull()
    expect(document.body.innerHTML).not.toContain('secret-pw')

    // removed
    ;(await shown(field('usdt-proxy'), 'button.clear')).click()
    await vi.waitFor(() => expect(usdtSave().disabled).toBe(false))
    usdtSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')[1]).toEqual({ enabled: true, address: store, api_key: '', clear_api_key: false, proxy: '', clear_proxy: true }))
    await vi.waitFor(() => expect(field('usdt-proxy').querySelector('.state')?.textContent).toBe(text('secret.not_set')))
    // Zarinpal's proxy is its own
    expect(sent(calls, 'PUT', '/gateways/zarinpal')).toEqual([])
  })

  it('refuses a TronGrid key that core and payments refuse', async () => {
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig({ key_set: false })], settings: payments() }), usdtReport)
    await found('.usdt-card')
    const key = await found<HTMLInputElement>('#usdt-api-key')
    expect(key.maxLength).toBe(128)
    type(key, 'abcd_efgh_ijkl_mnop')
    await vi.waitFor(() => expect(field('usdt-api-key').textContent).toContain(text('gateway.usdt.invalid.api_key')))
    expect(usdtSave().disabled).toBe(true)
    expect(usdtTest().disabled).toBe(true)
    type(key, apiKey)
    await vi.waitFor(() => expect(usdtSave().disabled).toBe(false))
  })

  it('keeps automatic USDT read only while the payments key is missing', async () => {
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig({ key_missing: true })], settings: payments() }), (url) => {
      if (url === '/api/v1/gateways/usdt_trc20_auto/test') return json(200, { ok: true, detail: '' })
      return usdtReport(url)
    })
    await found('.usdt-card')
    expect(usdt().querySelector('.key-missing')?.textContent).toContain(text('gateway.key_missing'))
    expect((document.getElementById('usdt-address') as HTMLInputElement).disabled).toBe(true)
    expect((document.getElementById('usdt-enabled') as HTMLInputElement).disabled).toBe(true)
    expect(usdt().querySelector('button.replace')).toBeNull()
    expect(usdtSave()).toBeNull()
    usdtTest().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/usdt_trc20_auto/test')).toEqual([{}]))
  })

  it('filters the payments by the automatic method and shows the exact amount sent', async () => {
    const item = {
      id: 'p1', order_id: 'o1', user: { id: 'u2', telegram_id: 2, username: 'ali' }, provider: 'usdt_trc20_auto', status: 'succeeded',
      amount: { amount: 5_000_000, currency: 'USDT' }, gateway_amount: { amount: 5_000_123, currency: 'USDT' }, provider_ref: txid(3), failure_reason: '', created_at: t0, proof: null,
    }
    const { calls } = await boot('/admin/payments?tab=history', ['overview.read', 'payments.read'], () => ({}), (url) =>
      url.startsWith('/api/v1/payments?') ? json(200, { items: [item], total: 1, page: 1, size: 25 }) : undefined,
    )
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/payments?page=1&size=25'))
    // the method filter of the history tab (the orders tab has filters too)
    const select = await vi.waitFor(() => {
      const el = [...document.querySelectorAll<HTMLElement>('.p-select')].find((e) => e.querySelector(`[aria-label="${text('payment.method')}"]`))
      expect(el).toBeDefined()
      return el!
    })
    select.click()
    const option = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-select-option')].find((o) => o.textContent?.includes(text('payment.provider.usdt_trc20_auto'))) as HTMLElement | undefined
      expect(el).toBeDefined()
      return el!
    })
    option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/payments?provider=usdt_trc20_auto&page=1&size=25'))
    const row = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-datatable-tbody tr')].find((r) => r.querySelector('.gateway-amount'))
      expect(el).toBeDefined()
      return el!
    })
    expect(row.textContent).toContain(text('payment.provider.usdt_trc20_auto'))
    expect(row.querySelector('.gateway-amount')?.textContent?.trim()).toBe('5.000123 USDT')
    expect(row.querySelector('.txid')?.getAttribute('title')).toBe(txid(3))
  })

  it('lists Aqayepardakht among the methods to filter the payments by, and names it in the row', async () => {
    const item = {
      id: 'p2', order_id: 'o2', user: { id: 'u2', telegram_id: 2, username: 'ali' }, provider: 'aqayepardakht', status: 'succeeded',
      amount: { amount: 150_000, currency: 'IRT' }, gateway_amount: null, provider_ref: '987654321', failure_reason: '', created_at: t0, proof: null,
    }
    const { calls } = await boot('/admin/payments?tab=history', ['overview.read', 'payments.read'], () => ({}), (url) =>
      url.startsWith('/api/v1/payments?') ? json(200, { items: [item], total: 1, page: 1, size: 25 }) : undefined,
    )
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/payments?page=1&size=25'))
    const select = await vi.waitFor(() => {
      const el = [...document.querySelectorAll<HTMLElement>('.p-select')].find((e) => e.querySelector(`[aria-label="${text('payment.method')}"]`))
      expect(el).toBeDefined()
      return el!
    })
    select.click()
    const options = await vi.waitFor(() => {
      const list = [...document.querySelectorAll('.p-select-option')]
      expect(list.length).toBeGreaterThan(5)
      return list
    })
    // after NextPay, before the automatic USDT
    const labels = options.map((o) => o.textContent?.trim())
    expect(labels.indexOf(text('payment.provider.aqayepardakht'))).toBe(labels.indexOf(text('payment.provider.nextpay')) + 1)
    expect(labels.indexOf(text('payment.provider.usdt_trc20_auto'))).toBe(labels.indexOf(text('payment.provider.aqayepardakht')) + 1)
    options.find((o) => o.textContent?.trim() === text('payment.provider.aqayepardakht'))!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/payments?provider=aqayepardakht&page=1&size=25'))
    // the row names the gateway, not a key of the catalog
    const row = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-datatable-tbody tr')].find((r) => r.textContent?.includes(text('payment.provider.aqayepardakht')))
      expect(el).toBeDefined()
      return el!
    })
    expect(row.textContent).not.toContain('payment.provider.')
    expect(row.querySelector('.app-mono')?.textContent).toBe('987654321')
  })

  // ---- Automatic TON
  const ton = () => document.querySelector('.ton-card') as HTMLElement
  const tonSave = () => ton().querySelector('button.save') as HTMLButtonElement
  const tonTest = () => ton().querySelector('button.test') as HTMLButtonElement
  type TonDeposit = Deposit & { network: string; to: string; comment: string; lt: number }
  const tonDeposit = (n: number, over: Partial<TonDeposit> = {}): TonDeposit => ({
    txid: txid(n), event_index: 0, network: 'TON', from: tonSender, to: tonStore, amount: { amount: 1_000_000_000, currency: 'TON' }, at: t0 - 60 * n, comment: 'BOB-3F9A1C20', lt: 1000 + n, ...over,
  })
  // three transfers that matched nothing: less than asked, with no comment, and a stranger's comment that is not text
  const tonDeposits: TonDeposit[] = [
    tonDeposit(5, { amount: { amount: 2_531_000_000, currency: 'TON' }, at: t0 - 120, reason: 'less TON than the payment asked for', reason_code: 'underpaid' }),
    tonDeposit(6, { amount: { amount: 1_000_000_000, currency: 'TON' }, at: t0 - 3600, comment: '', reason: 'no open payment has this comment', reason_code: 'no_payment' }),
    tonDeposit(7, { amount: { amount: 50_000_000, currency: 'TON' }, at: t0 - 7200, comment: '<b>سلام</b> BOB-XXXX', reason_code: 'late_price' }),
  ]
  const tonReportOf = (list: () => TonDeposit[], count?: () => number, extra: Record<string, unknown> = {}) => (url: string) => {
    if (!url.startsWith('/api/v1/gateways/report?') || !url.includes('provider=ton_auto')) return undefined
    const q = new URLSearchParams(url.split('?')[1])
    const days = (Number(q.get('to')) - Number(q.get('from'))) / 86_400
    return json(200, {
      provider: 'ton_auto', from: Number(q.get('from')), to: Number(q.get('to')), healthy: true, open: 1, oldest_open_at: t0 - 600, last_paid_at: t0 - 300, last_error_at: null, last_error: '',
      last_scan_at: t0 - 20, last_scan_ok_at: t0 - 20,
      totals: [{ status: 'succeeded', count: days === 7 ? 2 : 6, amounts: [{ amount: days === 7 ? 2_531_000_000 : 9_000_000_000, currency: 'TON' }] }],
      unmatched: list(), unmatched_count: count ? count() : list().length, ...extra,
    })
  }
  // 12 to handle; the list holds the newest
  const tonReport = tonReportOf(() => tonDeposits, () => 12)
  const noOpen = (c: GatewayConfig) => ({ ...c.health!, open: 0, oldest_open_at: null })
  const faAgo = (n: number, unit: Intl.RelativeTimeFormatUnit) => new Intl.RelativeTimeFormat('fa-IR', { numeric: 'always' }).format(-n, unit)

  it('shows automatic TON after USDT, with its address, the price, the key only as set, and its unmatched deposits', async () => {
    let current = tonConfig()
    const scanned = usdtConfig({ health: { ...usdtConfig().health!, last_scan_at: t0 - 20, last_scan_ok_at: t0 - 20 } })
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [config(), scanned, current], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/ton_auto' && init?.method === 'PUT') {
        current = { ...current, enabled: b.enabled as boolean, key_set: true }
        return json(200, { provider: current })
      }
      return usdtReport(url) ?? tonReport(url)
    })
    await found('.ton-card')
    const cards = [...document.querySelectorAll('.cards > .wide')]
    expect(cards.indexOf(ton())).toBe(cards.indexOf(usdt()) + 1)
    expect(ton().querySelector('.card-title')?.textContent).toBe(text('gateway.ton.title'))
    expect(ton().querySelector('.state')?.textContent).toBe(text('gateway.state.on'))
    expect(ton().textContent).toContain(text('gateway.ton.desc'))
    // the address is public: shown whole, left to right, as saved; the key is only "set"
    const address = document.getElementById('ton-address') as HTMLInputElement
    expect(address.value).toBe(tonStore)
    expect(address.dir).toBe('ltr')
    expect(ton().querySelector('#ton-address-saved-as')).toBeNull()
    expect(ton().textContent).toContain(text('gateway.ton.address_help'))
    expect(field('ton-api-key').querySelector('.state')?.textContent).toBe(text('secret.set'))
    expect(document.querySelector('#ton-api-key')).toBeNull()
    expect(ton().querySelector('.open-count')?.textContent).toContain(fill(text('gateway.open_count'), { n: '۱' }))
    expect(ton().querySelector('.health-state')?.textContent).toBe(text('gateway.health.ok'))
    // when the address was last read is told for TON only
    expect(ton().querySelector('.last-scan')?.textContent).toBe(fill(text('gateway.last_scan'), { time: dateTime(t0 - 20, 'fa') }))
    await found('.usdt-card .health-state')
    expect(usdt().querySelector('.last-scan')).toBeNull()

    // the live price: the value left to right with its code, where it came from, how old
    expect(ton().querySelector('.price-title')?.textContent).toBe(text('gateway.ton.price.title'))
    expect(ton().querySelector('.price-state')?.textContent).toBe(text('gateway.ton.price.fresh'))
    expect(ton().querySelector('.price-value')?.textContent).toBe('3.2145 USDT')
    expect(ton().querySelector('.price-value')?.classList.contains('app-ltr')).toBe(true)
    expect(ton().querySelector('.price-source')?.textContent).toBe(text('gateway.ton.source.binance'))
    expect(ton().querySelector('.price-age')?.textContent).toBe(faAgo(10, 'minute'))
    expect(ton().querySelector('.price-warning')).toBeNull()

    // the report asks for this provider and shows its totals in TON
    await vi.waitFor(() => expect(ton().querySelector('.month-succeeded')?.textContent).toBe('۶'))
    const reports = calls.filter((c) => c.startsWith('GET /api/v1/gateways/report?') && c.includes('provider=ton_auto'))
    expect(reports).toHaveLength(2)
    expect(ton().querySelector('.report-table')?.textContent).toContain('۹ تون')
    expect(ton().querySelector('.report-table')?.textContent).toContain('۲٫۵۳ تون')

    // the deposits that matched nothing, the newest first, with the comment each came with
    const table = await shown(ton(), '.unmatched-table')
    expect(ton().querySelector('#ton-unmatched-title')?.textContent).toBe(text('gateway.usdt.unmatched'))
    expect(usdt().querySelector('#usdt-unmatched-title')).not.toBeNull()
    expect(ton().querySelector('.unmatched-count')?.textContent).toBe('۱۲')
    expect(ton().querySelector('.unmatched-more')?.textContent?.trim()).toBe(fill(text('gateway.usdt.unmatched_more'), { shown: '۳', n: '۱۲' }))
    expect(ton().querySelector('.unmatched')?.textContent).toContain(text('gateway.ton.unmatched_help'))
    expect(ton().querySelector('.unmatched')?.textContent).not.toContain(text('gateway.usdt.unmatched_help'))
    expect([...table.querySelectorAll('thead th')].map((th) => th.textContent?.trim())).toContain(text('gateway.ton.comment'))
    const rows = [...table.querySelectorAll('.p-datatable-tbody tr')]
    expect(rows).toHaveLength(3)
    // exact amounts with the code TON (up to 9 decimals), in Latin digits
    expect(rows.map((r) => r.querySelector('.amount')?.textContent)).toEqual(['2.531 TON', '1 TON', '0.05 TON'])
    // why, in the staff member's language and TON's own words (core's English is not shown)
    expect(rows[0].querySelector('.reason')?.textContent).toBe(text('gateway.ton.reason.underpaid'))
    expect(rows[1].querySelector('.reason')?.textContent).toBe(text('gateway.ton.reason.no_payment'))
    expect(rows[2].querySelector('.reason')?.textContent).toBe(text('gateway.ton.reason.late_price'))
    expect(rows[0].textContent).not.toContain('less TON than the payment asked for')
    expect(rows[0].querySelector('.reason')?.classList.contains('app-ltr')).toBe(false)
    // the comment: monospace, left to right; none is said; whatever else was written is text, never markup
    expect(rows[0].querySelector('.comment')?.textContent).toBe('BOB-3F9A1C20')
    expect(rows[0].querySelector('.comment')?.classList.contains('app-ltr')).toBe(true)
    expect(rows[0].querySelector('.comment')?.classList.contains('app-mono')).toBe(true)
    expect(rows[1].querySelector('.comment')).toBeNull()
    expect(rows[1].querySelector('.no-comment')?.textContent).toBe(text('gateway.ton.comment_none'))
    expect(rows[2].querySelector('.comment')?.textContent).toBe('<b>سلام</b> BOB-XXXX')
    expect(rows[2].querySelector('b')).toBeNull()
    expect(rows[0].querySelector('.from')?.textContent).toBe(tonSender)
    expect(rows[0].querySelector('.txid')?.textContent).toBe(txid(5))
    expect(rows[0].querySelector('button.resolve')?.textContent).toContain(text('gateway.usdt.resolve'))
    // the transaction on Tonviewer, not Tronscan
    const link = rows[0].querySelector('a.tonviewer') as HTMLAnchorElement
    expect(link.href).toBe('https://tonviewer.com/transaction/' + txid(5))
    expect(link.getAttribute('aria-label')).toBe(text('gateway.ton.tonviewer'))
    expect(link.rel).toContain('noopener')
    expect(link.target).toBe('_blank')
    expect(ton().querySelector('a.tronscan')).toBeNull()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    ;(rows[1].querySelector('button.copy') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(txid(6)))
    // the USDT card keeps Tronscan and has no comment column
    await shown(usdt(), '.unmatched-table')
    expect(usdt().querySelector('a.tronscan')).not.toBeNull()
    expect(usdt().querySelector('a.tonviewer')).toBeNull()
    expect(usdt().querySelector('.comment')).toBeNull()
    expect(usdt().querySelector('.unmatched')?.textContent).toContain(text('gateway.usdt.unmatched_help'))

    // A new key: checked first, sent once, then gone from the page.
    ;(field('ton-api-key').querySelector('button.replace') as HTMLButtonElement).click()
    const key = await found<HTMLInputElement>('#ton-api-key')
    expect(key.type).toBe('password')
    type(key, 'not a key!')
    await vi.waitFor(() => expect(field('ton-api-key').textContent).toContain(text('gateway.ton.invalid.api_key')))
    expect(tonSave().disabled).toBe(true)
    type(key, tonKey)
    await vi.waitFor(() => expect(tonSave().disabled).toBe(false))
    tonSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/ton_auto')).toEqual([{ enabled: true, address: tonStore, api_key: tonKey, clear_api_key: false, ...keep }]))
    await vi.waitFor(() => expect(document.querySelector('#ton-api-key')).toBeNull())
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('gateway.ton.saved'))
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/gateways')).toHaveLength(2))
    expect(document.body.innerHTML).not.toContain(tonKey)
    // no other gateway was touched
    expect(sent(calls, 'PUT', '/gateways/usdt_trc20_auto')).toEqual([])
    expect(sent(calls, 'PUT', '/gateways/zarinpal')).toEqual([])
  })

  it('says TON is not offered while the price is not fresh or not there', async () => {
    let price: GatewayConfig['price'] = { usdt_per_ton: '3.2145', source: 'okx', at: Math.floor(Date.now() / 1000) - 3600, fresh: false }
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig({ offered: false, price })], settings: payments() }), tonReport)
    await found('.ton-card')
    expect(ton().querySelector('.state')?.textContent).toBe(text('gateway.state.not_offered'))
    expect(ton().querySelector('.price-state')?.textContent).toBe(text('gateway.ton.price.stale'))
    expect(ton().querySelector('.price-warning')?.textContent).toContain(text('gateway.ton.price.warn_stale'))
    // the last price read is still told, with where it came from and how old it is
    expect(ton().querySelector('.price-value')?.textContent).toBe('3.2145 USDT')
    expect(ton().querySelector('.price-source')?.textContent).toBe('OKX')
    expect(ton().querySelector('.price-age')?.textContent).toBe(faAgo(60, 'minute'))
    // none has been read yet
    price = null
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig({ offered: false, price })], settings: payments() }), tonReport)
    await found('.ton-card')
    expect(ton().querySelector('.price-state')?.textContent).toBe(text('gateway.ton.price.none'))
    expect(ton().querySelector('.price-warning')?.textContent).toContain(text('gateway.ton.price.warn_none'))
    expect(ton().querySelector('.price-facts')).toBeNull()
    // an older core that sends no price at all says the same
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig({ price: undefined })], settings: payments() }), tonReport)
    await found('.ton-card')
    expect(ton().querySelector('.price-warning')?.textContent).toContain(text('gateway.ton.price.warn_none'))
  })

  it('refuses a mistyped, test network or masterchain address in the browser, before anything is sent', async () => {
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig()], settings: payments() }), tonReport)
    await found('.ton-card')
    const address = document.getElementById('ton-address') as HTMLInputElement
    const cases: [string, string][] = [
      [tonTypo, 'checksum'],
      ['TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t', 'format'], // a TRON address
      ['0:' + 'ab'.repeat(31), 'format'],
      [tonTestnet, 'testnet'],
      [tonMasterchain, 'workchain'],
      ['-1:' + 'ab'.repeat(32), 'workchain'],
      ['', 'required'],
    ]
    for (const [typed, problem] of cases) {
      type(address, typed)
      await vi.waitFor(() => expect(ton().querySelector('#ton-address-error')?.textContent).toBe(text('gateway.ton.invalid.address_' + problem)))
      // the message never repeats what was typed
      if (typed) expect(ton().textContent).not.toContain(typed)
      expect(address.getAttribute('aria-describedby')).toContain('ton-address-error')
      expect(ton().querySelector('#ton-address-saved-as')).toBeNull()
      expect(tonSave().disabled).toBe(true)
      expect(tonTest().disabled).toBe(true)
    }
    // nothing was sent, not even for a test
    expect(sent(calls, 'PUT', '/gateways/ton_auto')).toEqual([])
    expect(sent(calls, 'POST', '/gateways/ton_auto/test')).toEqual([])
    // a right one clears the message
    type(address, tonNewEQ)
    await vi.waitFor(() => expect(ton().querySelector('#ton-address-error')).toBeNull())
  })

  it('takes the address in any form a wallet shows, says how it will be saved, and sends that form', async () => {
    let current = tonConfig({ health: noOpen(tonConfig()) })
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [current], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/ton_auto' && init?.method === 'PUT') {
        current = { ...current, address: b.address as string }
        return json(200, { provider: current })
      }
      return tonReport(url)
    })
    await found('.ton-card')
    const address = document.getElementById('ton-address') as HTMLInputElement
    const savedAs = () => ton().querySelector('#ton-address-saved-as')?.textContent
    // the saved account in another form: said how it is saved, and nothing to save
    type(address, tonStoreEQ)
    await vi.waitFor(() => expect(savedAs()).toBe(fill(text('gateway.ton.address_saved_as'), { address: '⁨' + tonStore + '⁩' })))
    expect(address.getAttribute('aria-describedby')).toContain('ton-address-saved-as')
    expect(ton().querySelector('#ton-address-error')).toBeNull()
    expect(tonSave().disabled).toBe(true)
    // a raw address of another account: saved in the UQ… form
    type(address, ' ' + tonNewRaw + ' ')
    await vi.waitFor(() => expect(savedAs()).toBe(fill(text('gateway.ton.address_saved_as'), { address: '⁨' + tonNew + '⁩' })))
    expect(ton().querySelector('.address-blocked')).toBeNull()
    await vi.waitFor(() => expect(tonSave().disabled).toBe(false))
    // the same account written in the saved form says nothing
    type(address, tonNew)
    await vi.waitFor(() => expect(savedAs()).toBeUndefined())
    type(address, tonNewEQ)
    await vi.waitFor(() => expect(savedAs()).toBeDefined())
    // a test uses the form it will be saved in
    tonTest().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/ton_auto/test')).toEqual([{ address: tonNew, api_key: '', clear_api_key: false, ...keep }]))
    tonSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/ton_auto')).toEqual([{ enabled: true, address: tonNew, api_key: '', clear_api_key: false, ...keep }]))
    // the page reads the settings again and the field shows what was saved
    await vi.waitFor(() => expect(address.value).toBe(tonNew))
    expect(savedAs()).toBeUndefined()
    expect(tonSave().disabled).toBe(true)
  })

  it('refuses a new TON address while automatic payments are open, before saving and when payments refuses', async () => {
    let refuse = true
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig()], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/ton_auto' && init?.method === 'PUT') {
        if (refuse) {
          refuse = false
          return json(409, { error: 'state', message: '1 open ton_auto payments: the address cannot change', open: 1 })
        }
        return json(200, { provider: tonConfig({ address: tonNew }) })
      }
      return tonReport(url)
    })
    await found('.ton-card')
    const address = document.getElementById('ton-address') as HTMLInputElement
    // the address's own problem comes first
    type(address, tonTypo)
    await vi.waitFor(() => expect(ton().textContent).toContain(text('gateway.ton.invalid.address_checksum')))
    expect(ton().querySelector('.address-blocked')).toBeNull()
    // a real new address, in any form: said before saving that open payments block it
    type(address, ' ' + tonNewEQ + ' ')
    await vi.waitFor(() => expect(ton().querySelector('.address-blocked')?.textContent).toContain(fill(text('gateway.ton.open_payments'), { n: '۱' })))
    await vi.waitFor(() => expect(tonSave().disabled).toBe(false))
    tonSave().click()
    await vi.waitFor(() => expect(ton().querySelector('.error')?.textContent).toContain(fill(text('gateway.ton.open_payments'), { n: '۱' })))
    expect(ton().querySelector('.error')?.textContent).toContain('the address cannot change')
    tonSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/ton_auto')).toHaveLength(2))
    expect(sent(calls, 'PUT', '/gateways/ton_auto')[1]).toEqual({ enabled: true, address: tonNew, api_key: '', clear_api_key: false, ...keep })
    // saved at the second try: the refusal is gone
    await vi.waitFor(() => expect(ton().querySelector('.error')).toBeNull())
  })

  it('names the field core refused', async () => {
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig({ health: noOpen(tonConfig()) })], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/ton_auto' && init?.method === 'PUT') return json(400, { error: 'invalid', message: 'address: not a TON address', field: 'address' })
      return tonReport(url)
    })
    await found('.ton-card')
    type(document.getElementById('ton-address'), tonNewRaw)
    await vi.waitFor(() => expect(tonSave().disabled).toBe(false))
    tonSave().click()
    await vi.waitFor(() => expect(ton().querySelector('#ton-address-error')?.textContent).toBe('address: not a TON address'))
  })

  it('tests TON Center with the typed values, without saving, and tells the latest transaction and the answer time', async () => {
    let answer: Record<string, unknown> = { ok: true, detail: '', unverified: 0, latency_ms: 183, latest_tx_at: t0 - 3600 }
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig()], settings: payments() }), (url) => {
      if (url === '/api/v1/gateways/ton_auto/test') return json(200, answer)
      return tonReport(url)
    })
    await found('.ton-card')
    // a wallet with a history
    tonTest().click()
    await vi.waitFor(() => expect(ton().querySelector('.test-result')?.textContent).toContain(text('gateway.ton.test_ok')))
    const notes = () => [...ton().querySelectorAll('.test-result .test-note')].map((n) => n.textContent)
    expect(notes()).toEqual([fill(text('gateway.ton.test_latest'), { time: '⁨' + dateTime(t0 - 3600, 'fa') + '⁩' }), fill(text('gateway.ton.test_latency'), { ms: '۱۸۳' })])
    expect(ton().querySelector('.test-result')?.classList.contains('p-message-success')).toBe(true)
    expect(sent(calls, 'POST', '/gateways/ton_auto/test')).toEqual([{ address: tonStore, api_key: '', clear_api_key: false, ...keep }])

    // a new wallet has no transaction yet: said so, still a success
    answer = { ok: true, detail: '', unverified: 0, latency_ms: 90, latest_tx_at: null }
    tonTest().click()
    await vi.waitFor(() => expect(notes()[0]).toBe(text('gateway.ton.test_no_tx')))
    expect(notes()).toEqual([text('gateway.ton.test_no_tx'), fill(text('gateway.ton.test_latency'), { ms: '۹۰' })])
    expect(ton().querySelector('.test-result')?.textContent).toContain(text('gateway.ton.test_ok'))

    // an older core that tells neither says nothing more
    answer = { ok: true, detail: '' }
    tonTest().click()
    await vi.waitFor(() => expect(ton().querySelector('.test-result')?.textContent).toContain(text('gateway.ton.test_ok')))
    expect(notes()).toEqual([])

    // a typed key and address are tested as typed, and a failure says why
    answer = { ok: false, detail: 'toncenter: 401 invalid api key' }
    ;(field('ton-api-key').querySelector('button.replace') as HTMLButtonElement).click()
    type(await found('#ton-api-key'), tonKey)
    type(document.getElementById('ton-address'), tonNewRaw)
    await vi.waitFor(() => expect(ton().querySelector('.test-result')).toBeNull())
    tonTest().click()
    await vi.waitFor(() => expect(ton().querySelector('.test-result')?.textContent).toContain(text('gateway.ton.test_failed')))
    expect(ton().querySelector('.test-result')?.textContent).toContain('toncenter: 401 invalid api key')
    expect(ton().querySelector('.test-result .detail')?.classList.contains('app-ltr')).toBe(true)
    expect(ton().querySelector('.test-result')?.classList.contains('p-message-error')).toBe(true)
    expect(notes()).toEqual([])
    expect(sent(calls, 'POST', '/gateways/ton_auto/test')[3]).toEqual({ address: tonNew, api_key: tonKey, clear_api_key: false, ...keep })

    // and without the stored key
    ;(field('ton-api-key').querySelector('button.keep') as HTMLButtonElement).click()
    ;(await shown(field('ton-api-key'), 'button.clear')).click()
    await vi.waitFor(() => expect(field('ton-api-key').querySelector('.state')?.textContent).toBe(text('secret.will_clear')))
    tonTest().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/ton_auto/test')).toHaveLength(5))
    expect(sent(calls, 'POST', '/gateways/ton_auto/test')[4]).toEqual({ address: tonNew, api_key: '', clear_api_key: true, ...keep })
    // a test saves nothing, and the key typed stays out of the page
    expect(sent(calls, 'PUT', '/gateways/ton_auto')).toEqual([])
    expect(document.body.innerHTML).not.toContain(tonKey)
  })

  it('needs an address to turn automatic TON on', async () => {
    const blank = tonConfig({ enabled: false, configured: false, offered: false, address: '', key_set: false, health: null, price: null })
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [blank], settings: payments() }), (url, init) => {
      if (url === '/api/v1/gateways/ton_auto' && init?.method === 'PUT') return json(200, { provider: tonConfig() })
      return tonReport(url)
    })
    await found('.ton-card')
    expect(ton().querySelector('.state')?.textContent).toBe(text('gateway.state.not_set'))
    expect(field('ton-api-key').querySelector('.state')?.textContent).toBe(text('secret.not_set'))
    expect(tonTest().disabled).toBe(true)
    ;(document.getElementById('ton-enabled') as HTMLInputElement).click()
    await vi.waitFor(() => expect(ton().textContent).toContain(text('gateway.ton.invalid.needs_address')))
    expect(tonSave().disabled).toBe(true)
    type(document.getElementById('ton-address'), tonStoreEQ)
    await vi.waitFor(() => expect(tonSave().disabled).toBe(false))
    tonSave().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/gateways/ton_auto')).toEqual([{ enabled: true, address: tonStore, api_key: '', clear_api_key: false, ...keep }]))
  })

  it('says why automatic TON is on but not offered', async () => {
    let problem = 'unreadable'
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig({ offered: false, problem })], settings: payments() }), tonReport)
    await found('.ton-card')
    expect(ton().querySelector('.state')?.textContent).toBe(text('gateway.state.not_offered'))
    expect(ton().querySelector('.problem')?.textContent).toContain(text('gateway.ton.problem.unreadable'))
    problem = 'something_new'
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig({ offered: false, problem })], settings: payments() }), tonReport)
    await found('.ton-card')
    expect(ton().querySelector('.problem')?.textContent).toContain(text('gateway.ton.problem.invalid'))
  })

  it('tells why the address could not be read, for TON only', async () => {
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig(), tonConfig()], settings: payments() }), (url) => {
      // core reports the same fields for USDT, which this dashboard does not show
      if (url.startsWith('/api/v1/gateways/report?') && url.includes('provider=usdt_trc20_auto')) {
        const q = new URLSearchParams(url.split('?')[1])
        return json(200, {
          provider: 'usdt_trc20_auto', from: Number(q.get('from')), to: Number(q.get('to')), healthy: true, open: 0, oldest_open_at: null, last_paid_at: null, last_error_at: null, last_error: '',
          totals: [], unmatched: [], unmatched_count: 0, scan_error: 'trongrid: too many requests (HTTP 429)',
        })
      }
      return tonReportOf(() => tonDeposits, undefined, { scan_error: 'toncenter: HTTP 429' })(url)
    })
    await found('.ton-card')
    await vi.waitFor(() => expect(ton().querySelector('.scan-error')?.textContent).toContain(text('gateway.scan_error')))
    expect(ton().querySelector('.scan-error .detail')?.textContent).toBe('toncenter: HTTP 429')
    expect(ton().querySelector('.scan-error .detail')?.classList.contains('app-ltr')).toBe(true)
    // the USDT card is as it was
    await found('.usdt-card .report-table')
    expect(usdt().querySelector('.scan-error')).toBeNull()
  })

  it('marks an unmatched TON deposit handled, sends its network, and reads the list again', async () => {
    let open: TonDeposit[] = [...tonDeposits]
    let refuse = false
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [usdtConfig(), tonConfig()], settings: payments() }), (url, init, b) => {
      if (url === '/api/v1/gateways/deposits/resolve' && init?.method === 'POST') {
        if (refuse) return json(409, { error: 'state', message: 'this transfer is not unmatched any more' })
        const d = open.find((x) => x.txid === b.txid && x.event_index === b.event_index)
        open = open.filter((x) => x !== d)
        return json(200, { deposit: { ...d, outcome: 'resolved', resolved_at: t0, note: b.note } })
      }
      return usdtReport(url) ?? tonReportOf(() => open)(url)
    })
    await found('.ton-card')
    await shown(ton(), '.unmatched-table')
    const rows = () => [...ton().querySelectorAll('.unmatched-table .p-datatable-tbody tr')]
    const row = (amount: string) => rows().find((r) => r.querySelector('.amount')?.textContent === amount) as HTMLElement
    const reports = () => calls.filter((c) => c.startsWith('GET /api/v1/gateways/report?') && c.includes('provider=ton_auto'))
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    expect(reports()).toHaveLength(2)

    ;(row('2.531 TON').querySelector('button.resolve') as HTMLButtonElement).click()
    const dialog = await found<HTMLElement>('.p-dialog')
    expect(dialog.textContent).toContain(text('gateway.usdt.resolve_title'))
    // TON's own words: no mention of USDT
    expect(dialog.textContent).toContain(text('gateway.ton.resolve_message'))
    expect(dialog.textContent).not.toContain(text('gateway.usdt.resolve_message'))
    const facts = dialog.querySelector('.resolve-facts')
    expect(facts?.textContent).toContain('2.531 TON')
    expect(facts?.textContent).toContain(txid(5))
    expect(facts?.textContent).toContain(tonSender)
    expect(facts?.querySelector('.resolve-comment')?.textContent).toBe('BOB-3F9A1C20')
    const note = dialog.querySelector('textarea') as HTMLTextAreaElement
    expect(note.maxLength).toBe(depositNoteMax)
    type(note, '  تون به کیف پول ali اضافه شد ')
    const confirm = () => [...document.querySelectorAll('.p-dialog .p-dialog-footer button')].pop() as HTMLButtonElement
    confirm().click()
    await vi.waitFor(() =>
      expect(sent(calls, 'POST', '/gateways/deposits/resolve')).toEqual([{ txid: txid(5), event_index: 0, note: 'تون به کیف پول ali اضافه شد', network: 'TON' }]),
    )
    // the list is read again: the deposit is gone
    await vi.waitFor(() => expect(rows()).toHaveLength(2))
    expect(reports()).toHaveLength(4)
    expect(row('2.531 TON')).toBeUndefined()
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('gateway.usdt.resolved'))
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull(), { timeout: 5000 })

    // a deposit without a comment says so in the dialog; refused meanwhile: said, and the list is read again
    refuse = true
    ;(row('1 TON').querySelector('button.resolve') as HTMLButtonElement).click()
    const again = await found<HTMLElement>('.p-dialog')
    expect(again.querySelector('.resolve-facts')?.textContent).toContain(text('gateway.ton.comment_none'))
    confirm().click()
    await vi.waitFor(() => expect(again.textContent).toContain(text('error.state')))
    expect(again.textContent).toContain('this transfer is not unmatched any more')
    expect(sent(calls, 'POST', '/gateways/deposits/resolve')[1]).toEqual({ txid: txid(6), event_index: 0, note: '', network: 'TON' })
    await vi.waitFor(() => expect(reports()).toHaveLength(6))
  })

  it('keeps automatic TON read only while the payments key is missing', async () => {
    const { calls } = await boot('/admin/gateways', ownerPerms, () => ({ providers: [tonConfig({ key_missing: true })], settings: payments() }), (url) => {
      if (url === '/api/v1/gateways/ton_auto/test') return json(200, { ok: true, detail: '', latency_ms: 40, latest_tx_at: null })
      return tonReport(url)
    })
    await found('.ton-card')
    expect(ton().querySelector('.key-missing')?.textContent).toContain(text('gateway.key_missing'))
    expect((document.getElementById('ton-address') as HTMLInputElement).disabled).toBe(true)
    expect((document.getElementById('ton-enabled') as HTMLInputElement).disabled).toBe(true)
    expect(ton().querySelector('button.replace')).toBeNull()
    expect(tonSave()).toBeNull()
    tonTest().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gateways/ton_auto/test')).toEqual([{}]))
  })

  it('filters the payments by TON, in its place among the methods, and shows the exact amount sent', async () => {
    const item = {
      id: 'p3', order_id: 'o3', user: { id: 'u2', telegram_id: 2, username: 'ali' }, provider: 'ton_auto', status: 'succeeded',
      amount: { amount: 150_000, currency: 'IRT' }, gateway_amount: { amount: 2_531_000_000, currency: 'TON' }, provider_ref: txid(7), failure_reason: '', created_at: t0, proof: null,
    }
    const { calls } = await boot('/admin/payments?tab=history', ['overview.read', 'payments.read'], () => ({}), (url) =>
      url.startsWith('/api/v1/payments?') ? json(200, { items: [item], total: 1, page: 1, size: 25 }) : undefined,
    )
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/payments?page=1&size=25'))
    const select = await vi.waitFor(() => {
      const el = [...document.querySelectorAll<HTMLElement>('.p-select')].find((e) => e.querySelector(`[aria-label="${text('payment.method')}"]`))
      expect(el).toBeDefined()
      return el!
    })
    select.click()
    const options = await vi.waitFor(() => {
      const list = [...document.querySelectorAll('.p-select-option')]
      expect(list.length).toBeGreaterThan(5)
      return list
    })
    // after the automatic USDT, before Stars
    const labels = options.map((o) => o.textContent?.trim())
    expect(labels.indexOf(text('payment.provider.ton_auto'))).toBe(labels.indexOf(text('payment.provider.usdt_trc20_auto')) + 1)
    expect(labels.indexOf(text('payment.provider.stars'))).toBe(labels.indexOf(text('payment.provider.ton_auto')) + 1)
    options.find((o) => o.textContent?.trim() === text('payment.provider.ton_auto'))!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/payments?provider=ton_auto&page=1&size=25'))
    const row = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-datatable-tbody tr')].find((r) => r.querySelector('.gateway-amount'))
      expect(el).toBeDefined()
      return el!
    })
    expect(row.textContent).toContain(text('payment.provider.ton_auto'))
    expect(row.textContent).not.toContain('payment.provider.')
    // the TON amount whole, in the wallet's own form; the hash is cut by the column, whole in its title
    expect(row.querySelector('.gateway-amount')?.textContent?.trim()).toBe('2.531 TON')
    expect(row.querySelector('.txid')?.getAttribute('title')).toBe(txid(7))
  })

  it('keeps the page from admins', async () => {
    const { router, calls } = await boot('/admin/gateways', adminPerms, () => ({ providers: [], settings: [] }))
    expect(router.currentRoute.value.name).toBe('overview')
    expect(document.querySelector('.sidebar')?.textContent).not.toContain(text('section.gateways.title'))
    expect(calls.some((c) => c.includes('/api/v1/gateways'))).toBe(false)
  })

  it('says when the gateways could not be read, and tries again', async () => {
    let fail = true
    await boot('/admin/gateways', ownerPerms, () => ({ providers: [config()], settings: payments() }), (url) => {
      if (url === '/api/v1/gateways' && fail) {
        fail = false
        return json(503, { error: 'unavailable', message: 'payments down' })
      }
    })
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('error.unavailable')))
    ;([...document.querySelectorAll('button')].find((b) => b.textContent?.includes(text('app.retry'))) as HTMLButtonElement).click()
    await found('.zarinpal-card')
  })
})

// Every text the new screens ask for is in both catalogs.
describe('servers and gateways texts', () => {
  const sources = import.meta.glob(
    ['../pages/ServersPage.vue', '../pages/GatewaysPage.vue', '../pages/PlansPage.vue', '../pages/OverviewPage.vue', '../pages/SettingsPage.vue', '../components/servers/*.vue', '../components/gateways/*.vue', '../components/SecretField.vue'],
    { query: '?raw', import: 'default', eager: true },
  ) as Record<string, string>
  const has = (cat: Record<string, unknown>, key: string) =>
    typeof key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], cat) === 'string'
  it('exist in Persian and English', () => {
    const keys = new Set<string>()
    for (const src of Object.values(sources)) for (const m of src.matchAll(/\bt\('([a-z_.0-9]+)'\s*[,)]/g)) keys.add(m[1])
    expect(keys.size).toBeGreaterThan(100)
    for (const k of keys) {
      expect(has(fa, k), 'fa: ' + k).toBe(true)
      expect(has(en, k), 'en: ' + k).toBe(true)
    }
  })
  it('cover every state, method and setting the pages build keys for', () => {
    const built = [
      ...['ok', 'down', 'unknown', 'off'].map((s) => 'server.health_state.' + s),
      ...['url', 'https', 'credentials'].map((s) => 'server.invalid.' + s),
      ...['ok', 'error', 'idle'].map((s) => 'gateway.health.' + s),
      ...['unreadable', 'sandbox_refused', 'public_url', 'invalid'].map((s) => 'gateway.problem.' + s),
      ...['unreadable', 'sandbox_refused', 'public_url', 'invalid'].map((s) => 'gateway.keyed.problem.' + s),
      ...keyedProviders.map((p) => 'gateway.' + p + '_desc'),
      ...['label', 'help', 'sandbox_help', 'sandbox_prod', 'open_payments', 'test_ok'].map((s) => 'gateway.keyed.pin.' + s),
      ...['pin', 'sandbox', 'needs_pin'].map((s) => 'gateway.keyed.pin.invalid.' + s),
      ...['unreadable', 'sandbox_refused', 'invalid'].map((s) => 'gateway.keyed.pin.problem.' + s),
      ...['unreadable', 'invalid'].map((s) => 'gateway.usdt.problem.' + s),
      ...['format', 'checksum', 'required'].map((s) => 'gateway.usdt.invalid.address_' + s),
      ...unmatchedReasons.map((r) => 'gateway.usdt.reason.' + r),
      ...['unreadable', 'invalid'].map((s) => 'gateway.ton.problem.' + s),
      ...['format', 'checksum', 'workchain', 'testnet', 'required'].map((s) => 'gateway.ton.invalid.address_' + s),
      ...tonUnmatchedReasons.map((r) => 'gateway.ton.reason.' + r),
      ...['title', 'label', 'source', 'updated', 'fresh', 'stale', 'none', 'warn_stale', 'warn_none'].map((s) => 'gateway.ton.price.' + s),
      ...['binance', 'okx'].map((s) => 'gateway.ton.source.' + s),
      ...['unmatched_help', 'resolve_message', 'comment', 'comment_none', 'tonviewer'].map((s) => 'gateway.ton.' + s),
      ...['manual_card', 'manual_crypto', 'manual_zarinpal', 'zarinpal', ...keyedProviders, 'usdt_trc20_auto', 'ton_auto', 'stars', 'wallet'].map((p) => 'payment.provider.' + p),
      ...['on', 'off', 'incomplete'].map((s) => 'gateway.method_state.' + s),
      ...methods.flatMap((m) => ['gateway.method.' + m.key + '.title', 'gateway.method.' + m.key + '.desc', `settings.field.${m.toggle}.label`, `settings.field.${m.toggle}.help`]),
      ...['off', 'ok', 'incomplete', 'down'].map((s) => 'overview.gateway_state.' + s),
      ...['server', 'gateway'].flatMap((e) => ['audit.entity.' + e, 'audit.group.' + e]),
      ...['add', 'update', 'delete', 'inbound'].map((a) => 'audit.action.server.' + a),
      'audit.action.gateway.update',
    ]
    for (const k of built) {
      expect(has(fa, k), 'fa: ' + k).toBe(true)
      expect(has(en, k), 'en: ' + k).toBe(true)
    }
  })
})
