import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, type Me, type Plan, type WalletQuote } from '../api'
import { errorKey, staleQuote } from '../errors'
import { messages, setLang } from '../i18n'
import { state } from '../store'
import CheckoutView from '../views/CheckoutView.vue'
import { json, mount, noTelegram, signedIn, telegram, type Call, type Mounted } from './helpers'

// Paying a plan priced in USDT from the wallet, which is in Toman: the customer is quoted the price in Toman at today's rate,
// sees it count down, and confirms. Core charges exactly the quoted price while the quote holds (two minutes); a price that
// ran out is asked for again and shown again, never paid.

const NOW = Date.UTC(2026, 9, 28, 12, 0, 0) // the phone's clock in these tests, to the second
const ADDRESS = 'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r'
const QR = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAhJ0pbAAAAABJRU5ErkJggg=='
const usdtPlan: Plan = { id: 'p2', names: { en: 'Gold', fa: 'طلایی' }, kind: 'both', days: 30, traffic_bytes: 50 * 2 ** 30, price: 4_000_000, list_price: 4_000_000, currency: 'USDT', scale: 6, topup: false }
const tomanPlan: Plan = { ...usdtPlan, id: 'p1', price: 150000, list_price: 150000, currency: 'IRT', scale: 0 }
const order = (over: Record<string, unknown> = {}) => ({ id: 'o1', plan_id: 'p2', type: 'new', status: 'pending_payment', amount: 4_000_000, discount_amount: 0, currency: 'USDT', scale: 6, subscription_id: '', ...over })
// the order once the wallet paid it: amount and currency are what was charged, the list_ fields what it was listed at
const paid = (over: Record<string, unknown> = {}) =>
  order({ status: 'paid', amount: 1_176_000, currency: 'IRT', scale: 0, list_amount: 4_000_000, list_currency: 'USDT', list_scale: 6, rate: 294_000, discount_currency: 'USDT', ...over })
const quote = (over: Partial<WalletQuote> = {}): WalletQuote => ({
  order_id: 'o1', list_amount: 4_000_000, list_currency: 'USDT', list_scale: 6, rate: 294_000, amount: 1_176_000, currency: 'IRT', scale: 0,
  quote: 'tok-1', expires_at: 1_730_000_120, expires_in: 120, balance: 2_000_000, credit_limit: 0, enough: true, ...over,
})

interface Script {
  /** the answer to the n-th GET wallet-quote (1 is the first) */
  quote?: (n: number) => unknown
  /** the answer to the n-th POST pay-wallet, with its body */
  pay?: (body: { quote?: string }, n: number) => unknown
  methods?: string[]
}

// A store with one unpaid order, priced as the plan is; each quote it makes has a token of its own (tok-1, tok-2...).
function shop(script: Script = {}, plan: Plan = usdtPlan): Call[] {
  let quotes = 0
  let pays = 0
  let delivered = false
  return signedIn(async ({ method, path, init }) => {
    if (path === '/orders' && method === 'POST') return plan.currency === 'USDT' ? order() : order({ plan_id: 'p1', amount: 150000, currency: 'IRT', scale: 0 })
    if (path.startsWith('/methods')) return { methods: script.methods ?? ['zarinpal', 'usdt_trc20_auto'] }
    if (path === '/orders/o1/wallet-quote') return script.quote ? script.quote(++quotes) : quote({ quote: `tok-${++quotes}` })
    if (path === '/orders/o1/pay-wallet') {
      const answer = script.pay ? await script.pay(JSON.parse(String(init.body)), ++pays) : paid()
      if (!(answer instanceof Response)) delivered = true // the wallet paid: the order is followed to its end
      return answer
    }
    if (path === '/orders/o1/pay') {
      const provider = JSON.parse(String(init.body)).method
      return {
        intent_id: 'i1', status: 'pending', provider, order_id: 'o1', amount: 4_000_000, currency: 'USDT', scale: 6,
        pay_url: provider === 'zarinpal' ? 'https://pay.example/x' : '', gateway_amount: 4_000_417, gateway_currency: 'USDT', scale_gateway: 6,
        details: provider === 'zarinpal' ? {} : { pay_address: ADDRESS, network: 'TRC20', expires_at: String(NOW / 1000 + 600) },
        ...(provider === 'zarinpal' ? {} : { qr: QR }),
      }
    }
    if (path === '/orders/o1') return delivered ? paid({ status: 'active' }) : plan.currency === 'USDT' ? order() : order({ plan_id: 'p1', amount: 150000, currency: 'IRT', scale: 0 })
    return { wallets: [], ledger: [], services: [] }
  })
}

