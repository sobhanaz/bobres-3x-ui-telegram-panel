import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref, type Component } from 'vue'
import PrimeVue from 'primevue/config'
import fa from '../locales/fa.json'
import en from '../locales/en.json'
import { ApiError } from '../api'
import {
  apiKeyMax,
  apiKeyValid,
  blockedByOpen,
  depositKey,
  depositNoteMax,
  healthState,
  merchantValid,
  methodCards,
  methods,
  methodState,
  normalizeMerchant,
  openPayments,
  overviewGatewayState,
  proxyValid,
  reportRange,
  reportRows,
  resolveBody,
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
const apiKey = '25f66928-0b70-48cd-9ac6-da6f8247c663'
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
    // Zarinpal can fail
    expect(overviewGatewayState(g('zarinpal', true, false))).toBe('down')
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
      ...['unreadable', 'invalid'].map((s) => 'gateway.usdt.problem.' + s),
      ...['format', 'checksum', 'required'].map((s) => 'gateway.usdt.invalid.address_' + s),
      ...unmatchedReasons.map((r) => 'gateway.usdt.reason.' + r),
      ...['manual_card', 'manual_crypto', 'manual_zarinpal', 'zarinpal', 'usdt_trc20_auto', 'stars', 'wallet'].map((p) => 'payment.provider.' + p),
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
