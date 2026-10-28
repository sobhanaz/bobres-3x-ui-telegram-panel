import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CashbackInfo, Me, Plan } from '../api'
import { ledgerKey } from '../fmt'
import { messages, setLang, translate } from '../i18n'
import { state } from '../store'
import CheckoutView from '../views/CheckoutView.vue'
import WalletView from '../views/WalletView.vue'
import { mount, settle, signedIn } from './helpers'

const plan: Plan = { id: 'p1', names: { en: 'Gold', fa: 'طلایی' }, kind: 'both', days: 30, traffic_bytes: 50 * 2 ** 30, price: 10000, list_price: 10000, currency: 'IRT', scale: 0, topup: false }
const order = (over: Record<string, unknown> = {}) => ({ id: 'o1', plan_id: 'p1', type: 'new', status: 'awaiting_payment', amount: 10000, discount_amount: 0, currency: 'IRT', scale: 0, subscription_id: '', ...over })
const cashback = (over: Partial<CashbackInfo> = {}): CashbackInfo => ({ on: true, percent: '2.5', max_per_order: 0, min_order: 0, renewals: true, earned: 0, currency: 'IRT', scale: 0, ...over })

beforeEach(() => {
  setLang('en')
  state.gate = ''
  state.me = { top_up: { currency: 'IRT', min: 10000, max: 50000000 }, brand: { bot_url: '' }, features: {} } as unknown as Me
  state.wallets = [{ currency: 'IRT', balance: 100000, scale: 0 }]
  state.ledger = []
  state.cashback = null
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('the cashback in the wallet', () => {
  it('says what a purchase earns while it is on, and what the customer earned so far', async () => {
    signedIn(({ path }) => (path === '/wallet' ? { wallets: state.wallets, ledger: [], cashback: cashback({ earned: 1250, min_order: 5000, max_per_order: 20000, renewals: false }) } : { payments: [] }))
    const m = mount(WalletView)
    await settle(20)
    expect(m.text()).toContain('Every purchase gives 2.5% of its price back to your wallet as cashback.')
    expect(m.text()).toContain('For orders of at least 5,000 Toman.')
    expect(m.text()).toContain('The most one order earns: 20,000 Toman.')
    expect(m.text()).toContain('New services only; renewals do not earn it.')
    expect(m.text()).toContain('Cashback earned so far: 1,250 Toman')
    m.unmount()
  })

  it('says it in Persian, with Persian digits and the Persian percent sign', async () => {
    setLang('fa')
    signedIn(({ path }) => (path === '/wallet' ? { wallets: state.wallets, ledger: [], cashback: cashback({ earned: 1250 }) } : { payments: [] }))
    const m = mount(WalletView)
    await settle(20)
    expect(m.text()).toContain('با هر خرید، ۲٫۵٪ از مبلغ آن به‌صورت کش‌بک به کیف پولتان برمی‌گردد.')
    expect(m.text()).toContain('کش‌بک دریافتی تا امروز: ۱٬۲۵۰ تومان')
    expect(m.text()).not.toContain('فقط برای سرویس جدید') // renewals earn it
    m.unmount()
  })

  it('keeps what was earned in view after the owner switches it off, and shows nothing when there is nothing to say', async () => {
    signedIn(({ path }) => (path === '/wallet' ? { wallets: state.wallets, ledger: [], cashback: cashback({ on: false, percent: '', earned: 300 }) } : { payments: [] }))
    let m = mount(WalletView)
    await settle(20)
    expect(m.text()).not.toContain('Every purchase gives')
    expect(m.text()).toContain('Cashback earned so far: 300 Toman')
    m.unmount()

    vi.restoreAllMocks()
    signedIn(({ path }) => (path === '/wallet' ? { wallets: state.wallets, ledger: [] } : { payments: [] }))
    m = mount(WalletView)
    await settle(20)
    expect(m.text()).not.toContain('ashback')
    m.unmount()
  })

  it('names the movements it writes, in both languages', () => {
    for (const [kind, en, fa] of [
      ['cashback', 'Cashback', 'کش‌بک'],
      ['cashback_reversal', 'Cashback taken back', 'کسر کش‌بک (برگشت سفارش)'],
      ['target_bonus', 'Monthly target bonus', 'پاداش هدف ماهانه'],
      ['wheel_prize', 'Wheel of fortune prize', 'جایزهٔ گردونهٔ شانس'],
    ] as const) {
      const key = ledgerKey(kind)
      expect(key).toBe(`ledger.${kind}`)
      expect(translate('en', key)).toBe(en)
      expect(translate('fa', key)).toBe(fa)
    }
    expect(ledgerKey('referral_reward')).toBe('ledger.other')
    expect(Object.keys(messages.fa).filter((k) => k.startsWith('cashback.') || k === 'done.cashback').sort()).toEqual(
      Object.keys(messages.en).filter((k) => k.startsWith('cashback.') || k === 'done.cashback').sort(),
    )
  })
})

describe('the result of a purchase', () => {
  async function buy(delivered: Record<string, unknown>) {
    vi.useFakeTimers()
    const calls = signedIn(({ method, path }) => {
      if (path === '/orders' && method === 'POST') return order()
      if (path.startsWith('/methods')) return { methods: ['manual_card'] }
      if (path === '/orders/o1/pay-wallet') return order({ status: 'paid' })
      if (path === '/orders/o1') return order({ status: 'active', ...delivered })
      return { wallets: [], ledger: [], services: [] }
    })
    const flush = () => vi.advanceTimersByTimeAsync(10)
    const m = mount(CheckoutView, { plan, type: 'new' }, ['close', 'services'])
    await flush()
    await m.click('Continue')
    await flush()
    await m.click('Pay from the wallet')
    await flush()
    await vi.advanceTimersByTimeAsync(3100) // the order is asked about every 3 seconds
    await flush()
    expect(calls.some((c) => c.path === '/orders/o1/pay-wallet')).toBe(true)
    return m
  }

  it('tells the customer what the delivered order earned', async () => {
    const m = await buy({ cashback: { amount: 250, currency: 'IRT', scale: 0 } })
    expect(m.text()).toContain('Your service is ready.')
    expect(m.text()).toContain('You earned 250 Toman cashback. It was added to your wallet.')
    m.unmount()
  })

  it('says nothing about cashback when the order earned none', async () => {
    const m = await buy({})
    expect(m.text()).toContain('Your service is ready.')
    expect(m.text()).not.toContain('cashback')
    m.unmount()
  })
})
