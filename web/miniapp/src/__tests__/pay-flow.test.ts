import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Intent, Me, Plan } from '../api'
import Methods from '../components/Methods.vue'
import PayPanel from '../components/PayPanel.vue'
import { setLang } from '../i18n'
import { state } from '../store'
import CheckoutView from '../views/CheckoutView.vue'
import WalletView from '../views/WalletView.vue'
import { chooseFile, clipboard, json, mount, noTelegram, pngBytes, settle, signedIn, telegram, type Call, type Mounted } from './helpers'

const ALL = ['zarinpal', 'idpay', 'nextpay', 'aqayepardakht', 'usdt_trc20_auto', 'ton_auto', 'stars', 'manual_card', 'manual_crypto', 'manual_zarinpal']

const plan: Plan = { id: 'p1', names: { en: 'Gold', fa: 'طلایی' }, kind: 'both', days: 30, traffic_bytes: 50 * 2 ** 30, price: 150000, list_price: 150000, currency: 'IRT', scale: 0, topup: false }
const order = (over: Record<string, unknown> = {}) => ({ id: 'o1', plan_id: 'p1', type: 'new', status: 'pending_payment', amount: 150000, discount_amount: 0, currency: 'IRT', scale: 0, subscription_id: '', ...over })
const intent = (over: Partial<Intent> = {}): Intent => ({
  intent_id: 'i1', status: 'pending', provider: 'manual_card', order_id: 'o1', amount: 150000, currency: 'IRT', scale: 0, pay_url: '',
  gateway_amount: 0, gateway_currency: '', scale_gateway: -1,
  details: { card_number: '6037-9911-2222-3333', card_holder: 'Ali Rezaei', reference: 'AB12CD34' }, ...over,
})
// The store's address (non-bounceable, as the server writes it) and a payment's memo.
const tonAddress = 'UQDtFpEwcFAEcRe5mLVh2N6C0x-_hJEM7W61_JLnSF74p9dz'
const tonIntent = (expiresAt: number, over: Partial<Intent> = {}): Intent =>
  intent({
    provider: 'ton_auto', gateway_amount: 2_531_000_000, gateway_currency: 'TON', scale_gateway: 9,
    details: { pay_address: tonAddress, network: 'TON', memo: 'BOB-3F9A1C20', expires_at: String(expiresAt), reference: 'AB12CD34' }, ...over,
  })
