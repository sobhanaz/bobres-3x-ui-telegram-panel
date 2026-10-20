import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Intent, Me, Plan } from '../api'
import Methods from '../components/Methods.vue'
import PayPanel from '../components/PayPanel.vue'
import { setLang } from '../i18n'
import { state } from '../store'
import CheckoutView from '../views/CheckoutView.vue'
import WalletView from '../views/WalletView.vue'
import { chooseFile, clipboard, json, mount, noTelegram, pngBytes, settle, signedIn, telegram, type Call, type Mounted } from './helpers'

const ALL = ['zarinpal', 'idpay', 'nextpay', 'usdt_trc20_auto', 'stars', 'manual_card', 'manual_crypto', 'manual_zarinpal']

const plan: Plan = { id: 'p1', names: { en: 'Gold', fa: 'طلایی' }, kind: 'both', days: 30, traffic_bytes: 50 * 2 ** 30, price: 150000, list_price: 150000, currency: 'IRT', scale: 0, topup: false }
const order = (over: Record<string, unknown> = {}) => ({ id: 'o1', plan_id: 'p1', type: 'new', status: 'pending_payment', amount: 150000, discount_amount: 0, currency: 'IRT', scale: 0, subscription_id: '', ...over })
const intent = (over: Partial<Intent> = {}): Intent => ({
  intent_id: 'i1', status: 'pending', provider: 'manual_card', order_id: 'o1', amount: 150000, currency: 'IRT', scale: 0, pay_url: '',
  gateway_amount: 0, gateway_currency: '', scale_gateway: -1,
  details: { card_number: '6037-9911-2222-3333', card_holder: 'Ali Rezaei', reference: 'AB12CD34' }, ...over,
})
const starsIntent = (): Intent =>
  intent({ provider: 'stars', gateway_amount: 100, gateway_currency: 'XTR', invoice_url: 'https://t.me/$fakeInvoice1', stars: 100, details: { reference: 'AB12CD34' } })

beforeEach(() => {
  setLang('en')
  state.gate = ''
  state.me = { top_up: { currency: 'IRT', min: 10000, max: 50000000 }, brand: { bot_url: '' }, features: {} } as unknown as Me
  state.wallets = [{ currency: 'IRT', balance: 0, scale: 0 }]
  state.ledger = []
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
})

describe('Methods', () => {
  it('lists what the store offers, Stars and the manual methods among them, and no longer sends them to the bot', async () => {
    signedIn(() => ({ methods: ALL }))
    const m = mount(Methods, { currency: 'IRT', amount: 150000, busy: false }, ['pick'])
    await settle(10)
    const labels = m.buttons().map((b) => b.textContent?.trim())
    expect(labels).toEqual([
      'Zarinpal', 'IDPay', 'NextPay', 'USDT (TRC20)', 'Telegram Stars ⭐', 'Card to card', 'USDT, then send a receipt', 'Zarinpal payment link, then send a receipt',
    ])
    expect(m.text()).not.toContain('in the bot only')
    await m.click('Card to card')
    await m.click('Telegram Stars')
    expect(m.events.pick).toEqual([['manual_card'], ['stars']])
    m.unmount()
  })

  it('has a name in Persian for each', async () => {
    setLang('fa')
    signedIn(() => ({ methods: ALL }))
    const m = mount(Methods, { currency: 'IRT', amount: 150000, busy: false }, ['pick'])
    await settle(10)
    const t = m.text()
    expect(t).toContain('استارز تلگرام')
    expect(t).toContain('کارت به کارت')
    expect(t).toContain('تتر (USDT) با ارسال رسید')
    expect(t).toContain('زرین‌پال (لینک پرداخت) با ارسال رسید')
    m.unmount()
  })
})

describe('PayPanel', () => {
  it('shows the screen of the payment: Stars, a manual method, or an online one', async () => {
    telegram({ openInvoice: vi.fn(), isVersionAtLeast: () => true })
    let m = mount(PayPanel, { intent: starsIntent(), checking: false, message: '' }, ['check', 'leave'])
    await settle(5)
    expect(m.text()).toContain('Pay with Telegram Stars')
    m.unmount()
    for (const provider of ['manual_card', 'manual_crypto', 'manual_zarinpal']) {
      m = mount(PayPanel, { intent: intent({ provider, usdt_amount: 1_000_000, details: { zarinpal_link: 'https://zarinp.al/x', usdt_trc20: 'T', card_number: '1', reference: 'R' } }), checking: false, message: '' }, [])
      await settle(5)
      expect(m.text()).toContain('After paying, send the receipt')
      m.unmount()
    }
    m = mount(PayPanel, { intent: intent({ provider: 'zarinpal', pay_url: 'https://pay.example/x', details: {} }), checking: false, message: '' }, ['check'])
    await settle(5)
    expect(m.text()).toContain('Waiting for the payment')
    expect(m.text()).toContain('Open the payment page')
    m.unmount()
  })

  it('passes a check and a leave on, and shows or hides the way out', async () => {
    const m = mount(PayPanel, { intent: intent({ status: 'confirming' }), checking: false, message: '' }, ['check', 'leave'])
    await settle(5)
    await m.click('Check status')
    await m.click('See my services')
    expect(m.events.check).toEqual([[undefined]])
    expect(m.events.leave).toHaveLength(1)
    m.unmount()
    const bare = mount(PayPanel, { intent: intent({ status: 'confirming' }), checking: false, message: '', showLeave: false }, ['leave'])
    await settle(5)
    expect(bare.text()).not.toContain('See my services')
    bare.unmount()
  })
})

