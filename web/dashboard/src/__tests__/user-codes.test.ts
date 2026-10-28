import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import type { CodeItem } from '../types'

// The Codes tab of the customer page: the reward codes issued to the customer and the gift codes they applied, read only, for staff who may
// read the page. The server is a fetch that answers by address; the page is the real app.

const uid = '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60'
const oid = '0199a5b2-7c3e-7d11-9f2a-aaaaaaaaaaaa'
const t0 = Math.floor(Date.now() / 1000)
const day = 86_400

const reward = (over: Partial<CodeItem> = {}): CodeItem => ({
  id: 'THX-AB23CD45', kind: 'reward', code: 'THX-AB23CD45', state: 'unused', percent: 20, scale: 0, expires_at: t0 + 5 * day, used_at: null,
  received_at: t0 - day, plan: { en: 'Monthly 50GB', fa: 'ماهانه ۵۰ گیگ' }, order_id: oid, ...over,
})
const gift = (over: Partial<CodeItem> = {}): CodeItem => ({
  id: 'red-1', kind: 'gift', tail: 'JKMN', state: 'used', amount: 25_000, currency: 'IRT', scale: 0, expires_at: null, used_at: t0 - 3 * day, received_at: t0 - 3 * day, ...over,
})

const supportPerms = ['overview.read', 'users.read', 'services.read', 'payments.read', 'tickets.write']
const meOf = (permissions: string[], role = 'support') => ({
  user: { id: 's1', telegram_id: 1001, username: 'helper', role, language: 'fa' },
  permissions,
  csrf: 'csrf-1',
  session: { method: 'link', expires_at: 2_000_000_000 },
  password: { enabled: false },
  password_available: true,
  brand: 'Shop',
})
const customer = { id: uid, telegram_id: 777, username: 'cust', language: 'fa', role: 'user', status: 'active', created_at: t0 - 30 * day, wallets: [] }
const detail = { user: customer, counts: { subscriptions: 0, orders: 0, tickets: 0 }, referral: { referred_by: null, invited: 0, rewarded: 0, earned: [] }, can_ban: false }
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const page = (items: CodeItem[], total = items.length, p = 1, size = 10) => ({ items, total, page: p, size })

type Answer = (url: string, init?: RequestInit) => Response | Promise<Response> | undefined

