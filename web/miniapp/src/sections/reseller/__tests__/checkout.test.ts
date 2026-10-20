import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Plan } from '../../../api'
import { irtSpendable, state } from '../../../store'
import CheckoutView from '../../../views/CheckoutView.vue'
import { ME, fakeCore, find, mount, need, settle, startAs, tap, text, unmountAll } from './helpers'

// The shop's checkout, as a reseller buying for a customer uses it: the name
// goes with the order, no discount code (resellers pay tier prices), and the
// wallet pays with the credit as well as the balance.

const plan: Plan = {
  id: '00000000-0000-0000-0000-0000000000a1', names: { en: 'Monthly' }, kind: 'both', days: 30, traffic_bytes: 50 * 2 ** 30,
  price: 80_000, list_price: 100_000, currency: 'IRT', scale: 0, topup: false,
}

const order = (over: Record<string, unknown> = {}) => ({
  id: '00000000-0000-0000-0000-0000000000b1', plan_id: plan.id, type: 'new', status: 'awaiting_payment', amount: 80_000, discount_amount: 0,
  currency: 'IRT', scale: 0, subscription_id: '', ...over,
})

beforeEach(() => {
  startAs('en')
  state.me = { ...ME, features: { reseller: true } }
  state.wallets = [{ currency: 'IRT', balance: -50_000, scale: 0, credit_limit: 500_000 }]
})
afterEach(() => {
  unmountAll()
  vi.restoreAllMocks()
})

describe('the wallet of a reseller', () => {
  it('can pay with the balance and the credit', () => {
    expect(irtSpendable()).toBe(450_000)
    state.wallets = [{ currency: 'IRT', balance: 10_000, scale: 0 }] // a customer's wallet has no credit
    expect(irtSpendable()).toBe(10_000)
    state.wallets = []
    expect(irtSpendable()).toBe(0)
  })
})

describe('the checkout for a customer', () => {
  it('sends the name with the new order, never a discount code, and pays from the credit', async () => {
    const calls = fakeCore((c) => {
      if (c.path === '/orders') return order()
      if (c.path.endsWith('/pay-wallet')) return order({ status: 'paid' })
      if (c.path.startsWith('/methods')) return { methods: [] }
      if (c.path.startsWith('/orders/')) return order({ status: 'active' })
      return {}
    })
    const { el, unmount } = mount(CheckoutView, { plan, type: 'new', label: 'Ali’s phone' })
    await settle()
    expect(text(el)).toContain('🏷 Ali’s phone')
    expect(text(el)).toContain('80,000 Toman')
    expect(find(el, 'input')).toBeNull() // no discount code for a reseller
    expect(find(el, 'button', 'Apply')).toBeNull()

    await tap(el, 'Continue')
    const created = calls.find((c) => c.path === '/orders')
    expect(created?.method).toBe('POST')
    expect(created?.body).toMatchObject({ plan_id: plan.id, type: 'new', label: 'Ali’s phone' })
    expect(created?.body).not.toHaveProperty('discount_code')
    expect(created?.body).not.toHaveProperty('price')
    expect(created?.body).not.toHaveProperty('amount')
    expect(String((created?.body as { key: string }).key)).toMatch(/^[0-9a-f]{24}$/)

    // 450,000 can be spent (the balance is a debt of 50,000 and the credit 500,000): the wallet button is on
    const pay = need<HTMLButtonElement>(el, 'button', 'Pay from the wallet')
    expect(pay.disabled).toBe(false)
    expect(pay.textContent).toContain('450,000 Toman')
    pay.click()
    await settle()
    expect(calls.some((c) => c.path === '/orders/00000000-0000-0000-0000-0000000000b1/pay-wallet')).toBe(true)
    unmount()
  })

  it('does not pay past the credit', async () => {
    state.wallets = [{ currency: 'IRT', balance: -450_000, scale: 0, credit_limit: 500_000 }] // 50,000 left to spend
    fakeCore((c) => (c.path === '/orders' ? order() : c.path.startsWith('/methods') ? { methods: [] } : {}))
    const { el, unmount } = mount(CheckoutView, { plan, type: 'new', label: 'x' })
    await settle()
    await tap(el, 'Continue')
    expect(need<HTMLButtonElement>(el, 'button', 'Pay from the wallet').disabled).toBe(true)
    expect(text(el)).toContain('The wallet balance is too low.')
    unmount()
  })

  it('leaves a renewal without a name', async () => {
    const calls = fakeCore((c) => (c.path === '/orders' ? order({ type: 'renew' }) : c.path.startsWith('/methods') ? { methods: [] } : {}))
    const { el, unmount } = mount(CheckoutView, { plan, type: 'renew', subscriptionId: '00000000-0000-0000-0000-0000000000c1', label: 'ignored' })
    await settle()
    expect(text(el)).not.toContain('ignored')
    await tap(el, 'Continue')
    const created = calls.find((c) => c.path === '/orders')
    expect(created?.body).toMatchObject({ type: 'renew', subscription_id: '00000000-0000-0000-0000-0000000000c1' })
    expect(created?.body).not.toHaveProperty('label')
    unmount()
  })
})

describe('the checkout for anyone else', () => {
  it('keeps the discount code and sends no name', async () => {
    state.me = { ...ME, features: {} }
    state.wallets = [{ currency: 'IRT', balance: 500_000, scale: 0, credit_limit: 0 }]
    const calls = fakeCore((c) => (c.path === '/orders' ? order({ amount: 100_000 }) : c.path.startsWith('/methods') ? { methods: [] } : {}))
    const { el, unmount } = mount(CheckoutView, { plan: { ...plan, price: 100_000 }, type: 'new' })
    await settle()
    expect(find(el, 'input')).not.toBeNull()
    expect(find(el, 'button', 'Apply')).not.toBeNull()
    await tap(el, 'Continue')
    const created = calls.find((c) => c.path === '/orders')
    expect(created?.body).not.toHaveProperty('label')
    unmount()
  })
})