const flush = () => vi.advanceTimersByTimeAsync(10)
const hits = (calls: Call[], method: string, path: string) => calls.filter((c) => c.method === method && c.path === path)
const bodyOf = (c: Call | undefined) => JSON.parse(String(c?.init.body))
// the lines of the quote card as the customer reads them: what it is and its value
const rows = (m: Mounted) => Array.from(m.el.querySelectorAll('.quote .total')).map((p) => Array.from(p.children).map((c) => c.textContent?.trim()))

async function toPay(plan: Plan = usdtPlan): Promise<Mounted> {
  const m = mount(CheckoutView, { plan, type: 'new' }, ['close', 'services'])
  await flush()
  await m.click('Continue')
  await flush()
  return m
}
async function toQuote(): Promise<Mounted> {
  const m = await toPay()
  await m.click('Pay from the wallet')
  await flush()
  return m
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  setLang('en')
  state.gate = ''
  state.me = { top_up: { currency: 'IRT', min: 10000, max: 50000000 }, brand: { bot_url: '' }, features: {} } as unknown as Me
  state.wallets = [{ currency: 'IRT', balance: 2_000_000, scale: 0 }]
  state.ledger = []
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
})

describe('the wallet button of a plan priced in USDT', () => {
  it('is on, shows the Toman balance, and asks for a quote instead of paying', async () => {
    state.wallets = [{ currency: 'IRT', balance: 0, scale: 0 }] // even an empty wallet may look at the price
    const calls = shop()
    const m = await toPay()
    expect(m.text()).toContain('Pay from the wallet (0 Toman)')
    expect(m.text()).not.toContain('The wallet balance is too low.') // the quote says whether it is enough
    expect(m.button('Pay from the wallet').disabled).toBe(false)
    await m.click('Pay from the wallet')
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')[0].init.body).toBeUndefined()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(0) // looking at a price pays nothing
    m.unmount()
  })

  it('asks for one quote for a double tap', async () => {
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    const calls = shop({ quote: async () => (await held, quote()) })
    const m = await toPay()
    const button = m.button('Pay from the wallet')
    button.click()
    button.click()
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1)
    expect(m.button('Pay from the wallet').disabled).toBe(true)
    release()
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1)
    expect(m.text()).toContain('This price is held for 02:00')
    m.unmount()
  })

  it('shows the USDT price, the rate, the Toman it takes, the balance and the time it is held', async () => {
    shop()
    const m = await toQuote()
    const t = m.text()
    expect(t).toContain('Pay from the wallet')
    expect(rows(m)).toEqual([
      ['Plan price', '4 USDT'],
      ['USDT rate', '1 USDT = 294,000 Toman'],
      ['Taken from your wallet', '1,176,000 Toman'],
      ['Wallet balance', '2,000,000 Toman'],
    ])
    expect(t).toContain('This price is held for 02:00')
    expect(m.buttons().map((b) => b.textContent?.trim())).toEqual(['Confirm and pay 1,176,000 Toman', 'Back', 'Back to the store'])
    expect(m.button('Confirm and pay').disabled).toBe(false)
    expect(t).not.toContain('The price was updated') // the first price is not an update
    expect(t).not.toContain('Pay online') // the ways to pay are on the step before
    m.unmount()
  })

  it('leaves the ways to pay online as they were, below the wallet button', async () => {
    shop()
    const m = await toPay()
    expect(m.text()).toContain('Pay online')
    expect(m.buttons().map((b) => b.textContent?.trim())).toEqual(['Pay from the wallet (2,000,000 Toman)', 'Zarinpal', 'USDT (TRC20)', 'Back to the store'])
    m.unmount()
  })
})