// Buying: review, pay, wait.
async function startCheckout(handler: (c: Call) => unknown, flush: () => Promise<unknown>): Promise<Mounted> {
  const m = mount(CheckoutView, { plan, type: 'new' }, ['close', 'services'])
  await flush()
  await m.click('Continue')
  await flush()
  expect(handler).toBeDefined()
  return m
}

describe('buying with Telegram Stars', () => {
  it('opens the invoice and goes to done when the bot has settled the payment', async () => {
    vi.useFakeTimers()
    let ended: (s: string) => void = () => {}
    const open = vi.fn((_url: string, cb: (s: string) => void) => (ended = cb))
    telegram({ openInvoice: open, isVersionAtLeast: () => true })
    let orderStatus = 'pending_payment'
    const calls = signedIn(({ method, path }) => {
      if (path === '/orders' && method === 'POST') return order()
      if (path.startsWith('/methods')) return { methods: ['stars', 'manual_card'] }
      if (path === '/orders/o1/pay') return starsIntent()
      if (path === '/orders/o1') return order({ status: orderStatus })
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: orderStatus === 'active' ? 'succeeded' : 'pending', order_id: 'o1', failure_reason: '' }
      return { wallets: [], ledger: [], services: [] }
    })
    const flush = () => vi.advanceTimersByTimeAsync(10)
    const m = await startCheckout(() => {}, flush)
    expect(m.text()).toContain('Pay online')
    await m.click('Telegram Stars')
    await flush()
    expect(calls.find((c) => c.path === '/orders/o1/pay')).toBeTruthy()
    expect(JSON.parse(String(calls.find((c) => c.path === '/orders/o1/pay')?.init.body))).toEqual({ method: 'stars' })
    expect(open).toHaveBeenCalledTimes(1)
    expect(open.mock.calls[0][0]).toBe('https://t.me/$fakeInvoice1')
    expect(m.text()).toContain('100 Stars (150,000 Toman)')

    // Telegram: paid. The bot settles it a moment later.
    ended('paid')
    await flush()
    expect(m.text()).toContain('Payment sent; confirming it')
    expect(calls.some((c) => c.path === '/payments/i1/check')).toBe(true) // asked at once, quietly
    expect(m.text()).not.toContain('No payment seen yet') // quietly: nothing said while it is not there yet
    orderStatus = 'active'
    await vi.advanceTimersByTimeAsync(3000) // the screen's own look at the order
    await flush()
    expect(m.text()).toContain('Your service is ready.')
    m.unmount()
  })

  it('stays on the methods when the payment by this method is over', async () => {
    signedIn(({ method, path }) => {
      if (path === '/orders' && method === 'POST') return order()
      if (path.startsWith('/methods')) return { methods: ['stars'] }
      if (path === '/orders/o1/pay') return { ...starsIntent(), status: 'expired' }
      return {}
    })
    const flush = () => settle(10)
    const m = await startCheckout(() => {}, flush)
    await m.click('Telegram Stars')
    await flush()
    expect(m.text()).toContain('The payment did not go through.')
    expect(m.text()).toContain('Pay online') // the methods are still there to pick another
    expect(m.text()).not.toContain('Pay with Telegram Stars')
    m.unmount()
  })
})

