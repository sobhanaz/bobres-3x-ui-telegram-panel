import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type Component } from 'vue'
import { resetSession, type Me } from '../../../api'
import { allSections } from '../../../sections'
import { setLang } from '../../../i18n'
import { state } from '../../../store'
import DigitalView from '../DigitalView.vue'
import '../../index'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const ME: Me = {
  user: { id: 'u', username: '', language: 'en' },
  wallets: [],
  brand: { name: 'Acme', color: '', support: '', support_url: '', bot_url: '' },
  maintenance: false,
  top_up: { currency: 'IRT', min: 1, max: 2 },
  features: { digital: true },
}

type Handler = (body: Record<string, unknown> | undefined) => Response
function mockApi(routes: Record<string, Handler>) {
  const calls: { key: string; body?: Record<string, unknown> }[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const key = `${init?.method ?? 'GET'} ${String(url).replace('/api/mini/v1', '')}`
    const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined
    calls.push({ key, body })
    if (key === 'POST /auth') return json(200, { ...ME, token: 'tok' })
    return routes[key]?.(body) ?? json(404, { error: 'not_found', message: key })
  })
  return calls
}

const mounted: (() => void)[] = []
function mount(component: Component, props: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(component, props)
  app.mount(el)
  mounted.push(() => {
    app.unmount()
    el.remove()
  })
  return el
}
const text = (el: Element) => el.textContent?.replace(/\s+/g, ' ').trim() ?? ''
const button = (el: Element, label: string) => [...el.querySelectorAll('button')].find((b) => text(b).includes(label))
const seen = (el: Element, s: string) => vi.waitFor(() => expect(text(el)).toContain(s))

beforeEach(() => {
  resetSession()
  setLang('en')
  window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
  state.me = { ...ME, features: { ...ME.features } }
})
afterEach(() => {
  mounted.splice(0).forEach((f) => f())
  vi.useRealTimers()
  vi.restoreAllMocks()
})

const gem = { sid: 'abcde12345', names: { fa: 'جمینای', en: 'Gemini Pro' }, descriptions: { fa: '', en: 'A Gemini Pro account' }, category: 'AI', price: 450000, sold_out: false, stock: 5, stock_known: true }
const chat = { sid: 'zzzzz99999', names: { en: 'Chat Plus' }, descriptions: {}, category: 'AI', price: 300000, sold_out: true, stock: 0, stock_known: true }
const vpn = { sid: 'vvvvv11111', names: { en: 'VPN year' }, descriptions: {}, category: '', price: 100000, sold_out: false, stock: 0, stock_known: false }
const shelf = (items: unknown[], balance = 1_000_000) => ({ items, wallet: { currency: 'IRT', balance, credit_limit: 0, scale: 0 }, currency: 'IRT', scale: 0, max_quantity: 25 })
const done = { id: 'o1', names: gem.names, quantity: 2, total: { amount: 900000, currency: 'IRT', scale: 0 }, status: 'done', at: 1_790_000_000, delivery: 'a@x.com\npassword: pw' }