describe('the time a price is held', () => {
  it('counts down by a timer of its own, whatever the phone says the time is', async () => {
    shop()
    const m = await toQuote()
    expect(m.text()).toContain('This price is held for 02:00')
    await vi.advanceTimersByTimeAsync(1000)
    expect(m.text()).toContain('This price is held for 01:59')
    await vi.advanceTimersByTimeAsync(59_000)
    expect(m.text()).toContain('This price is held for 01:00')
    // The phone's date jumps a day: the quote's own expires_at (long ago, by that clock) means nothing, the countdown goes on.
    vi.setSystemTime(Date.now() + 86_400_000)
    await vi.advanceTimersByTimeAsync(1000)
    expect(m.text()).toContain('This price is held for 00:59')
    expect(m.text()).not.toContain('The price was updated')
    m.unmount()
  })

  it('asks for a new price by itself when it reaches zero, says so, and pays nothing until it is confirmed', async () => {
    const calls = shop()
    const m = await toQuote()
    await vi.advanceTimersByTimeAsync(119_000)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1)
    expect(m.text()).toContain('This price is held for 00:01')
    await vi.advanceTimersByTimeAsync(1_100)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
    expect(m.text()).toContain('The price was updated. Check it and confirm again.')
    expect(m.text()).toContain('This price is held for 02:00') // the new price holds for its own two minutes
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(0)
    expect(m.button('Confirm and pay').disabled).toBe(false)
    // the new tap pays the new price, by its own token
    await m.click('Confirm and pay')
    await flush()
    expect(bodyOf(hits(calls, 'POST', '/orders/o1/pay-wallet')[0])).toEqual({ quote: 'tok-2' })
    m.unmount()
  })

  it('shows the price that is being replaced as such, and cannot confirm it meanwhile', async () => {
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    shop({ quote: async (n) => (n === 1 ? quote() : (await held, quote({ quote: 'tok-2' }))) })
    const m = await toQuote()
    await vi.advanceTimersByTimeAsync(120_100)
    expect(m.text()).toContain('Updating the price…')
    expect(m.text()).not.toContain('This price is held for')
    expect(m.button('Confirm and pay').disabled).toBe(true)
    release()
    await flush()
    expect(m.text()).toContain('The price was updated')
    expect(m.text()).toContain('This price is held for 02:00')
    expect(m.button('Confirm and pay').disabled).toBe(false)
    m.unmount()
  })

  it('stops with the screen: going Back, or leaving the checkout, leaves no timer and no more asking', async () => {
    const calls = shop()
    const m = await toQuote()
    await m.click('Back')
    expect(m.text()).toContain('Pay online')
    expect(m.text()).not.toContain('This price is held for')
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1)
    await m.click('Pay from the wallet') // a new look at the price, from the start
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
    expect(m.text()).toContain('This price is held for 02:00')
    expect(m.text()).not.toContain('The price was updated')
    m.unmount()
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
  })
})