describe('buying by card to card', () => {
  it('shows the card, takes the receipt, and leaves to the services', async () => {
    const copied = clipboard()
    const calls = signedIn(({ method, path }) => {
      if (path === '/orders' && method === 'POST') return order()
      if (path.startsWith('/methods')) return { methods: ['manual_card'] }
      if (path === '/orders/o1/pay') return intent()
      if (path === '/payments/i1/proof') return { intent_id: 'i1', status: 'confirming' }
      return {}
    })
    const flush = () => settle(30)
    const m = await startCheckout(() => {}, flush)
    await m.click('Card to card')
    await flush()
    expect(m.text()).toContain('6037-9911-2222-3333')
    expect(m.text()).toContain('150,000 Toman')
    m.buttons().filter((b) => b.textContent?.trim() === 'Copy')[1].click()
    await settle(5)
    expect(copied).toEqual(['6037991122223333'])

    await chooseFile(m.el, new File([pngBytes()], 'receipt.png', { type: 'image/png' }))
    const ref = m.el.querySelector('input[type=text]') as HTMLInputElement
    ref.value = '998877'
    ref.dispatchEvent(new Event('input'))
    await settle(5)
    await m.click('Send the receipt')
    await flush()
    expect(calls.find((c) => c.path === '/payments/i1/proof')).toBeTruthy()
    expect(m.text()).toContain('Receipt received')
    // "Check status" reads the order and says it is still waiting
    await m.click('Check status')
    await flush()
    expect(m.text()).toContain('Still waiting for review.')
    await m.click('See my services')
    expect(m.events.services).toHaveLength(1)
    m.unmount()
  })
})

// Adding funds to the wallet.
describe('topping up the wallet', () => {
  it('by card: the same screen, and the way out is the screen\'s own Back', async () => {
    const calls = signedIn(({ method, path }) => {
      if (path === '/wallet') return { wallets: [{ currency: 'IRT', balance: 0, scale: 0 }], ledger: [] }
      if (path.startsWith('/methods')) return { methods: ['stars', 'manual_card'] }
      if (path === '/topup' && method === 'POST') return intent({ order_id: '', amount: 100000 })
      if (path === '/payments/i1/proof') return { intent_id: 'i1', status: 'confirming' }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    await m.click('100,000 Toman')
    await settle(20)
    expect(m.text()).toContain('Telegram Stars')
    await m.click('Card to card')
    await settle(20)
    const topup = calls.find((c) => c.path === '/topup')
    expect(topup).toBeTruthy()
    expect(JSON.parse(String(topup?.init.body))).toMatchObject({ amount: 100000, method: 'manual_card' })
    expect(m.text()).toContain('6037-9911-2222-3333')

    await chooseFile(m.el, new File([pngBytes()], 'receipt.png', { type: 'image/png' }))
    const ref = m.el.querySelector('input[type=text]') as HTMLInputElement
    ref.value = '445566'
    ref.dispatchEvent(new Event('input'))
    await settle(5)
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Receipt received')
    expect(m.text()).not.toContain('See my services') // a top-up has no services to go to
    expect(m.buttons().filter((b) => b.textContent?.trim() === 'Back')).toHaveLength(1)
    await m.click('Back')
    expect(m.text()).toContain('Add funds')
    m.unmount()
  })

  it('by Stars: opens the invoice and the wallet fills when the payment is seen', async () => {
    let ended: (s: string) => void = () => {}
    const open = vi.fn((_url: string, cb: (s: string) => void) => (ended = cb))
    telegram({ openInvoice: open, isVersionAtLeast: () => true })
    let paid = false
    signedIn(({ method, path }) => {
      if (path === '/wallet') return { wallets: [{ currency: 'IRT', balance: paid ? 100000 : 0, scale: 0 }], ledger: [] }
      if (path.startsWith('/methods')) return { methods: ['stars'] }
      if (path === '/topup' && method === 'POST') return { ...starsIntent(), order_id: '', amount: 100000, stars: 67, gateway_amount: 67 }
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: paid ? 'succeeded' : 'pending', order_id: '', failure_reason: '' }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    await m.click('100,000 Toman')
    await settle(20)
    await m.click('Telegram Stars')
    await settle(20)
    expect(open).toHaveBeenCalledTimes(1)
    expect(m.text()).toContain('67 Stars (100,000 Toman)')
    paid = true
    ended('paid')
    await settle(30)
    expect(m.text()).toContain('The wallet balance went up.')
    expect(state.wallets[0].balance).toBe(100000)
    m.unmount()
  })

  it('does not start a payment by a method that already failed', async () => {
    signedIn(({ method, path }) => {
      if (path === '/wallet') return { wallets: [{ currency: 'IRT', balance: 0, scale: 0 }], ledger: [] }
      if (path.startsWith('/methods')) return { methods: ['stars'] }
      if (path === '/topup' && method === 'POST') return json(200, { ...starsIntent(), status: 'failed' })
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    await m.click('100,000 Toman')
    await settle(20)
    await m.click('Telegram Stars')
    await settle(20)
    expect(m.text()).toContain('The payment did not go through.')
    expect(m.text()).not.toContain('Pay with Telegram Stars')
    m.unmount()
  })
})
