import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SupplierPricing, SupplierProduct } from '../supplier'

// The Digital accounts page, pricing from the USDT price: the card with the rate and the markup, the mode of each product, the confirmation of
// "make all products follow the price", and the catalog read the page makes by itself when it opens on an old one. Against a fake core.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()

const me = () => ({
  user: { id: 'u0', telegram_id: 1, username: 'boss', role: 'owner', language: 'en' },
  permissions: ['overview.read', 'gateways.write', 'settings.write', 'private.features'],
  csrf: 'csrf-1',
  session: { method: 'link', expires_at: 2_000_000_000 },
  password: { enabled: false },
  password_available: true,
  brand: 'Shop',
  branding: { color: '#0f766e', logo: null, currency: { fa: '', en: 'T' } },
})

const now = Math.floor(Date.now() / 1000)
const product = (id: string, over: Partial<SupplierProduct> = {}): SupplierProduct => ({
  id, name: `Product ${id}`, description: 'supplier text', category: '', cost_micro: 2_600_000, cost_currency: 'USDT', stock: 3, listed: true, enabled: false,
  sell_price: 437_000, price_mode: 'auto', can_follow: true, title: { fa: 'الف' }, desc: { fa: 'ب' }, sort: 0, synced_at: now, ...over,
})
const products = (synced = now): SupplierProduct[] => [
  product('auto-1', { synced_at: synced }),
  product('own-1', { price_mode: 'fixed', sell_price: 700_000, synced_at: synced }),
  product('nocost-1', { price_mode: 'fixed', sell_price: 50_000, cost_micro: null, can_follow: false, title: {}, desc: {}, synced_at: synced }),
]
const pricing = (over: Partial<SupplierPricing> = {}): SupplierPricing => ({
  auto: true, markup: 20, max_markup: 500, step: 1000,
  usdt: { auto: true, margin: 0, manual: 0, rate: 140_000, source: 'live', live: { rate: 140_000, at: now - 120, sources: ['nobitex'], fresh: true } },
  applied_rate: 140_000, applied_at: now - 120, counts: { auto: 1, fixed: 2, hand_priced: 1, cannot_follow: 1 }, ...over,
})