describe('confirming the price', () => {
  it('pays with the quote\'s token and goes to done, where the order is followed as always', async () => {
    telegram({ HapticFeedback: { notificationOccurred: vi.fn() } })
    const calls = shop()
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    const pay = hits(calls, 'POST', '/orders/o1/pay-wallet')
    expect(pay).toHaveLength(1)
    expect(bodyOf(pay[0])).toEqual({ quote: 'tok-1' })
    expect(m.text()).toContain('Done')
    expect(m.text()).toContain('Paid. Your service is being set up')
    expect(m.text()).not.toContain('Confirm and pay')
    expect(m.text()).not.toContain('Back to the store')
    expect(hits(calls, 'GET', '/wallet')).toHaveLength(1) // the balance is read again
    await vi.advanceTimersByTimeAsync(3000) // the screen's own look at the order
    expect(m.text()).toContain('Your service is ready.')
    // the countdown is over with the quote: it does not ask for a price again
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1)
    m.unmount()
  })

  it('sends one request for a double tap, and is off while it is on its way', async () => {
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    const calls = shop({ pay: async () => (await held, paid()) })
    const m = await toQuote()
    const confirm = m.button('Confirm and pay')
    confirm.click() // two taps before the screen has drawn the first
    confirm.click()
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(1)
    expect(m.button('Confirm and pay').disabled).toBe(true)
    expect(m.button('Back').disabled).toBe(true) // and the screen cannot be left under a payment that is going through
    m.button('Confirm and pay').click()
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(1)
    release()
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(1)
    expect(m.text()).toContain('Done')
    m.unmount()
  })

  it('does not pay a price that has run out, even if the button is pressed at that moment', async () => {
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    const calls = shop({ quote: async (n) => (n === 1 ? quote({ expires_in: 5 }) : (await held, quote({ quote: 'tok-2' }))) })
    const m = await toQuote()
    await vi.advanceTimersByTimeAsync(5_100) // zero: the new price is being asked for
    const confirm = m.button('Confirm and pay')
    expect(confirm.disabled).toBe(true)
    confirm.click()
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(0)
    release()
    await flush()
    m.unmount()
  })

  it('asks for a new price when the payment that was on its way fails after the time has run out', async () => {
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    const calls = shop({ quote: (n) => quote({ quote: `tok-${n}`, expires_in: 10 }), pay: async () => (await held, json(500, { error: 'weird', message: 'x' })) })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await vi.advanceTimersByTimeAsync(11_000) // the ten seconds run out while core has not answered
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1) // and nothing was asked for meanwhile: the answer comes first
    expect(m.button('Confirm and pay').disabled).toBe(true)
    release()
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
    expect(m.text()).toContain('The price was updated. Check it and confirm again.')
    expect(m.text()).toContain('Something went wrong. Try again.') // and the failure is not hidden
    expect(m.button('Confirm and pay').disabled).toBe(false)
    m.unmount()
  })

  it('goes on to done when the payment that was on its way succeeds after the time has run out', async () => {
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    const calls = shop({ quote: () => quote({ expires_in: 10 }), pay: async () => (await held, paid()) })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await vi.advanceTimersByTimeAsync(11_000)
    release()
    await flush()
    expect(m.text()).toContain('Done')
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1) // core had taken it in time: paid, and no new price
    expect(m.text()).not.toContain('The price was updated')
    m.unmount()
  })

  it('keeps a price that is still held when the payment could not be made, so the same tap can be repeated', async () => {
    const calls = shop({ pay: (_body, n) => (n === 1 ? json(429, { error: 'rate_limited', message: 'x' }) : paid()) })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    expect(m.text()).toContain('Too many requests. Wait a minute.')
    expect(m.text()).toContain('This price is held for')
    expect(m.text()).not.toContain('The price was updated')
    expect(m.button('Confirm and pay').disabled).toBe(false)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(1) // no new price for a failure that was not about the price
    await m.click('Confirm and pay')
    await flush()
    const pay = hits(calls, 'POST', '/orders/o1/pay-wallet')
    expect(pay.map(bodyOf)).toEqual([{ quote: 'tok-1' }, { quote: 'tok-1' }]) // the same price, the same token: charged once by core
    expect(m.text()).toContain('Done')
    m.unmount()
  })
})

