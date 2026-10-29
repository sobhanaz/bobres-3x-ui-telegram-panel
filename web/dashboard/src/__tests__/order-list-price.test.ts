import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import type { OrderItem } from '../types'

// An order listed in USDT and paid from the Toman wallet shows what was charged and what it was listed at, on its page and in the list; an
// order without a list price shows nothing more. The server is a fetch that answers by address; the page is the real app.

const ID = '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60'
const OTHER = '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f61'
const NOW = Math.floor(Date.now() / 1000)

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const lookup = (tree: unknown, path: string): string =>
  path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], tree) as string
const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)

const order = (id: string, over: Partial<OrderItem> = {}): OrderItem => ({
  id, user: { id: 'u1', telegram_id: 4242, username: 'ali' }, plan: { id: 'p1', name: { en: 'Gold USDT', fa: 'طلایی تتری' } }, type: 'new', status: 'active',
  amount: { amount: 1_176_000, currency: 'IRT' }, discount: null, subscription_id: null, created_at: NOW - 600, updated_at: NOW - 300, attempts: 0,
  last_error: '', next_attempt_at: null, staff: '', extend: null, refund: null,
  list: { price: { amount: 4_000_000, currency: 'USDT' }, rate: 294_000 }, ...over,
})

describe('orders listed in USDT and paid from the wallet', () => {
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(path: string, language: 'fa' | 'en', orders: OrderItem[]) {
    unmount()
    vi.resetModules()
    localStorage.setItem('bobres.lang', language) // the page remembers the language it was last set to
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url === '/api/v1/me') {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'owner', language }, permissions: ['payments.read', 'payments.review', 'users.read', 'audit.read'],
            csrf: 'c', session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
          })
        }
        if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        const one = orders.find((o) => url === `/api/v1/orders/${o.id}`)
        if (one) return json(200, { order: one, payments: [], payments_error: '', can: { retry: false, refund: true } })
        if (url.startsWith('/api/v1/orders?')) return json(200, { items: orders, total: orders.length, page: 1, size: 25 })
        if (url.startsWith('/api/v1/payments')) return json(200, { items: [], total: 0, page: 1, size: 25 })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
  }
  afterEach(() => {
    unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })
  const found = async (selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<HTMLElement>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })

  it('shows the order page both prices, in Persian', async () => {
    await boot(`/admin/payments/orders/${ID}`, 'fa', [order(ID)])
    const facts = (await found('.app-facts')).textContent ?? ''
    await vi.waitFor(() => expect(document.querySelector('.list-price')).not.toBeNull())
    const line = document.querySelector('.list-price')?.textContent?.trim()
    expect(line).toBe(fill(lookup(fa, 'order.list'), { price: '۴ تتر', rate: '۲۹۴٬۰۰۰ تومان' }))
    expect(facts).toContain('۱٬۱۷۶٬۰۰۰ تومان')
  })

  it('shows the same in English, with the discount in USDT', async () => {
    await boot(`/admin/payments/orders/${ID}`, 'en', [order(ID, { discount: { code: 'QUARTER', amount: { amount: 1_000_000, currency: 'USDT' } } })])
    await vi.waitFor(() => expect(document.querySelector('.list-price')).not.toBeNull())
    expect(document.querySelector('.list-price')?.textContent?.trim()).toBe(fill(lookup(en, 'order.list'), { price: '4 USDT', rate: '294,000 Toman' }))
    const facts = document.querySelector('.app-facts')?.textContent ?? ''
    expect(facts).toContain('1,176,000 Toman')
    expect(facts).toContain(fill(lookup(en, 'order.discount'), { amount: '1 USDT' }))
  })

  it('shows no list price for an order paid in the currency it was listed in', async () => {
    await boot(`/admin/payments/orders/${OTHER}`, 'en', [order(OTHER, { amount: { amount: 4_000_000, currency: 'USDT' }, list: null })])
    await found('.app-facts')
    await vi.waitFor(() => expect(document.querySelector('.app-facts')?.textContent).toContain('4 USDT'))
    expect(document.querySelector('.list-price')).toBeNull()
  })

  it('shows the list price under the amount in the orders list', async () => {
    await boot('/admin/payments?tab=orders', 'en', [order(ID), order(OTHER, { amount: { amount: 5_000, currency: 'IRT' }, list: undefined })])
    const rowsOf = () => [...document.querySelectorAll('tbody tr')].map((r) => r.textContent ?? '')
    await vi.waitFor(() => expect(rowsOf().filter((r) => r.includes('Gold USDT'))).toHaveLength(2))
    const [listed, plain] = rowsOf().filter((r) => r.includes('Gold USDT'))
    expect(listed).toContain('1,176,000 Toman')
    expect(listed).toContain(fill(lookup(en, 'order.list_short'), { price: '4 USDT' }))
    expect(plain).toContain('5,000 Toman')
    expect(plain).not.toContain('USDT listed')
    expect(plain).not.toContain('Listed at')
  })
})