describe('the customer page, Codes tab', () => {
  async function boot(path: string, permissions: string[], answer: Answer = () => undefined) {
    vi.resetModules()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push(`${init?.method ?? 'GET'} ${url}`)
        const own = answer(url, init)
        if (own) return own
        if (url === '/api/v1/me') return json(200, meOf(permissions))
        if (url === `/api/v1/users/${uid}`) return json(200, detail)
        if (url.startsWith('/api/v1/codes?')) return json(200, page([]))
        if (url.startsWith('/api/v1/')) return json(200, { items: [], total: 0, page: 1, size: 10 })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }

  afterEach(() => {
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  // the whole app boots in these tests, and slowly when the suite runs in parallel
  const waitFor = <T,>(fn: () => T) => vi.waitFor(fn, { timeout: 20_000 })
  const rows = () => [...document.querySelectorAll('.p-datatable.codes .p-datatable-tbody tr')] as HTMLElement[]
  const words = (el: Element) => (el.textContent ?? '').replace(/[⁦-⁩]/g, '').replace(/\s+/g, ' ').trim()
  const tabs = () => [...document.querySelectorAll('[role=tab]')].map((t) => (t.textContent ?? '').trim())

  it('lists the customer’s own codes, asking for this customer and no other, for staff who can read the page', async () => {
    const { calls } = await boot(`/admin/users/${uid}?tab=codes`, supportPerms, (url) => {
      if (url.startsWith('/api/v1/codes?')) return json(200, page([gift(), reward(), reward({ id: 'THX-OLD00002', code: 'THX-OLD00002', state: 'expired', percent: 0, amount: 40_000, currency: 'IRT', plan: undefined, order_id: undefined })]))
    })
    await waitFor(() => expect(calls).toContain(`GET /api/v1/codes?user=${uid}&page=1&size=10`))
    await waitFor(() => expect(rows()).toHaveLength(3))
    // the tab is there, selected, in the list of tabs next to the wallet history
    expect(tabs().some((t) => t.includes(fa.user.codes))).toBe(true)
    expect(document.querySelector('[role=tab][aria-selected=true]')?.textContent).toContain(fa.user.codes)
    const [g, r, old] = rows().map(words)
    // a gift code: only its last characters, the kind, what it added, used
    expect(g).toContain(fa.code.gift_ending)
    expect(g).toContain('JKMN')
    expect(g).toContain(fa.code.kind_gift)
    expect(g).toContain(fa.code.gift_added)
    expect(g).toContain(fa.code.status.used)
    // a reward code: whole, with what it takes off, its state and the purchase that gave it, linking to the order
    expect(r).toContain('THX-AB23CD45')
    expect(r).toContain(fa.code.kind_reward)
    expect(r).toContain('۲۰٪')
    expect(r).toContain(fa.code.off_any)
    expect(r).toContain(fa.code.status.unused)
    expect(r).toContain('ماهانه ۵۰ گیگ')
    const link = rows()[1].querySelector('a[href*="/orders/"]') as HTMLAnchorElement
    expect(link.getAttribute('href')).toContain(oid)
    // an amount code that ended, bought by no order
    expect(old).toContain('THX-OLD00002')
    expect(old).toContain('۴۰٬۰۰۰ تومان')
    expect(old).toContain(fa.code.status.expired)
    expect(rows()[2].querySelector('a')).toBeNull()
    // nothing here changes anything
    expect(calls.every((c) => c.startsWith('GET '))).toBe(true)
    expect(rows().every((row) => row.querySelector('button') === null)).toBe(true)
  }, 30_000)

  it('says so when the customer has no codes, and only asks when the tab is opened', async () => {
    const { calls } = await boot(`/admin/users/${uid}`, supportPerms)
    await waitFor(() => expect(calls).toContain(`GET /api/v1/services?user=${uid}&page=1&size=10`))
    expect(calls.some((c) => c.startsWith('GET /api/v1/codes'))).toBe(false)
    const tab = [...document.querySelectorAll('[role=tab]')].find((t) => (t.textContent ?? '').includes(fa.user.codes)) as HTMLElement
    tab.click()
    await waitFor(() => expect(calls).toContain(`GET /api/v1/codes?user=${uid}&page=1&size=10`))
    await waitFor(() => expect(document.querySelector('.p-datatable.codes')?.textContent).toContain(fa.user.codes_none))
  }, 30_000)

  it('pages with the table’s own paginator', async () => {
    const many = Array.from({ length: 10 }, (_, i) => reward({ id: `THX-PAGE00${i}`, code: `THX-PAGE00${i}` }))
    const { calls } = await boot(`/admin/users/${uid}?tab=codes`, supportPerms, (url) => {
      if (url === `/api/v1/codes?user=${uid}&page=2&size=10`) return json(200, page([reward({ id: 'THX-LAST0001', code: 'THX-LAST0001' })], 11, 2))
      if (url.startsWith('/api/v1/codes?')) return json(200, page(many, 11))
    })
    await waitFor(() => expect(rows()).toHaveLength(10))
    const next = await waitFor(() => {
      const b = document.querySelector('.p-datatable.codes .p-paginator-next') as HTMLButtonElement | null
      expect(b).not.toBeNull()
      return b!
    })
    next.click()
    await waitFor(() => expect(calls).toContain(`GET /api/v1/codes?user=${uid}&page=2&size=10`))
    await waitFor(() => expect(rows().map(words)[0]).toContain('THX-LAST0001'))
  }, 30_000)
})

// Every text the tab asks for is in both catalogs, and the status words name each state a code can be in.
describe('the Codes tab texts', () => {
  it('exist in Persian and English', () => {
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of ['received', 'code', 'gift_ending', 'kind', 'kind_reward', 'kind_gift', 'worth', 'off_any', 'off_plan', 'gift_added', 'valid_until', 'never', 'used_on', 'from', 'from_order']) {
        expect(typeof cat.code[k], k).toBe('string')
      }
      for (const s of ['unused', 'used', 'expired', 'off']) expect(typeof cat.code.status[s], s).toBe('string')
      expect(typeof cat.user.codes).toBe('string')
      expect(typeof cat.user.codes_none).toBe('string')
    }
  })
})