describe('a price core no longer holds', () => {
  it.each(['quote_expired', 'price_changed'])('(%s) is not paid: the new price is asked for, shown with its notice, and waits for a tap', async (code) => {
    const calls = shop({ pay: (_body, n) => (n === 1 ? json(409, { error: code, message: 'the price quote ran out' }) : paid()) })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(1) // refused, once
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2) // and the new price asked for
    expect(m.text()).toContain('The price was updated. Check it and confirm again.')
    expect(m.el.querySelector('[role=alert]')).toBeNull() // a new price is news, not an error
    expect(m.text()).toContain('This price is held for 02:00')
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(1) // it did not pay again by itself
    await vi.advanceTimersByTimeAsync(2000)
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(1)
    // the new tap pays the new price
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet').map(bodyOf)).toEqual([{ quote: 'tok-1' }, { quote: 'tok-2' }])
    expect(m.text()).toContain('Done')
    m.unmount()
  })

  it('shows the new price when it differs, and the notice goes with the next tap', async () => {
    const calls = shop({
      quote: (n) => (n === 1 ? quote() : quote({ quote: 'tok-2', rate: 300_000, amount: 1_200_000 })),
      pay: (_body, n) => (n === 1 ? json(409, { error: 'quote_expired', message: 'x' }) : json(429, { error: 'rate_limited', message: 'x' })),
    })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    expect(rows(m).slice(1, 3)).toEqual([['USDT rate', '1 USDT = 300,000 Toman'], ['Taken from your wallet', '1,200,000 Toman']])
    expect(m.button('Confirm and pay').textContent).toContain('1,200,000 Toman')
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(2)
    expect(m.text()).not.toContain('The price was updated') // the customer has seen it and acted on it
    m.unmount()
  })

  it('tells the customer, with a Retry, when the new price cannot be asked for', async () => {
    const calls = shop({
      quote: (n) => (n === 2 ? json(429, { error: 'rate_limited', message: 'x' }) : quote({ quote: `tok-${n}` })),
      pay: (_body, n) => (n === 1 ? json(409, { error: 'quote_expired', message: 'x' }) : paid()),
    })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    // asked once, not again by itself
    await vi.advanceTimersByTimeAsync(60_000)
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
    expect(m.el.querySelector('[role=alert]')?.textContent).toContain('Too many requests. Wait a minute.')
    expect(m.text()).not.toContain('Confirm and pay') // the price on screen is old: it cannot be confirmed
    expect(m.text()).not.toContain('This price is held for')
    expect(m.text()).not.toContain('1,176,000')
    expect(m.buttons().map((b) => b.textContent?.trim())).toEqual(['Try again', 'Back', 'Back to the store'])
    await m.click('Try again')
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(3)
    expect(m.el.querySelector('[role=alert]')).toBeNull()
    expect(m.text()).toContain('The price was updated. Check it and confirm again.') // it replaces the one that ran out
    expect(m.text()).toContain('This price is held for 02:00')
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet').map(bodyOf)).toEqual([{ quote: 'tok-1' }, { quote: 'tok-3' }])
    m.unmount()
  })

  it('lets a failed ask go back to the ways to pay', async () => {
    shop({ quote: (n) => (n === 1 ? quote({ expires_in: 3 }) : json(500, { error: 'weird', message: 'x' })) })
    const m = await toQuote()
    await vi.advanceTimersByTimeAsync(3_100)
    expect(m.text()).toContain('Something went wrong. Try again.')
    await m.click('Back')
    expect(m.text()).toContain('Pay online')
    expect(m.text()).not.toContain('Something went wrong')
    m.unmount()
  })
})

describe('a store with no USDT rate', () => {
  const noRate = () => json(409, { error: 'no_rate', message: 'the USDT rate is not known right now' })
  const SENTENCE = 'The USDT price is not available right now, so this plan cannot be paid from the wallet. Try later or pay another way.'

  it('says so at the wallet button, keeps the online methods, and pays online as ever', async () => {
    const calls = shop({ quote: noRate })
    const m = await toPay()
    await m.click('Pay from the wallet')
    await flush()
    expect(m.text()).toContain(SENTENCE)
    expect(m.el.querySelector('[role=alert]')?.textContent).toContain(SENTENCE)
    expect(m.text()).toContain('Pay online')
    expect(m.text()).not.toContain('Confirm and pay')
    expect(m.text()).not.toContain('This price is held for')
    expect(vi.getTimerCount()).toBe(0)
    // the online methods work; the direct crypto one is among them
    expect(m.buttons().map((b) => b.textContent?.trim())).toEqual(['Pay from the wallet (2,000,000 Toman)', 'Zarinpal', 'USDT (TRC20)', 'Back to the store'])
    expect(m.button('Zarinpal').disabled).toBe(false)
    const payCalls = (c: Call) => c.path === '/orders/o1/pay' && c.method === 'POST'
    await m.click('USDT (TRC20)')
    await flush()
    expect(bodyOf(calls.find(payCalls))).toEqual({ method: 'usdt_trc20_auto' })
    // the wallet button may be tried again (the rate may be back): the sentence goes away while it asks
    m.unmount()
  })

  it('goes back to the ways to pay when the rate is lost while a price is on screen', async () => {
    shop({ quote: (n) => (n === 1 ? quote() : noRate()) })
    const m = await toQuote()
    expect(m.text()).toContain('This price is held for')
    await vi.advanceTimersByTimeAsync(120_100) // the time ran out, and the store has no rate for a new price
    expect(m.text()).toContain(SENTENCE)
    expect(m.text()).toContain('Pay online')
    expect(m.text()).not.toContain('Confirm and pay')
    expect(m.text()).not.toContain('The price was updated')
    expect(vi.getTimerCount()).toBe(0)
    m.unmount()
  })

  it('goes back to the ways to pay when a payment finds the rate gone', async () => {
    const calls = shop({
      quote: (n) => (n === 1 ? quote() : noRate()),
      pay: () => json(409, { error: 'quote_expired', message: 'x' }),
    })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
    expect(m.text()).toContain(SENTENCE)
    expect(m.text()).toContain('Pay online')
    m.unmount()
  })

  it('forgets the sentence when the wallet button finds a rate again', async () => {
    shop({ quote: (n) => (n === 1 ? noRate() : quote({ quote: 'tok-2' })) })
    const m = await toPay()
    await m.click('Pay from the wallet')
    await flush()
    expect(m.text()).toContain(SENTENCE)
    await m.click('Pay from the wallet')
    await flush()
    expect(m.text()).not.toContain(SENTENCE)
    expect(m.text()).toContain('This price is held for 02:00')
    m.unmount()
  })
})