const nowSeconds = () => Math.floor(Date.now() / 1000)
// the isolates the screen puts around a left-to-right value
const plain = (s: string | null | undefined) => s?.replace(/[⁦-⁩]/g, '')
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
      'Zarinpal', 'IDPay', 'NextPay', 'Aqayepardakht', 'USDT (TRC20)', 'TON', 'Telegram Stars ⭐', 'Card to card', 'USDT, then send a receipt', 'Zarinpal payment link, then send a receipt',
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
    expect(t).toContain('آقای‌پرداخت')
    expect(t).toContain('تون (TON)')
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
    // an online gateway sends the customer to its page, whoever it is
    for (const provider of ['zarinpal', 'aqayepardakht']) {
      m = mount(PayPanel, { intent: intent({ provider, pay_url: 'https://pay.example/x', details: {} }), checking: false, message: '' }, ['check'])
      await settle(5)
      expect(m.text(), provider).toContain('Waiting for the payment')
      expect(m.text(), provider).toContain('Open the payment page')
      m.unmount()
    }
  })

  it('writes the amount of an automatic USDT payment whole, down to the last micro-USDT', async () => {
    // Every payment to the address has an amount of its own (that is how it is told from the others): "2.5" would be a
    // payment nobody asked for.
    const m = mount(
      PayPanel,
      {
        intent: intent({
          provider: 'usdt_trc20_auto', gateway_amount: 2_500_731, gateway_currency: 'USDT', scale_gateway: 6,
          details: { pay_address: 'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r', network: 'TRC20' },
        }),
        checking: false, message: '',
      },
      [],
    )
    await settle(5)
    expect(m.text()).toContain('Send exactly 2.500731 USDT to this address')
    m.unmount()
  })

  it('shows an automatic TON payment: the exact amount, the address and the memo, each to copy, and the warning', async () => {
    const copied = clipboard()
    const m = mount(PayPanel, { intent: tonIntent(nowSeconds() + 900), checking: false, message: '' }, ['check'])
    await settle(5)
    const text = m.text()
    // every decimal of a nanoton, zeros too, in Latin digits: never "2.531"
    expect(text).toContain('Exact amount')
    expect(text).toContain('2.531000000 TON')
    expect(text).not.toMatch(/2\.531 TON/)
    expect(text).toContain(tonAddress)
    expect(text).toContain('BOB-3F9A1C20')
    expect(text).toContain('Network TON')
    expect(text).toContain('Waiting for the payment')
    // the comment is what finds the payment: said so
    expect(text).toContain('You must write this comment in the transfer’s comment field')
    expect(text).toContain('can only be handled by hand')
    // the three values are in left-to-right monospace boxes, each with a Copy button of its own
    const boxes = [...m.el.querySelectorAll('.mono')] as HTMLElement[]
    expect(boxes.map((b) => b.getAttribute('dir'))).toEqual(['ltr', 'ltr', 'ltr'])
    expect(boxes.map((b) => plain(b.textContent))).toEqual(['2.531000000 TON', tonAddress, 'BOB-3F9A1C20'])
    const buttons = m.buttons().filter((b) => b.textContent?.trim() === 'Copy')
    expect(buttons).toHaveLength(3)
    for (const b of buttons) b.click()
    await settle(5)
    // the amount is copied as a plain number, the way a wallet takes it
    expect(copied).toEqual(['2.531000000', tonAddress, 'BOB-3F9A1C20'])
    expect(m.text().match(/Copied/g)).toHaveLength(3)
    // and the way to say it was paid
    await m.click('I have paid')
    expect(m.events.check).toEqual([[undefined]])
    m.unmount()
  })

  it('writes the TON screen in Persian, with the values still left to right', async () => {
    setLang('fa')
    const m = mount(PayPanel, { intent: tonIntent(nowSeconds() + 900), checking: false, message: '' }, [])
    await settle(5)
    const text = m.text()
    expect(text).toContain('مبلغ دقیق')
    expect(text).toContain('2.531000000 TON') // Latin digits and the code, whatever the language
    expect(text).toContain('آدرس مقصد')
    expect(text).toContain('کامنت (Memo)')
    expect(text).toContain('حتماً در بخش «کامنت» انتقال بنویسید')
    expect(text).toContain('شبکه TON')
    expect(text).toContain('زمان باقی‌مانده')
    expect(text).toContain('پرداخت کردم')
    expect([...m.el.querySelectorAll('.mono')].every((b) => b.getAttribute('dir') === 'ltr')).toBe(true)
    m.unmount()
  })

  it('counts down to the deadline by the second, and stops at zero with the late payment explained', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-28T12:00:00Z'))
    const m = mount(PayPanel, { intent: tonIntent(nowSeconds() + 75), checking: false, message: '' }, ['check'])
    await settle(5)
    const left = () => plain(m.el.querySelector('.ton-left')?.textContent)
    expect(left()).toBe('Time left: 01:15')
    expect(m.text()).not.toContain('The time to pay is over')
    await settle(1000)
    expect(left()).toBe('Time left: 01:14')
    await settle(14_000)
    expect(left()).toBe('Time left: 01:00')
    await settle(59_000)
    expect(left()).toBe('Time left: 00:01')
    expect(m.text()).not.toContain('The time to pay is over')
    // the last second: the time is over, and a late payment is still looked at
    await settle(1000)
    expect(left()).toBeUndefined()
    expect(m.text()).toContain('The time to pay is over. A payment made late is still looked at by the store.')
    expect(m.el.querySelector('.ton-over')?.getAttribute('role')).toBe('status')
    // nothing counts any more, and the amount, the address, the memo and the check stay
    expect(vi.getTimerCount()).toBe(0)
    await settle(60_000)
    expect(m.text()).toContain('The time to pay is over')
    expect(m.text()).toContain('2.531000000 TON')
    expect(m.text()).toContain('BOB-3F9A1C20')
    await m.click('I have paid')
    expect(m.events.check).toHaveLength(1)
    m.unmount()
  })

  it('does not start a clock for a payment whose time is already over, and stops it when the screen goes', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-28T12:00:00Z'))
    let m = mount(PayPanel, { intent: tonIntent(nowSeconds() - 30), checking: false, message: '' }, [])
    await settle(5)
    expect(m.text()).toContain('The time to pay is over')
    expect(m.el.querySelector('.ton-left')).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
    m.unmount()
    // one running clock, gone with the component
    m = mount(PayPanel, { intent: tonIntent(nowSeconds() + 600), checking: false, message: '' }, [])
    await settle(5)
    expect(vi.getTimerCount()).toBe(1)
    m.unmount()
    expect(vi.getTimerCount()).toBe(0)
    // no deadline told: no clock and no claim that time is over
    m = mount(PayPanel, { intent: tonIntent(0, { details: { pay_address: tonAddress, network: 'TON', memo: 'BOB-3F9A1C20' } }), checking: false, message: '' }, [])
    await settle(5)
    expect(m.text()).not.toContain('Time left')
    expect(m.text()).not.toContain('The time to pay is over')
    expect(vi.getTimerCount()).toBe(0)
    m.unmount()
  })

  it('shows only the way to check when a payment has no address yet, and no memo warning without a memo', async () => {
    let m = mount(PayPanel, { intent: tonIntent(0, { details: {} }), checking: false, message: '' }, ['check'])
    await settle(5)
    expect(m.text()).toContain('Waiting for the payment')
    expect(m.el.querySelector('.copy-field')).toBeNull()
    expect(m.text()).toContain('I have paid')
    m.unmount()
    m = mount(PayPanel, { intent: tonIntent(0, { details: { pay_address: tonAddress, network: 'TON' } }), checking: true, message: 'No payment seen yet.' }, [])
    await settle(5)
    expect(m.text()).not.toContain('You must write this comment')
    expect(m.el.querySelectorAll('.copy-field')).toHaveLength(2)
    expect(m.button('Checking').disabled).toBe(true)
    expect(m.text()).toContain('No payment seen yet.')
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

describe('buying with TON', () => {
  it('shows what to send, says a payment is not there yet in TON’s words, and finishes when the payment is seen', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-28T12:00:00Z'))
    let orderStatus = 'pending_payment'
    const expires = nowSeconds() + 900
    const calls = signedIn(({ method, path }) => {
      if (path === '/orders' && method === 'POST') return order()
      if (path.startsWith('/methods')) return { methods: ['ton_auto', 'manual_card'] }
      if (path === '/orders/o1/pay') return tonIntent(expires)
      if (path === '/orders/o1') return order({ status: orderStatus })
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: orderStatus === 'active' ? 'succeeded' : 'pending', order_id: 'o1', failure_reason: '' }
      return { wallets: [], ledger: [], services: [] }
    })
    const flush = () => vi.advanceTimersByTimeAsync(10)
    const m = await startCheckout(() => {}, flush)
    expect(m.buttons().map((b) => b.textContent?.trim())).toContain('TON')
    await m.click('TON')
    await flush()
    expect(JSON.parse(String(calls.find((c) => c.path === '/orders/o1/pay')?.init.body))).toEqual({ method: 'ton_auto' })
    expect(m.text()).toContain('2.531000000 TON')
    expect(m.text()).toContain(tonAddress)
    expect(m.text()).toContain('BOB-3F9A1C20')
    expect(m.text()).toContain('Time left: ')

    // asked too early: not there yet, and why that may be
    await m.click('I have paid')
    await flush()
    expect(calls.some((c) => c.path === '/payments/i1/check')).toBe(true)
    expect(m.text()).toContain('A TON transfer can take a minute or two to show up. If the comment was left out, the payment cannot be recognized.')

    // the payment is seen: the screen's own look at the order finds it
    orderStatus = 'active'
    await vi.advanceTimersByTimeAsync(3000)
    await flush()
    expect(m.text()).toContain('Your service is ready.')
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

// Leaving, failing, and how often a payment is asked about.
describe('leaving a payment screen and asking about a payment', () => {
  const waiting = (over: Partial<Intent> = {}): Intent => intent({ status: 'confirming', order_id: '', amount: 100000, ...over })
  const wallet = { wallets: [{ currency: 'IRT', balance: 0, scale: 0 }], ledger: [] }
  const checks = (calls: Call[]) => calls.filter((c) => c.path === '/payments/i1/check').length

  // A top-up of 100,000 by the method, as far as its screen.
  async function topUpBy(method: string): Promise<Mounted> {
    const m = mount(WalletView, {}, [])
    await settle(20)
    await m.click('100,000 Toman')
    await settle(20)
    await m.click(method)
    await settle(20)
    return m
  }

  it('does not trip when Back is pressed while a check is on its way', async () => {
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    signedIn(async ({ method, path }) => {
      if (path === '/wallet') return wallet
      if (path.startsWith('/methods')) return { methods: ['manual_card'] }
      if (path === '/topup' && method === 'POST') return waiting()
      if (path === '/payments/i1/check') {
        await held
        return { intent_id: 'i1', status: 'confirming', order_id: '', failure_reason: '' }
      }
      return {}
    })
    const m = await topUpBy('Card to card')
    expect(m.text()).toContain('Receipt received')
    m.button('Check status').click() // the answer is held
    await settle(5)
    await m.click('Back')
    release()
    await settle(30)
    expect(m.el.querySelector('[role=alert]')).toBeNull() // no "something went wrong" for a screen that is gone
    expect(m.text()).toContain('Add funds')
    expect(m.text()).not.toContain('Still waiting for review.') // said about a payment that is no longer on screen
    m.unmount()
  })

  it('asks about a manual payment every 30 seconds, learns it was turned down, and stops asking', async () => {
    vi.useFakeTimers()
    let status = 'confirming'
    const calls = signedIn(({ method, path }) => {
      if (path === '/wallet') return wallet
      if (path.startsWith('/methods')) return { methods: ['manual_card'] }
      if (path === '/topup' && method === 'POST') return waiting()
      if (path === '/payments/i1/check') return { intent_id: 'i1', status, order_id: '', failure_reason: '' }
      return {}
    })
    const m = await topUpBy('Card to card')
    expect(checks(calls)).toBe(0)
    await settle(29_000)
    expect(checks(calls)).toBe(0) // not every 5 seconds: it would use most of the customer's changes a minute
    await settle(1_500)
    expect(checks(calls)).toBe(1)
    expect(m.text()).not.toContain('Still waiting for review.') // asked by itself: nothing said while nothing is new
    status = 'rejected'
    await settle(30_000)
    expect(checks(calls)).toBe(2)
    expect(m.text()).toContain('The payment did not go through.') // the staff turned it down
    await settle(10 * 60_000)
    expect(checks(calls)).toBe(2) // and nothing more to wait for
    m.unmount()
  })

  it('asks about a manual payment for half an hour at most, and about an online one as long as the screen is open', async () => {
    vi.useFakeTimers()
    let calls = signedIn(({ method, path }) => {
      if (path === '/wallet') return wallet
      if (path.startsWith('/methods')) return { methods: ['manual_card', 'zarinpal'] }
      if (path === '/topup' && method === 'POST') return waiting()
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: 'confirming', order_id: '', failure_reason: '' }
      return {}
    })
    let m = await topUpBy('Card to card')
    await settle(40 * 60_000)
    expect(checks(calls)).toBe(60)
    m.unmount()

    calls = signedIn(({ method, path }) => {
      if (path === '/wallet') return wallet
      if (path.startsWith('/methods')) return { methods: ['zarinpal'] }
      if (path === '/topup' && method === 'POST') return intent({ provider: 'zarinpal', pay_url: 'https://pay.example/x', order_id: '', details: {} })
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: 'pending', order_id: '', failure_reason: '' }
      return {}
    })
    m = await topUpBy('Zarinpal')
    await settle(10 * 60_000)
    expect(checks(calls)).toBeGreaterThan(100) // every 5 seconds, as it was
    m.unmount()
  })

  it('starts no polling for a screen the customer has already left', async () => {
    vi.useFakeTimers()
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    const calls = signedIn(async ({ method, path }) => {
      if (path === '/orders' && method === 'POST') return order()
      if (path.startsWith('/methods')) return { methods: ['zarinpal'] }
      if (path === '/orders/o1/pay') {
        await held
        return intent({ provider: 'zarinpal', pay_url: 'https://pay.example/x', details: {} })
      }
      if (path === '/orders/o1') return order()
      return {}
    })
    const flush = () => vi.advanceTimersByTimeAsync(10)
    const m = await startCheckout(() => {}, flush)
    m.button('Zarinpal').click() // the payment is being made...
    await flush()
    m.unmount() // ...and the customer goes
    release()
    await vi.advanceTimersByTimeAsync(20_000)
    expect(calls.filter((c) => c.path === '/orders/o1' && c.method === 'GET')).toHaveLength(0)

    // The same for a top-up.
    let release2: () => void = () => {}
    const second = new Promise<void>((r) => (release2 = r))
    const calls2 = signedIn(async ({ method, path }) => {
      if (path === '/wallet') return wallet
      if (path.startsWith('/methods')) return { methods: ['zarinpal'] }
      if (path === '/topup' && method === 'POST') {
        await second
        return intent({ provider: 'zarinpal', pay_url: 'https://pay.example/x', order_id: '', details: {} })
      }
      return {}
    })
    const w = mount(WalletView, {}, [])
    await settle(20)
    await w.click('100,000 Toman')
    await settle(20)
    w.button('Zarinpal').click()
    await settle(20)
    w.unmount()
    release2()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(calls2.filter((c) => c.path === '/payments/i1/check')).toHaveLength(0)
  })

  it('tells a Stars payer who asks too early not to pay twice', async () => {
    telegram({ openInvoice: vi.fn(), isVersionAtLeast: () => true })
    signedIn(({ method, path }) => {
      if (path === '/orders' && method === 'POST') return order()
      if (path.startsWith('/methods')) return { methods: ['stars'] }
      if (path === '/orders/o1/pay') return starsIntent()
      if (path === '/orders/o1') return order()
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: 'pending', order_id: 'o1', failure_reason: '' }
      return {}
    })
    const flush = () => settle(30)
    const m = await startCheckout(() => {}, flush)
    await m.click('Telegram Stars')
    await flush()
    await m.click('I have paid')
    await flush()
    // not the bare "No payment seen yet.", which would send them back to the payment sheet
    expect(m.text()).toContain('wait a few seconds and check again; do not pay twice')
    m.unmount()
  })
})