describe('the Digital accounts page: pricing from the USDT price', () => {
  afterEach(() => {
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    try {
      localStorage.removeItem('bobres.lang')
    } catch {
      /* no storage in this environment */
    }
    document.body.innerHTML = ''
  })

  async function boot(calls: string[], opts: { synced?: number; pricing?: SupplierPricing } = {}) {
    vi.resetModules()
    localStorage.setItem('bobres.lang', 'en')
    window.history.replaceState(null, '', '/admin/supplier')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const state = { pricing: opts.pricing ?? pricing() }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        calls.push(`${method} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
        if (url === '/api/v1/me') return json(200, me())
        if (url === '/api/v1/branding') return json(200, { name: 'Shop', support: '', color: '#0f766e', terms_url: '', privacy_url: '', currency: { fa: '', en: 'T' }, logo: null, defaults: { name: 'BOBRES', color: '#14b8a6', currency: { fa: 'تومان', en: 'Toman' } } })
        if (url === '/api/v1/supplier' && method === 'GET') return json(200, { available: true, configured: true, on: true })
        if (url === '/api/v1/supplier/products') return json(200, { items: products(opts.synced), total: 3 })
        if (url === '/api/v1/supplier/pricing' && method === 'GET') return json(200, state.pricing)
        if (url === '/api/v1/supplier/pricing' && method === 'PUT') {
          const b = JSON.parse(String(init?.body)) as { auto: boolean; markup: number }
          state.pricing = pricing({ auto: b.auto, markup: b.markup })
          return json(200, { ...state.pricing, changed: 2, switched: 0, replaced: 0 })
        }
        if (url === '/api/v1/supplier/pricing/apply') {
          state.pricing = pricing({ counts: { auto: 3, fixed: 0, hand_priced: 0, cannot_follow: 1 } })
          return json(200, { ...state.pricing, changed: 1, switched: 1, replaced: 1 })
        }
        if (url === '/api/v1/supplier/sync') return json(200, { count: 3 })
        if (url === '/api/v1/supplier/balance') return json(200, { amount: '5', currency: 'USDT' })
        if (url.startsWith('/api/v1/supplier/orders')) return json(200, { items: [], total: 0, page: 1, size: 25 })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    await vi.waitFor(() => expect(document.querySelector('.p-datatable-tbody')).not.toBeNull())
  }
  const button = (label: string) => [...document.querySelectorAll('button')].find((b) => text(b) === label) as HTMLButtonElement | undefined
  const rows = () => [...document.querySelectorAll('.p-datatable-tbody tr')].map((r) => text(r))

  it('shows the USDT price, where it comes from, the markup and how each product is priced', async () => {
    const calls: string[] = []
    await boot(calls)
    await vi.waitFor(() => expect(text(document.body)).toContain('1 USDT = 140,000 Toman'))
    const body = text(document.body)
    expect(body).toContain('Pricing from the USDT price')
    expect(body).toContain('Live price')
    expect((document.querySelector('input.markup') as HTMLInputElement).value).toBe('20')
    expect(body).toContain('Follow the price: 1 · own price: 2')
    const r = rows()
    expect(r[0]).toContain('437,000 T') // the store's own name for its currency (here "T")
    expect(r[0]).toContain('Follows USDT')
    expect(r[1]).toContain('700,000 T')
    expect(r[1]).toContain('Own price')
    expect(r[1]).toContain('The formula gives 437,000 T') // to compare with the typed price
    expect(r[2]).not.toContain('Own price') // no cost in USDT: no mode to show
    expect(r[2]).toContain('Needs texts')
    // A catalog read a minute ago is not read again.
    expect(calls.filter((c) => c.startsWith('POST /api/v1/supplier/sync'))).toHaveLength(0)
  })

  it('saves the markup and the switch, and prices at once', async () => {
    const calls: string[] = []
    await boot(calls)
    await vi.waitFor(() => expect(document.querySelector('input.markup')).not.toBeNull())
    const input = document.querySelector('input.markup') as HTMLInputElement
    expect(button('Save and price')!.disabled).toBe(true) // nothing changed yet
    input.value = '25'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await vi.waitFor(() => expect(button('Save and price')!.disabled).toBe(false))
    button('Save and price')!.click()
    await vi.waitFor(() => expect(calls).toContain('PUT /api/v1/supplier/pricing {"auto":true,"markup":25}'))
    await vi.waitFor(() => expect(text(document.body)).toContain('Saved. Products priced: 2.'))
    // A markup core would refuse is not sent.
    input.value = '501'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await vi.waitFor(() => expect(button('Save and price')!.disabled).toBe(true))
  })

  it('asks before it replaces the prices the owner typed, then makes every product follow the price', async () => {
    const calls: string[] = []
    await boot(calls)
    await vi.waitFor(() => expect(button('Make all products follow the price')).toBeDefined())
    expect(button('Make all products follow the price')!.disabled).toBe(false)
    button('Make all products follow the price')!.click()
    await vi.waitFor(() => expect(text(document.querySelector('.p-dialog'))).toContain('Prices you typed that this replaces: 1.'))
    expect(calls.some((c) => c.startsWith('POST /api/v1/supplier/pricing/apply'))).toBe(false) // nothing moved yet
    const confirm = [...document.querySelectorAll('.p-dialog button')].find((b) => text(b) === 'Make all products follow the price') as HTMLButtonElement
    confirm.click()
    await vi.waitFor(() => expect(calls).toContain('POST /api/v1/supplier/pricing/apply {"all":true}'))
    await vi.waitFor(() => expect(text(document.body)).toContain('Products now following the price: 1 (typed prices replaced: 1).'))
  })

  it('does not offer to apply while the pricing is off or the USDT price is not known', async () => {
    await boot([], { pricing: pricing({ auto: false }) })
    await vi.waitFor(() => expect(button('Make all products follow the price')).toBeDefined())
    expect(button('Make all products follow the price')!.disabled).toBe(true)
    document.body.innerHTML = ''
  })

  it('says so, and offers to read it, when no USDT price is known', async () => {
    await boot([], { pricing: pricing({ auto: false, usdt: { auto: true, margin: 0, manual: 0, rate: 0, source: '', live: null } }) })
    await vi.waitFor(() => expect(text(document.body)).toContain('The USDT price is not known yet.'))
    expect(button('Read the price now')).toBeDefined()
    expect(button('Make all products follow the price')!.disabled).toBe(true)
  })

  it('reads the catalog again by itself when it opens on an old one', async () => {
    const calls: string[] = []
    await boot(calls, { synced: now - 3600 })
    await vi.waitFor(() => expect(calls).toContain('POST /api/v1/supplier/sync'))
    // ...and shows the new prices after it
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/supplier/pricing').length).toBeGreaterThanOrEqual(2))
  })
})