describe('a wallet that does not cover the price', () => {
  it('says how much is missing and cannot confirm', async () => {
    const calls = shop({ quote: () => quote({ balance: 900_000, enough: false }) })
    const m = await toQuote()
    expect(m.text()).toContain('The wallet balance is too low. You need 276,000 Toman more for this payment.')
    expect(m.el.querySelector('[role=alert]')?.textContent).toContain('too low')
    expect(rows(m)[3]).toEqual(['Wallet balance', '900,000 Toman'])
    const confirm = m.button('Confirm and pay')
    expect(confirm.disabled).toBe(true)
    confirm.click()
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet')).toHaveLength(0)
    m.unmount()
  })

  it('counts a reseller\'s credit as money to spend', async () => {
    const m = (shop({ quote: () => quote({ balance: 900_000, credit_limit: 500_000, enough: true }) }), await toQuote())
    expect(m.text()).not.toContain('too low')
    expect(m.button('Confirm and pay').disabled).toBe(false)
    m.unmount()
  })

  it('asks for a new quote, without calling the price new, when core did not take the one it was sent', async () => {
    const calls = shop({ pay: (_body, n) => (n === 1 ? json(400, { error: 'invalid', message: 'the price quote is not valid' }) : paid()) })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
    expect(m.el.querySelector('[role=alert]')?.textContent).toBe('The input is not valid.')
    expect(m.text()).not.toContain('The price was updated') // the price is what it was; only the token is new
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'POST', '/orders/o1/pay-wallet').map(bodyOf)).toEqual([{ quote: 'tok-1' }, { quote: 'tok-2' }])
    expect(m.text()).toContain('Done')
    m.unmount()
  })

  it('looks at the wallet again when core says it was not enough after all', async () => {
    const calls = shop({
      quote: (n) => (n === 1 ? quote() : quote({ quote: 'tok-2', balance: 100_000, enough: false })),
      pay: () => json(409, { error: 'insufficient_funds', message: 'the wallet balance is too low' }),
    })
    const m = await toQuote()
    await m.click('Confirm and pay')
    await flush()
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(2)
    expect(m.el.querySelector('[role=alert]')?.textContent).toContain('The wallet balance is too low.')
    expect(rows(m)[3]).toEqual(['Wallet balance', '100,000 Toman'])
    expect(m.text()).toContain('You need 1,076,000 Toman more')
    expect(m.text()).not.toContain('The price was updated') // the price is the same; the wallet is what changed
    expect(m.button('Confirm and pay').disabled).toBe(true)
    m.unmount()
  })
})