describe('digital accounts section', () => {
  it('is registered and visible only when core says the store sells them', () => {
    const s = allSections().find((x) => x.id === 'digital')
    expect(s).toBeDefined()
    expect(s!.visible({ ...ME, features: { digital: true } })).toBe(true)
    expect(s!.visible({ ...ME, features: {} })).toBe(false)
  })

  it('walks categories, product, quantity and summary, pays once and shows the account', async () => {
    const calls = mockApi({
      'GET /digital': () => json(200, shelf([gem, chat, vpn])),
      'POST /digital/buy': () => json(200, done),
    })
    const el = mount(DigitalView)
    await seen(el, 'Choose a category')
    expect(text(el)).toContain('Wallet balance: 1,000,000 Toman')
    expect(text(el)).toContain('AI 2 products')
    expect(text(el)).toContain('Other 1 products')

    button(el, 'AI')!.click()
    await seen(el, 'Gemini Pro')
    expect(text(el)).toContain('Chat Plus') // sold out is listed, marked
    expect(text(el)).toContain('Sold out')
    button(el, 'Gemini Pro')!.click()
    await seen(el, 'A Gemini Pro account')
    expect(text(el)).toContain('In stock: 5')
    button(el, 'Buy')!.click()

    await seen(el, 'How many do you want?')
    expect(button(el, '10')).toBeUndefined() // only 5 in stock
    expect(button(el, '5')).toBeDefined()
    button(el, '2')!.click()
    await seen(el, 'Order summary')
    expect(text(el)).toContain('Total900,000 Toman')
    expect(text(el)).toContain('Balance after buying100,000 Toman')
    button(el, 'Confirm and pay')!.click()
    await seen(el, 'This is your account')
    expect(text(el)).toContain('a@x.com')
    const buys = calls.filter((c) => c.key === 'POST /digital/buy')
    expect(buys).toHaveLength(1)
    expect(buys[0].body).toMatchObject({ sid: 'abcde12345', quantity: 2, expected_total: 900000 })
    expect(String(buys[0].body?.key)).toMatch(/^[a-z0-9]{24}$/)
  })

  it('asks for a typed quantity within the limits', async () => {
    mockApi({ 'GET /digital': () => json(200, shelf([{ ...vpn, category: 'AI' }])) })
    const el = mount(DigitalView)
    await seen(el, 'VPN year')
    button(el, 'VPN year')!.click()
    await seen(el, 'Price each')
    button(el, 'Buy')!.click()
    await seen(el, 'Or type a number (1 to 25)')
    const input = el.querySelector('input')!
    input.value = '99'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    button(el, 'Continue')!.click()
    await seen(el, 'The quantity must be 1 to 25.')
    input.value = '7'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    button(el, 'Continue')!.click()
    await seen(el, 'Order summary')
    expect(text(el)).toContain('Quantity7')
  })

  it('sends a short wallet to the top-up and never calls buy', async () => {
    const calls = mockApi({ 'GET /digital': () => json(200, shelf([{ ...gem, category: '' }, vpn], 100_000)) })
    const onWallet = vi.fn()
    const el = mount(DigitalView, { onWallet })
    await seen(el, 'Gemini Pro') // one category only: straight to the products
    button(el, 'Gemini Pro')!.click()
    await seen(el, 'Buy')
    button(el, 'Buy')!.click()
    await seen(el, 'How many')
    button(el, '1')!.click()
    await seen(el, 'Your wallet is 350,000 Toman short.')
    expect(button(el, 'Confirm and pay')).toBeUndefined()
    button(el, 'Top up the wallet')!.click()
    expect(onWallet).toHaveBeenCalledOnce()
    expect(calls.some((c) => c.key === 'POST /digital/buy')).toBe(false)
  })

  it('shows the shelf again when the price or the stock moved', async () => {
    let n = 0
    const calls = mockApi({
      'GET /digital': () => json(200, shelf([{ ...gem, price: ++n > 1 ? 500000 : 450000 }])),
      'POST /digital/buy': () => json(409, { error: 'state', message: 'the price changed' }),
    })
    const el = mount(DigitalView)
    await seen(el, 'Gemini Pro')
    button(el, 'Gemini Pro')!.click()
    await seen(el, 'Buy')
    button(el, 'Buy')!.click()
    await seen(el, 'How many')
    button(el, '1')!.click()
    await seen(el, 'Order summary')
    button(el, 'Confirm and pay')!.click()
    await seen(el, 'The price or the stock changed')
    await seen(el, '500,000 Toman')
    expect(calls.filter((c) => c.key === 'GET /digital')).toHaveLength(2)
  })

  it('follows a purchase the supplier has not delivered yet', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let asked = 0
    mockApi({
      'GET /digital': () => json(200, shelf([{ ...gem, category: '' }])),
      'POST /digital/buy': () => json(200, { ...done, status: 'paid', delivery: '' }),
      'GET /digital/orders/o1': () => json(200, ++asked > 1 ? done : { ...done, status: 'paid', delivery: '' }),
    })
    const el = mount(DigitalView)
    await seen(el, 'Gemini Pro')
    button(el, 'Gemini Pro')!.click()
    await seen(el, 'Buy')
    button(el, 'Buy')!.click()
    await seen(el, 'How many')
    button(el, '2')!.click()
    await seen(el, 'Order summary')
    button(el, 'Confirm and pay')!.click()
    await seen(el, 'the account is being prepared')
    vi.advanceTimersByTime(3000)
    await seen(el, 'the account is being prepared')
    vi.advanceTimersByTime(3000)
    await seen(el, 'This is your account')
    expect(text(el)).toContain('password: pw')
  })

  it('lists my purchases without the account and opens one with it', async () => {
    mockApi({
      'GET /digital': () => json(200, shelf([{ ...gem, category: '' }])),
      'GET /digital/orders': () => json(200, { orders: [{ ...done, delivery: undefined }, { ...done, id: 'o2', status: 'refunded', delivery: undefined }] }),
      'GET /digital/orders/o1': () => json(200, done),
    })
    const el = mount(DigitalView)
    await seen(el, 'Gemini Pro')
    button(el, 'My purchases')!.click()
    await seen(el, 'Gemini Pro × 2')
    expect(text(el)).toContain('Delivered')
    expect(text(el)).toContain('Refunded')
    expect(text(el)).not.toContain('password: pw')
    button(el, 'Delivered')!.click()
    await seen(el, 'password: pw')
  })

  it('shows an empty shelf as empty', async () => {
    mockApi({ 'GET /digital': () => json(404, { error: 'not_found', message: 'x' }) })
    const el = mount(DigitalView)
    await seen(el, 'Nothing is for sale right now.')
  })
})