describe('paying a plan priced in USDT straight to an address', () => {
  it('works as before, with the address, the QR code and the time left, and stops following the order when the time is over', async () => {
    const calls = shop()
    const m = await toPay()
    await m.click('USDT (TRC20)')
    await flush()
    expect(bodyOf(hits(calls, 'POST', '/orders/o1/pay')[0])).toEqual({ method: 'usdt_trc20_auto' })
    expect(m.text()).toContain('Send exactly 4.000417 USDT to this address:')
    expect(m.text()).toContain(ADDRESS)
    expect(m.text()).toContain('Time left 10:00')
    expect(m.el.querySelector('img')?.getAttribute('src')).toBe(QR)
    expect(m.buttons().map((b) => b.textContent?.trim())).toEqual(['Copy', 'I have paid', 'Back to the store'])
    await vi.advanceTimersByTimeAsync(9_000)
    expect(hits(calls, 'GET', '/orders/o1').length).toBeGreaterThan(1) // the order is followed every 3 seconds
    await vi.advanceTimersByTimeAsync(591_000)
    expect(m.text()).toContain('The time for this payment is over.')
    expect(m.text()).not.toContain(ADDRESS)
    const followed = hits(calls, 'GET', '/orders/o1').length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(hits(calls, 'GET', '/orders/o1')).toHaveLength(followed)
    m.unmount()
  })
})

describe('a plan priced in Toman', () => {
  it('pays from the wallet at once with an empty body, as before, and asks for no quote', async () => {
    const calls = shop({}, tomanPlan)
    state.wallets = [{ currency: 'IRT', balance: 200_000, scale: 0 }]
    const m = await toPay(tomanPlan)
    expect(m.text()).toContain('Pay from the wallet (200,000 Toman)')
    expect(m.button('Pay from the wallet').disabled).toBe(false)
    await m.click('Pay from the wallet')
    await flush()
    const pay = hits(calls, 'POST', '/orders/o1/pay-wallet')
    expect(pay).toHaveLength(1)
    expect(pay[0].init.body).toBe('{}')
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(0)
    expect(m.text()).toContain('Done')
    m.unmount()
  })

  it('keeps the button off, and says why, when the wallet is short', async () => {
    const calls = shop({}, tomanPlan)
    state.wallets = [{ currency: 'IRT', balance: 100_000, scale: 0 }]
    const m = await toPay(tomanPlan)
    expect(m.button('Pay from the wallet').disabled).toBe(true)
    expect(m.text()).toContain('The wallet balance is too low.')
    expect(m.text()).not.toContain('The USDT price is not available')
    expect(hits(calls, 'GET', '/orders/o1/wallet-quote')).toHaveLength(0)
    m.unmount()
  })
})

describe('in Persian', () => {
  it('writes the quote with Persian digits and the words Toman and Tether, the time left reading left to right', async () => {
    setLang('fa')
    shop()
    const m = mount(CheckoutView, { plan: usdtPlan, type: 'new' }, ['close', 'services'])
    await flush()
    await m.click('ادامه')
    await flush()
    await m.click('پرداخت از کیف پول')
    await flush()
    const t = m.text()
    expect(rows(m)).toEqual([
      ['قیمت پلن', '۴ تتر'],
      ['نرخ تتر', '۱ تتر = ۲۹۴٬۰۰۰ تومان'],
      ['مبلغ کسر از کیف پول', '۱٬۱۷۶٬۰۰۰ تومان'],
      ['موجودی کیف پول', '۲٬۰۰۰٬۰۰۰ تومان'],
    ])
    expect(t).toContain('مدت اعتبار این قیمت: ۰۲:۰۰')
    expect(m.el.querySelector('.clock')?.getAttribute('dir')).toBe('ltr')
    expect(m.el.querySelector('[role=timer]')).not.toBeNull()
    expect(m.button('تأیید و پرداخت').textContent).toContain('۱٬۱۷۶٬۰۰۰ تومان')
    await vi.advanceTimersByTimeAsync(120_100)
    expect(m.text()).toContain('قیمت به‌روز شد؛ آن را بررسی کنید و دوباره تأیید کنید.')
    expect(m.text()).toContain('۰۲:۰۰')
    m.unmount()
  })

  it('words a short wallet and a missing rate in Persian', async () => {
    setLang('fa')
    shop({ quote: () => quote({ balance: 900_000, enough: false }) })
    let m = mount(CheckoutView, { plan: usdtPlan, type: 'new' }, [])
    await flush()
    await m.click('ادامه')
    await flush()
    await m.click('پرداخت از کیف پول')
    await flush()
    expect(m.text()).toContain('موجودی کیف پول کافی نیست. برای این پرداخت ۲۷۶٬۰۰۰ تومان دیگر لازم است.')
    m.unmount()

    vi.restoreAllMocks()
    shop({ quote: () => json(409, { error: 'no_rate', message: 'x' }) })
    m = mount(CheckoutView, { plan: usdtPlan, type: 'new' }, [])
    await flush()
    await m.click('ادامه')
    await flush()
    await m.click('پرداخت از کیف پول')
    await flush()
    expect(m.text()).toContain('الان قیمت تتر در دسترس نیست و نمی‌شود این پلن را از کیف پول پرداخت کرد.')
    expect(m.text()).toContain('پرداخت آنلاین')
    m.unmount()
  })
})

describe('the calls', () => {
  it('asks for a quote with a plain GET', async () => {
    const calls = signedIn(() => quote())
    expect(await api.walletQuote('o1')).toEqual(quote())
    expect(calls).toHaveLength(1)
    expect(calls[0].method).toBe('GET')
    expect(calls[0].path).toBe('/orders/o1/wallet-quote')
    expect(calls[0].init.body).toBeUndefined()
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBe('Bearer tok1')
  })

  it('pays with the quote\'s token when there is one, and with an empty body when there is none', async () => {
    const calls = signedIn(() => paid())
    await api.payWallet('o1', 'tok-1')
    await api.payWallet('o2')
    await api.payWallet('o3', '')
    expect(calls.map((c) => [c.method, c.path, c.init.body])).toEqual([
      ['POST', '/orders/o1/pay-wallet', '{"quote":"tok-1"}'],
      ['POST', '/orders/o2/pay-wallet', '{}'],
      ['POST', '/orders/o3/pay-wallet', '{}'],
    ])
  })

  it('words the failures of the quote and of the payment in both languages', () => {
    expect(errorKey(new ApiError(409, 'quote_expired', ''))).toBe('err.quote_expired')
    expect(errorKey(new ApiError(409, 'price_changed', ''))).toBe('err.quote_expired')
    expect(errorKey(new ApiError(409, 'no_rate', ''))).toBe('err.no_rate')
    expect(errorKey(new ApiError(409, 'insufficient_funds', ''))).toBe('err.insufficient_funds')
    for (const key of ['err.quote_expired', 'err.no_rate']) {
      expect(messages.fa[key], `fa ${key}`).toBeTruthy()
      expect(messages.en[key], `en ${key}`).toBeTruthy()
    }
    expect(messages.en['err.no_rate']).toBe('The USDT price is not available right now, so this plan cannot be paid from the wallet. Try later or pay another way.')
  })

  it('knows a price that no longer holds from any other failure', () => {
    expect(staleQuote(new ApiError(409, 'quote_expired', ''))).toBe(true)
    expect(staleQuote(new ApiError(409, 'price_changed', ''))).toBe(true)
    for (const code of ['no_rate', 'insufficient_funds', 'not_payable', 'invalid', 'state', 'network']) expect(staleQuote(new ApiError(409, code, '')), code).toBe(false)
    expect(staleQuote(new Error('quote_expired'))).toBe(false)
  })

  it('has every text of the quote screen in both languages, with the same placeholders', () => {
    const keys = [
      'quote.title', 'quote.price', 'quote.rate', 'quote.rate_line', 'quote.charge', 'quote.balance', 'quote.held', 'quote.updating', 'quote.updated',
      'quote.missing', 'quote.confirm', 'err.quote_expired', 'err.no_rate',
    ]
    const names = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
    for (const key of keys) {
      expect(messages.fa[key], `fa ${key}`).toBeTruthy()
      expect(messages.en[key], `en ${key}`).toBeTruthy()
      expect(names(messages.fa[key]), key).toEqual(names(messages.en[key]))
    }
    expect(messages.fa['quote.rate_line']).toContain('تتر')
    expect(messages.fa['quote.confirm']).toContain('{amount}')
  })
})
