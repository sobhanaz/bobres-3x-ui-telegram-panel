import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import en from '../locales/en.json'
import {
  afterPayment,
  amountOf,
  blankTerms,
  blankTier,
  byCurrency,
  debts,
  limitsBody,
  owed,
  paymentAmount,
  paymentCurrency,
  percent,
  percentOf,
  planPriceFor,
  priceablePlans,
  resellerOf,
  termsBody,
  termsFormOf,
  termsProblems,
  tierBody,
  tierFormOf,
  tierPrice,
  tierProblems,
  typedAmount,
  walletRows,
} from '../resellers'
import { auditLink } from '../staff'
import type { LedgerItem, PlanItem, ResellerDetail, ResellerItem, ServiceItem, TierItem, UserItem } from '../types'

const t0 = 1_760_000_000
const rid = 'aaaaaaaa-1111-4222-8333-444444444444'
const plan = (id: string, amount: number, currency = 'IRT', over: Partial<PlanItem> = {}): PlanItem => ({
  id, name: { fa: 'پلن ' + id, en: 'Plan ' + id }, kind: 'both', duration_days: 30, traffic_bytes: 50 * 2 ** 30,
  price: { amount, currency }, enabled: true, is_trial: false, is_topup: false, sort: 0, sales: 0, ...over,
})
const p1 = plan('p1', 120000)
const p2 = plan('p2', 2_500_000, 'USDT')
const trial = plan('p3', 0, 'IRT', { is_trial: true })
const plans = [p1, p2, trial]

const item = (over: Partial<ResellerItem> = {}): ResellerItem => ({
  user: { id: rid, telegram_id: 555, username: 'shop_ali', status: 'active' },
  tier: { id: 't1', name: 'Gold', discount_percent: 20, commission_percent: 10 },
  commission_percent: 10,
  commission_override: null,
  balances: [
    { currency: 'USDT', balance: 0, credit_limit: 0 },
    { currency: 'IRT', balance: -250000, credit_limit: 1_000_000 },
  ],
  customers: 12,
  commission_earned: [{ amount: 36000, currency: 'IRT' }],
  created_at: t0 - 30 * 86_400,
  ...over,
})

describe('amounts and balances', () => {
  it('reads amounts as minor units or decimals', () => {
    expect(amountOf(250000, 'IRT')).toBe(250000)
    expect(amountOf(-250000, 'IRT')).toBe(-250000)
    expect(amountOf('2.5', 'USDT')).toBe(2_500_000)
    expect(amountOf('-1200', 'IRT')).toBe(-1200)
    expect(amountOf('۱۲۰٬۰۰۰', 'IRT')).toBe(120000)
    expect(amountOf('nope', 'IRT')).toBe(0)
    expect(amountOf(null, 'IRT')).toBe(0)
    expect(amountOf(Number.NaN, 'IRT')).toBe(0)
  })
  it('puts Toman first', () => {
    const list = [{ currency: 'XTR' }, { currency: 'USDT' }, { currency: 'IRT' }, { currency: 'ABC' }]
    expect(byCurrency(list).map((m) => m.currency)).toEqual(['IRT', 'USDT', 'ABC', 'XTR'])
    expect(list[0].currency).toBe('XTR') // the input stays as it was
  })
  it('works out what a reseller can still spend', () => {
    const rows = walletRows(item().balances)
    expect(rows).toEqual([
      { currency: 'IRT', balance: -250000, limit: 1_000_000, available: 750_000 },
      { currency: 'USDT', balance: 0, limit: 0, available: 0 },
    ])
    expect(walletRows([])).toEqual([{ currency: 'IRT', balance: 0, limit: 0, available: 0 }])
    expect(walletRows(undefined)).toHaveLength(1)
    // decimals, should core write the balances as strings
    expect(walletRows([{ currency: 'USDT', balance: '-1.5' as unknown as number, credit_limit: '10' as unknown as number }])[0]).toEqual({
      currency: 'USDT', balance: -1_500_000, limit: 10_000_000, available: 8_500_000,
    })
  })
  it('turns a balance below zero into what is owed', () => {
    expect(owed({ amount: -250000, currency: 'IRT' })).toEqual({ amount: 250000, currency: 'IRT' })
    expect(owed({ amount: 0, currency: 'IRT' })).toBeNull()
    expect(owed({ amount: 5, currency: 'IRT' })).toBeNull()
    expect(debts(walletRows(item().balances))).toEqual([{ amount: 250000, currency: 'IRT' }])
    expect(debts(walletRows([]))).toEqual([])
  })
  it('writes percentages in the language', () => {
    expect(percent(20, 'fa')).toBe('۲۰٪')
    expect(percent(20, 'en')).toBe('20%')
    expect(percent(0, 'en')).toBe('0%')
  })
})

describe('reading a reseller', () => {
  it('reads a list row', () => {
    const r = resellerOf(item())
    expect(r.id).toBe(rid)
    expect(r.tier).toEqual({ id: 't1', name: 'Gold' })
    expect(r.discount).toBe(20)
    expect(r.commission).toBe(10)
    expect(r.override).toBeNull()
    expect(r.wallets[0].currency).toBe('IRT')
    expect(r.earned).toEqual([{ amount: 36000, currency: 'IRT' }])
    expect(r.note).toBe('')
    // a row says nothing about removing: allowed only without debt
    expect(r.canRemove).toBe(false)
    expect(resellerOf(item({ balances: [] })).canRemove).toBe(true)
  })
  it('reads the item page answer', () => {
    const d: ResellerDetail = { ...item({ commission_override: 15, commission_percent: 15 }), note: 'pays on Fridays', version: 4, can_remove: true }
    const r = resellerOf(d)
    expect(r.discount).toBe(20)
    expect(r.override).toBe(15)
    expect(r.commission).toBe(15)
    expect(r.note).toBe('pays on Fridays')
    expect(r.version).toBe(4)
    expect(r.canRemove).toBe(true) // core's word wins
    expect(resellerOf({ ...item({ tier: { id: 't1', name: 'Gold', discount_percent: 25, commission_percent: 5 } }), note: '', version: 1, can_remove: false }).discount).toBe(25)
  })
})

describe('typed numbers', () => {
  it('reads whole percentages in either digit set', () => {
    expect(percentOf('۲۰', 90)).toBe(20)
    expect(percentOf('20%', 90)).toBe(20)
    expect(percentOf(' ٪۵ ', 50)).toBe(5)
    expect(percentOf('0', 50)).toBe(0)
    expect(percentOf('90', 90)).toBe(90)
    expect(percentOf('91', 90)).toBeNull()
    expect(percentOf('51', 50)).toBeNull()
    expect(percentOf('-1', 90)).toBeNull()
    expect(percentOf('1.5', 90)).toBeNull()
    expect(percentOf('', 90)).toBeNull()
    expect(percentOf('abc', 90)).toBeNull()
  })
  it('reads amounts of at least zero', () => {
    expect(typedAmount('۵٬۰۰۰٬۰۰۰', 'IRT')).toBe(5_000_000)
    expect(typedAmount('2.5', 'USDT')).toBe(2_500_000)
    expect(typedAmount('0', 'IRT')).toBe(0)
    expect(typedAmount('', 'IRT')).toBeNull()
    expect(typedAmount('-5', 'IRT')).toBeNull()
    expect(typedAmount('1.5', 'IRT')).toBeNull()
    expect(typedAmount('1', 'EUR')).toBeNull()
  })
  it('takes payments above zero only', () => {
    expect(paymentAmount('۱۰۰٬۰۰۰', 'IRT')).toBe(100000)
    expect(paymentAmount('0.5', 'USDT')).toBe(500_000)
    expect(paymentAmount('0', 'IRT')).toBeNull()
    expect(paymentAmount('-100', 'IRT')).toBeNull()
    expect(paymentAmount('', 'IRT')).toBeNull()
    expect(paymentAmount('1.25', 'IRT')).toBeNull()
  })
  it('shows what a payment leaves, in the currency owed', () => {
    const rows = walletRows(item().balances)
    expect(afterPayment(rows, 'IRT', 100000)).toEqual({ amount: -150000, currency: 'IRT' })
    expect(afterPayment(rows, 'IRT', 300000)).toEqual({ amount: 50000, currency: 'IRT' })
    expect(afterPayment(rows, 'USDT', 1_000_000)).toEqual({ amount: 1_000_000, currency: 'USDT' })
    expect(afterPayment([], 'IRT', 5)).toEqual({ amount: 5, currency: 'IRT' })
    expect(paymentCurrency(rows)).toBe('IRT')
    expect(paymentCurrency(walletRows([{ currency: 'USDT', balance: -1, credit_limit: 5 }, { currency: 'IRT', balance: 0, credit_limit: 0 }]))).toBe('USDT')
    expect(paymentCurrency(walletRows([]))).toBe('IRT')
  })
})

describe('tier prices', () => {
  it('rounds a discounted price down as core does', () => {
    expect(tierPrice(120000, 20)).toBe(96000)
    expect(tierPrice(99999, 15)).toBe(84999) // 84999.15
    expect(tierPrice(2_500_000, 33)).toBe(1_675_000)
    expect(tierPrice(1, 50)).toBe(0)
    expect(tierPrice(120000, 0)).toBe(120000)
    expect(tierPrice(0, 90)).toBe(0)
    expect(tierPrice(1e12, 90)).toBe(1e11) // the largest prices stay exact
  })
  it('prices every plan but free trials', () => {
    expect(priceablePlans(plans).map((p) => p.id)).toEqual(['p1', 'p2'])
  })
  it('uses a fixed price over the discount', () => {
    expect(planPriceFor(p1, { discount: '20', prices: {} })).toEqual({ amount: 96000, fixed: false })
    expect(planPriceFor(p1, { discount: '20', prices: { p1: '۹۰٬۰۰۰' } })).toEqual({ amount: 90000, fixed: true })
    expect(planPriceFor(p1, { discount: '20', prices: { p1: '0' } })).toEqual({ amount: 0, fixed: true })
    // a price still being typed does not count yet
    expect(planPriceFor(p1, { discount: '20', prices: { p1: '9.5' } })).toEqual({ amount: 96000, fixed: false })
    expect(planPriceFor(p2, { discount: 'x', prices: {} })).toEqual({ amount: 2_500_000, fixed: false })
  })
  it('reads a tier into the editor and back', () => {
    const tier: TierItem = {
      id: 't1', name: 'Gold', discount_percent: 20, commission_percent: 10, resellers: 1, version: 3,
      prices: [{ plan_id: 'p1', price: '90000' }, { plan_id: 'p2', price: '2.100000' }, { plan_id: 'gone', price: '5' }],
    }
    const f = tierFormOf(tier, plans)
    expect(f).toEqual({ name: 'Gold', discount: '20', commission: '10', prices: { p1: '90000', p2: '2.1', gone: '5' } })
    expect(tierProblems(f, plans)).toEqual([])
    // only listed plans with a price go back
    expect(tierBody(f, plans)).toEqual({
      name: 'Gold', discount_percent: 20, commission_percent: 10,
      prices: [{ plan_id: 'p1', price: '90000' }, { plan_id: 'p2', price: '2.1' }],
    })
    // before the plans are known, the prices stay as core wrote them
    expect(tierFormOf(tier, []).prices).toEqual({ p1: '90000', p2: '2.100000', gone: '5' })
  })
  it('checks the editor as core will', () => {
    const ok = { ...blankTier(), name: ' Bronze ', discount: '۱۵', commission: '5', prices: { p1: '', p2: '2.1' } }
    expect(tierProblems(ok, plans)).toEqual([])
    expect(tierBody(ok, plans)).toEqual({ name: 'Bronze', discount_percent: 15, commission_percent: 5, prices: [{ plan_id: 'p2', price: '2.1' }] })
    expect(tierProblems(blankTier(), plans)).toEqual(['name'])
    expect(tierProblems({ ...ok, name: 'ب'.repeat(41) }, plans)).toEqual(['name'])
    expect(tierProblems({ ...ok, name: 'ب'.repeat(40) }, plans)).toEqual([])
    expect(tierProblems({ ...ok, discount: '91', commission: '51' }, plans)).toEqual(['discount_percent', 'commission_percent'])
    expect(tierProblems({ ...ok, discount: '', commission: '' }, plans)).toEqual(['discount_percent', 'commission_percent'])
    // Toman has no fractions; prices are never negative; a trial is never priced
    expect(tierProblems({ ...ok, prices: { p1: '1.5', p2: '-1', p3: 'x' } }, plans)).toEqual(['prices.p1', 'prices.p2'])
    expect(tierBody({ ...ok, prices: { p1: '۱۰۰٬۰۰۰', p3: '5' } }, plans).prices).toEqual([{ plan_id: 'p1', price: '100000' }])
  })
})

describe('terms', () => {
  const r = resellerOf({ ...item(), note: 'pays on Fridays', version: 4, can_remove: false })
  it('reads the terms into the editor', () => {
    expect(termsFormOf(r)).toEqual({ tier: 't1', limits: { IRT: '1000000', USDT: '' }, commission: '', note: 'pays on Fridays' })
    const own = resellerOf({ ...item({ commission_override: 0 }), note: '', version: 1, can_remove: true })
    expect(termsFormOf(own).commission).toBe('0')
  })
  it('never lets the limit drop below what they owe', () => {
    const f = termsFormOf(r)
    expect(termsProblems(f, r.wallets)).toEqual([])
    expect(termsProblems({ ...f, limits: { IRT: '200000', USDT: '' } }, r.wallets)).toEqual(['credit_limit.IRT'])
    expect(termsProblems({ ...f, limits: { IRT: '', USDT: '' } }, r.wallets)).toEqual(['credit_limit.IRT'])
    expect(termsProblems({ ...f, limits: { IRT: '250000', USDT: '' } }, r.wallets)).toEqual([]) // exactly the debt
    expect(termsProblems({ ...f, limits: { IRT: '1000000', USDT: '1.2345678' } }, r.wallets)).toEqual(['credit_limit.USDT'])
  })
  it('checks the other fields', () => {
    const f = termsFormOf(r)
    expect(termsProblems({ ...f, tier: '' }, r.wallets)).toEqual(['tier_id'])
    expect(termsProblems({ ...f, commission: '51' }, r.wallets)).toEqual(['commission_percent'])
    expect(termsProblems({ ...f, commission: '۵۰' }, r.wallets)).toEqual([])
    expect(termsProblems({ ...f, note: 'x'.repeat(501) }, r.wallets)).toEqual(['note'])
    expect(termsProblems({ ...f, note: 'x'.repeat(500) }, r.wallets)).toEqual([])
    expect(termsProblems(blankTerms())).toEqual(['tier_id'])
    expect(termsProblems({ ...blankTerms(), tier: 't1', limits: { IRT: '-5' } })).toEqual(['credit_limit.IRT'])
  })
  it('sends each typed limit, and 0 for a cleared one', () => {
    expect(limitsBody({ IRT: '۲٬۰۰۰٬۰۰۰', USDT: '' }, r.wallets)).toEqual([{ currency: 'IRT', amount: '2000000' }])
    const both = walletRows([{ currency: 'IRT', balance: 0, credit_limit: 500 }, { currency: 'USDT', balance: 0, credit_limit: 5_000_000 }])
    expect(limitsBody({ IRT: '500', USDT: '' }, both)).toEqual([{ currency: 'IRT', amount: '500' }, { currency: 'USDT', amount: '0' }])
    expect(limitsBody({ IRT: '' })).toEqual([])
    expect(limitsBody({ IRT: '0' })).toEqual([{ currency: 'IRT', amount: '0' }])
  })
  it('builds the request', () => {
    expect(termsBody({ tier: 't2', limits: { IRT: '3,000,000', USDT: '' }, commission: '', note: '  note ' }, 'raise', r.wallets)).toEqual({
      tier_id: 't2', credit_limits: [{ currency: 'IRT', amount: '3000000' }], commission_percent: null, note: 'note', reason: 'raise',
    })
    expect(termsBody({ ...blankTerms(), tier: 't1', commission: '۱۵' }, 'why').commission_percent).toBe(15)
    expect(termsBody({ ...blankTerms(), tier: 't1', commission: '0' }, 'why').commission_percent).toBe(0)
  })
})

describe('audit links', () => {
  const entry = (entity: string, entity_id = '') => ({ entity, entity_id, after: null, actor: null })
  it('opens the reseller, or the tiers', () => {
    expect(auditLink(entry('reseller', rid))).toEqual({ name: 'reseller', params: { id: rid } })
    expect(auditLink(entry('reseller', 'nope'))).toBeNull()
    expect(auditLink(entry('reseller_tier', rid))).toEqual({ name: 'resellers', query: { tab: 'tiers' } })
  })
})

// Every text the reseller screens ask for is in both catalogs.
describe('reseller texts', () => {
  const sources = import.meta.glob(['../pages/Reseller*.vue', '../components/resellers/*.vue'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>
  const has = (cat: Record<string, unknown>, key: string) =>
    typeof key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], cat) === 'string'
  it('exist in Persian and English', () => {
    const keys = new Set<string>()
    // whole keys only: t('currency.' + c) builds its key at run time
    for (const src of Object.values(sources)) for (const m of src.matchAll(/\bt\('([a-z_.]+)'\s*[,)]/g)) keys.add(m[1])
    expect(keys.size).toBeGreaterThan(60)
    for (const k of keys) {
      expect(has(fa, k), 'fa: ' + k).toBe(true)
      expect(has(en, k), 'en: ' + k).toBe(true)
    }
  })
  it('label the bot texts of the reseller panel', () => {
    expect(fa.texts.group.rs).toBeTruthy()
    expect(en.texts.group.rs).toBeTruthy()
    expect(fa.role.reseller).toBe('نماینده')
  })
})

// The pages, booted with the whole app against a fake core.

describe('reseller pages', () => {
  const cid = 'bbbbbbbb-1111-4222-8333-444444444444'
  const boss = { id: 'cccccccc-1111-4222-8333-444444444444', telegram_id: 1, username: 'boss' }
  const ownerPerms = ['overview.read', 'users.read', 'users.write', 'services.read', 'payments.read', 'ledger.read', 'plans.write', 'resellers.write', 'audit.read', 'wallet.adjust']
  const supportPerms = ['overview.read', 'users.read', 'services.read', 'services.extend', 'payments.read', 'tickets.write']
  const tiers = (): TierItem[] => [
    { id: 't1', name: 'Gold', discount_percent: 20, commission_percent: 10, prices: [{ plan_id: 'p1', price: '90000', currency: 'IRT' }], resellers: 1, version: 3 },
    { id: 't2', name: 'Silver', discount_percent: 10, commission_percent: 5, prices: [], resellers: 0, version: 1 },
  ]
  const detail = (over: Partial<ResellerDetail> = {}): ResellerDetail => ({ ...item(), note: 'pays on Fridays', version: 4, can_remove: false, ...over })
  const customer: UserItem = { id: cid, telegram_id: 777, username: 'cust', language: 'fa', role: 'user', status: 'active', created_at: t0 - 86_400, wallets: [], subscriptions: 2, orders: 3 }
  const commissionEntry: LedgerItem = {
    id: 'l1', user: { id: rid, telegram_id: 555, username: 'shop_ali' }, amount: { amount: 12000, currency: 'IRT' }, balance_after: { amount: -238000, currency: 'IRT' },
    kind: 'commission', ref_type: 'order', ref_id: 'dddddddd-1111-4222-8333-444444444444', created_at: t0,
  }
  const service: ServiceItem = {
    id: 'eeeeeeee-1111-4222-8333-444444444444', user: { id: rid, telegram_id: 555, username: 'shop_ali' }, plan: { id: 'p1', name: { fa: 'ماهانه' } },
    status: 'active', client_email: 'x', sub_link: '', expires_at: t0 + 10 * 86_400, traffic_total: null, traffic_used: 0, last_synced_at: null, created_at: t0, label: 'گوشی علی',
  }
  const page = <T>(items: T[]) => ({ items, total: items.length, page: 1, size: 25 })
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | Promise<Response> | undefined
  // stop the app a test booted (Vue and PrimeVue are shared between tests)
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(path: string, permissions: string[], answer: Answer = () => undefined) {
    unmount() // a test may boot again, as another viewer
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
          return json(200, { user: { ...boss, role: permissions.includes('resellers.write') ? 'owner' : 'support', language: 'fa' }, permissions, csrf: 'c', session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop' })
        }
        if (get && url === '/api/v1/reseller-tiers') return json(200, { items: tiers() })
        if (get && url === '/api/v1/plans') return json(200, { items: plans })
        if (get && url.startsWith('/api/v1/resellers?')) return json(200, page([item()]))
        if (get && url === `/api/v1/resellers/${rid}`) return json(200, { item: detail() })
        if (get && url.startsWith('/api/v1/users?')) return json(200, page([customer]))
        if (get && url.startsWith('/api/v1/ledger?')) return json(200, page([commissionEntry]))
        if (get && url.startsWith('/api/v1/services?')) return json(200, page([service]))
        if (get && url.startsWith('/api/v1/orders?')) return json(200, page([]))
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
    const input = el as HTMLInputElement | HTMLTextAreaElement
    input.value = value
    input.dispatchEvent(new Event('input'))
  }
  const posts = (calls: string[], method: string, path: string) =>
    calls.filter((c) => c.startsWith(`${method} /api/v1${path} `)).map((c) => JSON.parse(c.slice(`${method} /api/v1${path} `.length)) as Record<string, unknown>)
  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  async function pick(select: string, label: string) {
    ;(await found<HTMLElement>(select)).click()
    const option = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-select-option')].find((o) => o.textContent?.includes(label)) as HTMLElement | undefined
      expect(el, label).toBeDefined()
      return el!
    })
    option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
  }
  const dialog = () => document.querySelector('.p-dialog') as HTMLElement
  const confirmButton = () => [...dialog().querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement

  it('lists resellers with what they owe, filters them and opens one', async () => {
    const { calls, router } = await boot('/admin/resellers', ownerPerms)
    const body = await found('.resellers .p-datatable-tbody')
    await vi.waitFor(() => expect(body.textContent).toContain('@shop_ali'))
    expect(calls).toContain('GET /api/v1/resellers?page=1&size=25')
    expect(calls).toContain('GET /api/v1/reseller-tiers')
    const row = body.querySelector('tr') as HTMLElement
    // Toman first, the debt in words, never a bare minus
    const balance = row.querySelector('td:nth-child(3)') as HTMLElement
    expect(balance.querySelector('div')?.textContent).toBe('بدهی ۲۵۰٬۰۰۰ تومان')
    expect(balance.textContent).not.toMatch(/[-−]/)
    expect(row.querySelector('td:nth-child(4)')?.textContent).toContain('۱٬۰۰۰٬۰۰۰ تومان')
    expect(row.textContent).toContain('Gold')
    expect(row.textContent).toContain('۱۰٪')
    expect(row.querySelector('td:nth-child(5)')?.textContent).toBe('۱۲')
    expect(row.querySelector('td:nth-child(6)')?.textContent).toContain('۳۶٬۰۰۰ تومان')
    // the menu shows the section
    expect(document.querySelector('.sidebar')?.textContent).toContain(text('section.resellers.title'))

    type(document.querySelector('input[type=search]'), ' ۵۵۵ ')
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/resellers?q=555&page=1&size=25'))
    ;(document.querySelector('.debt-filter input') as HTMLInputElement).click()
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/resellers?q=555&in_debt=1&page=1&size=25'))
    await pick('.tier-filter', 'Silver')
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/resellers?q=555&tier=t2&in_debt=1&page=1&size=25'))

    ;(body.querySelector('tr td:nth-child(2)') as HTMLElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('reseller'))
    expect(router.currentRoute.value.params.id).toBe(rid)
  })

  it('keeps the pages from staff who may not manage resellers', async () => {
    const { router, calls } = await boot('/admin/resellers', supportPerms)
    expect(router.currentRoute.value.name).toBe('overview')
    expect(document.querySelector('.sidebar')?.textContent).not.toContain(text('section.resellers.title'))
    await router.push(`/resellers/${rid}`)
    expect(router.currentRoute.value.name).toBe('overview')
    expect(calls.some((c) => c.includes('/api/v1/resellers') || c.includes('/api/v1/reseller-tiers'))).toBe(false)
  })

  it('creates a tier with a fixed price, showing what resellers pay', async () => {
    let taken = true
    const { calls } = await boot('/admin/resellers?tab=tiers', ownerPerms, (url, init, b) => {
      if (url === '/api/v1/reseller-tiers' && init?.method === 'POST') {
        if (taken) {
          taken = false
          return json(400, { error: 'invalid', field: 'name', message: 'name: another tier has this name' })
        }
        return json(201, { item: { ...b, id: 't3', resellers: 0, version: 1 } })
      }
    })
    const body = await found('.tiers .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/plans'))
    expect(document.querySelector('button.add')).toBeNull() // Add reseller belongs to the first tab
    const [gold, silver] = [...body.querySelectorAll('tr')] as HTMLElement[]
    expect(gold.textContent).toContain('۲۰٪')
    expect((gold.querySelector('button.delete') as HTMLButtonElement).disabled).toBe(true) // in use
    expect((silver.querySelector('button.delete') as HTMLButtonElement).disabled).toBe(false)

    ;(document.querySelector('button.new-tier') as HTMLButtonElement).click()
    const d = await found('.tier-dialog')
    const save = () => d.querySelector('button.save') as HTMLButtonElement
    await vi.waitFor(() => expect(d.querySelector('input.fixed-p1')).not.toBeNull())
    expect(d.querySelector('input.fixed-p3')).toBeNull() // free trials are not priced
    expect(save().disabled).toBe(true)
    type(d.querySelector('input.name'), ' Bronze ')
    type(d.querySelector('input.discount'), '۱۵')
    type(d.querySelector('input.commission'), '5')
    // 120,000 less 15%: shown as the placeholder and as what they pay
    await vi.waitFor(() => expect((d.querySelector('input.fixed-p1') as HTMLInputElement).placeholder).toBe('102000'))
    expect(d.querySelector('.pays-p1')?.textContent).toBe('۱۰۲٬۰۰۰ تومان')
    type(d.querySelector('input.fixed-p2'), '2.1')
    await vi.waitFor(() => expect(d.querySelector('.pays-p2')?.textContent).toBe('۲٫۱ تتر'))
    // a price Toman cannot have keeps Save off
    type(d.querySelector('input.fixed-p1'), '1.5')
    await vi.waitFor(() => expect(save().disabled).toBe(true))
    type(d.querySelector('input.fixed-p1'), '')
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    save().click()
    // core's words go under the name
    await vi.waitFor(() => expect(d.querySelector('.field-error')?.textContent).toBe('name: another tier has this name'))
    expect(d.querySelector('input.name')?.classList.contains('p-invalid')).toBe(true)
    save().click()
    await vi.waitFor(() => expect(posts(calls, 'POST', '/reseller-tiers')).toHaveLength(2))
    expect(posts(calls, 'POST', '/reseller-tiers')[1]).toEqual({ name: 'Bronze', discount_percent: 15, commission_percent: 5, prices: [{ plan_id: 'p2', price: '2.1' }] })
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/reseller-tiers')).toHaveLength(2))
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('tier.saved'))
  })

  it('edits a tier with its version, and says when it changed meanwhile', async () => {
    // Core refuses any version but the current one, every time.
    let gold = tiers()[0]
    const { calls } = await boot('/admin/resellers?tab=tiers', ownerPerms, (url, init, b) => {
      if (url === '/api/v1/reseller-tiers' && (init?.method ?? 'GET') === 'GET') return json(200, { items: [gold, tiers()[1]] })
      if (url === '/api/v1/reseller-tiers/t1' && init?.method === 'PUT') {
        if (b.version !== gold.version) return json(409, { error: 'state', message: 'the tier was changed meanwhile' })
        gold = { ...gold, ...b, version: gold.version + 1 } as TierItem
        return json(200, { item: gold })
      }
    })
    const body = await found('.tiers .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    ;(body.querySelector('tr button.edit') as HTMLButtonElement).click()
    const d = await found('.tier-dialog')
    const save = () => d.querySelector('button.save') as HTMLButtonElement
    await vi.waitFor(() => expect((d.querySelector('input.fixed-p1') as HTMLInputElement).value).toBe('90000'))
    expect((d.querySelector('input.name') as HTMLInputElement).value).toBe('Gold')
    expect(d.querySelector('.pays-p1')?.textContent).toBe('۹۰٬۰۰۰ تومان')
    type(d.querySelector('input.discount'), '30')
    type(d.querySelector('input.fixed-p1'), '')
    await vi.waitFor(() => expect(d.querySelector('.pays-p1')?.textContent).toBe('۸۴٬۰۰۰ تومان'))

    // Another admin saves Gold first.
    gold = { ...gold, discount_percent: 25, version: 4 }
    save().click()
    await vi.waitFor(() => expect(d.textContent).toContain(text('tier.stale')))
    expect(d.textContent).toContain('the tier was changed meanwhile')
    expect(posts(calls, 'PUT', '/reseller-tiers/t1')[0]).toEqual({ name: 'Gold', discount_percent: 30, commission_percent: 10, prices: [], version: 3 })
    // the page reads the tiers again; this form is not sent again
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/reseller-tiers')).toHaveLength(2))
    expect(save().disabled).toBe(true)
    save().click()
    await new Promise((r) => setTimeout(r, 30))
    expect(posts(calls, 'PUT', '/reseller-tiers/t1')).toHaveLength(1)

    // Closed and opened again, it shows the new values and names the new version.
    ;(d.querySelector('.buttons button') as HTMLButtonElement).click() // Cancel
    await vi.waitFor(() => expect(document.querySelector('.tier-dialog')).toBeNull())
    ;(body.querySelector('tr button.edit') as HTMLButtonElement).click()
    const d2 = await found('.tier-dialog')
    await vi.waitFor(() => expect((d2.querySelector('input.discount') as HTMLInputElement).value).toBe('25'))
    expect(d2.textContent).not.toContain(text('tier.stale'))
    type(d2.querySelector('input.commission'), '12')
    await vi.waitFor(() => expect((d2.querySelector('button.save') as HTMLButtonElement).disabled).toBe(false))
    ;(d2.querySelector('button.save') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(posts(calls, 'PUT', '/reseller-tiers/t1')).toHaveLength(2))
    expect(posts(calls, 'PUT', '/reseller-tiers/t1')[1]).toEqual({
      name: 'Gold', discount_percent: 25, commission_percent: 12, prices: [{ plan_id: 'p1', price: '90000' }], version: 4,
    })
    await vi.waitFor(() => expect(document.querySelector('.tier-dialog')).toBeNull())
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('tier.saved'))
  })

  it('saves a tier only once the plans are in, so no fixed price is lost', async () => {
    let release!: () => void
    const slow = new Promise<void>((r) => (release = r))
    const { calls } = await boot('/admin/resellers?tab=tiers', ownerPerms, (url) => {
      if (url === '/api/v1/plans') return slow.then(() => json(200, { items: plans }))
      if (url === '/api/v1/reseller-tiers/t1') return json(200, { item: tiers()[0] })
    })
    const body = await found('.tiers .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    ;(body.querySelector('tr button.edit') as HTMLButtonElement).click()
    const d = await found('.tier-dialog')
    await vi.waitFor(() => expect((d.querySelector('input.name') as HTMLInputElement).value).toBe('Gold'))
    expect((d.querySelector('button.save') as HTMLButtonElement).disabled).toBe(true)
    release()
    await vi.waitFor(() => expect((d.querySelector('input.fixed-p1') as HTMLInputElement | null)?.value).toBe('90000'))
    await vi.waitFor(() => expect((d.querySelector('button.save') as HTMLButtonElement).disabled).toBe(false))
    ;(d.querySelector('button.save') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(posts(calls, 'PUT', '/reseller-tiers/t1')).toHaveLength(1))
    expect(posts(calls, 'PUT', '/reseller-tiers/t1')[0].prices).toEqual([{ plan_id: 'p1', price: '90000' }])
  })

  it('deletes a tier only after a confirm, and explains a tier in use', async () => {
    let inUse = false
    const { calls } = await boot('/admin/resellers?tab=tiers', ownerPerms, (url, init) => {
      if (url === '/api/v1/reseller-tiers/t2' && init?.method === 'DELETE') {
        return inUse ? json(409, { error: 'state', message: 'resellers are on this tier' }) : new Response(null, { status: 204 })
      }
    })
    const body = await found('.tiers .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    const del = () => body.querySelectorAll('tr')[1].querySelector('button.delete') as HTMLButtonElement
    del().click()
    const accept = await found<HTMLButtonElement>('.p-confirmdialog-accept-button')
    expect(document.querySelector('.p-confirmdialog')?.textContent).toContain('Silver')
    expect(calls.some((c) => c.startsWith('DELETE'))).toBe(false)
    accept.click()
    await vi.waitFor(() => expect(calls).toContain('DELETE /api/v1/reseller-tiers/t2'))
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('tier.deleted')))

    // Someone moved a reseller onto it meanwhile: core refuses.
    inUse = true
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    del().click()
    ;(await found<HTMLButtonElement>('.p-confirmdialog-accept-button')).click()
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('tier.in_use')))
  })

  it('shows a reseller’s numbers, and records a payment once with its key', async () => {
    let balance = -250000
    let failNext = true
    const { calls } = await boot(`/admin/resellers/${rid}`, ownerPerms, (url, init, b) => {
      if (url === `/api/v1/resellers/${rid}` && (init?.method ?? 'GET') === 'GET') {
        return json(200, { item: detail({ balances: [{ currency: 'IRT', balance, credit_limit: 1_000_000 }], can_remove: balance >= 0 }) })
      }
      if (url === `/api/v1/resellers/${rid}/payments`) {
        if (failNext) {
          failNext = false
          return Promise.reject(new TypeError('Failed to fetch')) // core got it, the answer was lost
        }
        balance += Number(b.amount)
        const item = detail({ balances: [{ currency: 'IRT', balance, credit_limit: 1_000_000 }], can_remove: balance >= 0 })
        return json(200, { item, entry: { ...commissionEntry, kind: 'settlement', ref_type: 'admin', amount: { amount: Number(b.amount), currency: 'IRT' }, balance_after: { amount: balance, currency: 'IRT' } } })
      }
    })
    const stats = await found('.stats')
    await vi.waitFor(() => expect(stats.querySelector('.balance')?.textContent).toBe('بدهی ۲۵۰٬۰۰۰ تومان'))
    expect(stats.querySelector('.limit')?.textContent).toBe('۱٬۰۰۰٬۰۰۰ تومان')
    expect(stats.querySelector('.available')?.textContent).toBe('۷۵۰٬۰۰۰ تومان')
    expect(stats.querySelector('.customers')?.textContent).toBe('۱۲')
    expect(stats.querySelector('.earned')?.textContent).toBe('۳۶٬۰۰۰ تومان')
    const facts = document.querySelector('.facts') as HTMLElement
    expect(facts.textContent).toContain('۲۰٪')
    expect(facts.textContent).toContain(text('reseller.commission_from_tier'))
    expect(facts.textContent).toContain('pays on Fridays')
    expect(document.querySelector('.app-page-title')?.textContent).toContain('@shop_ali')
    // in debt: Remove is off, and the page says why
    expect((document.querySelector('button.remove') as HTMLButtonElement).disabled).toBe(true)
    expect(document.querySelector('#remove-hint')?.textContent).toBe(text('reseller.remove_blocked'))
    // the first tab loads at once, the others when shown; a service shows the name the reseller gave it
    expect(calls).toContain(`GET /api/v1/services?user=${rid}&page=1&size=10`)
    await vi.waitFor(() => expect(document.querySelector('.p-tabpanels')?.textContent).toContain('گوشی علی'))
    expect(calls.some((c) => c.startsWith('GET /api/v1/users?'))).toBe(false)

    ;(document.querySelector('button.pay') as HTMLButtonElement).click()
    const d = await found('.p-dialog')
    expect(d.textContent).toContain('بدهی ۲۵۰٬۰۰۰ تومان')
    expect(confirmButton().disabled).toBe(true)
    type(d.querySelector('input.amount'), '۱۰۰٬۰۰۰')
    type(d.querySelector('textarea'), 'cash at the shop')
    await vi.waitFor(() => expect(d.querySelector('.after')?.textContent).toBe('بدهی ۱۵۰٬۰۰۰ تومان'))
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await vi.waitFor(() => expect(d.textContent).toContain(text('error.network')))
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await vi.waitFor(() => expect(posts(calls, 'POST', `/resellers/${rid}/payments`)).toHaveLength(2))
    const [first, retry] = posts(calls, 'POST', `/resellers/${rid}/payments`)
    expect(first).toEqual({ amount: '100000', currency: 'IRT', reason: 'cash at the shop', key: first.key })
    expect(first.key).toMatch(/^[0-9a-f]{32}$/)
    expect(retry.key).toBe(first.key)
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('reseller.payment_done').replace('{balance}', 'بدهی ۱۵۰٬۰۰۰ تومان')))
    await vi.waitFor(() => expect(stats.querySelector('.balance')?.textContent).toBe('بدهی ۱۵۰٬۰۰۰ تومان'))

    // a new payment is a new request
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    ;(document.querySelector('button.pay') as HTMLButtonElement).click()
    const d2 = await found('.p-dialog')
    type(d2.querySelector('input.amount'), '150000')
    type(d2.querySelector('textarea'), 'the rest')
    await vi.waitFor(() => expect(d2.querySelector('.after')?.textContent).toBe('۰ تومان'))
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await vi.waitFor(() => expect(posts(calls, 'POST', `/resellers/${rid}/payments`)).toHaveLength(3))
    expect(posts(calls, 'POST', `/resellers/${rid}/payments`)[2].key).not.toBe(first.key)
    // nothing owed now: Remove is on
    await vi.waitFor(() => expect((document.querySelector('button.remove') as HTMLButtonElement).disabled).toBe(false))
    expect(document.querySelector('#remove-hint')).toBeNull()
  })

  it('changes terms with the version, never below the debt', async () => {
    // Core refuses any version but the current one, every time.
    let version = 4
    let refuseLimit = true
    const { calls } = await boot(`/admin/resellers/${rid}`, ownerPerms, (url, init, b) => {
      if (url === `/api/v1/resellers/${rid}` && (init?.method ?? 'GET') === 'GET') return json(200, { item: detail({ version }) })
      if (url === `/api/v1/resellers/${rid}` && init?.method === 'PUT') {
        if (b.version !== version) return json(409, { error: 'state', message: 'the reseller was changed meanwhile' })
        if (refuseLimit) {
          refuseLimit = false
          return json(400, { error: 'invalid', field: 'credit_limit.IRT', message: 'credit_limit.IRT: at most 1000000000' })
        }
        version++
        return json(200, { item: detail({ version, commission_override: 15, commission_percent: 15, note: 'new note' }) })
      }
    })
    await vi.waitFor(() => expect(document.querySelector('.stats .balance')?.textContent).toBe('بدهی ۲۵۰٬۰۰۰ تومان'))
    ;(document.querySelector('button.terms') as HTMLButtonElement).click()
    const d = await found('.p-dialog')
    await vi.waitFor(() => expect((d.querySelector('input.limit-IRT') as HTMLInputElement).value).toBe('1000000'))
    expect((d.querySelector('input.limit-USDT') as HTMLInputElement).value).toBe('')
    expect((d.querySelector('textarea.note') as HTMLTextAreaElement).value).toBe('pays on Fridays')
    // the tier's commission is the default
    await vi.waitFor(() => expect((d.querySelector('input.commission') as HTMLInputElement).placeholder).toBe('10'))
    expect(d.querySelector('.tier .p-select-label')?.textContent).toContain('Gold')
    const reason = d.querySelector('textarea:not(.note)') as HTMLTextAreaElement
    type(reason, 'bigger shop now')

    // below what they owe: refused here already
    type(d.querySelector('input.limit-IRT'), '200000')
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(true))
    expect(d.textContent).toContain(text('reseller.limit_at_least').replace('{amount}', '۲۵۰٬۰۰۰ تومان'))
    type(d.querySelector('input.limit-IRT'), '۲٬۰۰۰٬۰۰۰٬۰۰۰')
    type(d.querySelector('input.commission'), '۱۵')
    type(d.querySelector('textarea.note'), ' new note ')
    await pick('.p-dialog .tier', 'Silver')
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    // core's own words go under the field
    await vi.waitFor(() => expect(d.textContent).toContain('credit_limit.IRT: at most 1000000000'))
    expect(d.querySelector('input.limit-IRT')?.classList.contains('p-invalid')).toBe(true)
    expect(posts(calls, 'PUT', `/resellers/${rid}`)[0]).toEqual({
      version: 4, tier_id: 't2', credit_limits: [{ currency: 'IRT', amount: '2000000000' }], commission_percent: 15, note: 'new note', reason: 'bigger shop now',
    })

    // Someone else changed them: the page reads them again, this form stays refused.
    version = 5
    type(d.querySelector('input.limit-IRT'), '2000000')
    confirmButton().click()
    await vi.waitFor(() => expect(d.textContent).toContain(text('reseller.terms_stale')))
    await vi.waitFor(() => expect(calls.filter((c) => c === `GET /api/v1/resellers/${rid}`)).toHaveLength(2))
    expect(posts(calls, 'PUT', `/resellers/${rid}`)[1]).toMatchObject({ version: 4 })
    // The old form never goes out under the version just read, even edited.
    type(d.querySelector('input.commission'), '20')
    await new Promise((r) => setTimeout(r, 30))
    expect(confirmButton().disabled).toBe(true)
    confirmButton().click()
    await new Promise((r) => setTimeout(r, 30))
    expect(posts(calls, 'PUT', `/resellers/${rid}`)).toHaveLength(2)
    expect(d.textContent).toContain(text('reseller.terms_stale'))

    // Opened again, it names the new version.
    ;(d.querySelector('.p-dialog-footer button') as HTMLButtonElement).click() // Cancel
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    ;(document.querySelector('button.terms') as HTMLButtonElement).click()
    const d2 = await found('.p-dialog')
    type(d2.querySelector('input.commission'), '15')
    type(d2.querySelector('textarea:not(.note)'), 'again')
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await vi.waitFor(() => expect(posts(calls, 'PUT', `/resellers/${rid}`)).toHaveLength(3))
    expect(posts(calls, 'PUT', `/resellers/${rid}`)[2]).toMatchObject({ version: 5, tier_id: 't1', commission_percent: 15 })
    await vi.waitFor(() => expect(document.querySelector('.facts')?.textContent).toContain(text('reseller.commission_own')))
    expect(document.querySelector('.facts')?.textContent).toContain('new note')
  })

  it('removes a reseller who owes nothing, after a reason', async () => {
    let refused = true
    const { calls, router } = await boot(`/admin/resellers/${rid}`, ownerPerms, (url, init) => {
      if (url === `/api/v1/resellers/${rid}` && (init?.method ?? 'GET') === 'GET') return json(200, { item: detail({ balances: [{ currency: 'IRT', balance: 0, credit_limit: 0 }], can_remove: true }) })
      if (url === `/api/v1/resellers/${rid}/remove`) {
        if (refused) {
          refused = false
          return json(409, { error: 'state', message: 'the reseller owes money' })
        }
        return json(200, { user_id: rid, role: 'user' })
      }
      if (url === `/api/v1/users/${rid}`) {
        return json(200, { user: { ...customer, id: rid, username: 'shop_ali' }, counts: { subscriptions: 0, orders: 0 }, referral: { referred_by: null, invited: 12, rewarded: 0, earned: [] }, can_ban: true })
      }
    })
    const remove = await found<HTMLButtonElement>('button.remove')
    await vi.waitFor(() => expect(remove.disabled).toBe(false))
    remove.click()
    const d = await found('.p-dialog')
    expect(d.textContent).toContain(text('reseller.remove_intro'))
    expect(confirmButton().disabled).toBe(true) // a reason first
    type(d.querySelector('textarea'), 'stopped selling')
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await vi.waitFor(() => expect(d.textContent).toContain('the reseller owes money'))
    confirmButton().click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('user'))
    expect(router.currentRoute.value.params.id).toBe(rid)
    expect(posts(calls, 'POST', `/resellers/${rid}/remove`)).toEqual([{ reason: 'stopped selling' }, { reason: 'stopped selling' }])
  })

  it('shows their customers and statement when those tabs open', async () => {
    const { calls, router } = await boot(`/admin/resellers/${rid}?tab=customers`, ownerPerms)
    await vi.waitFor(() => expect(calls).toContain(`GET /api/v1/users?referred_by=${rid}&page=1&size=10`))
    const panel = () => document.querySelector('.p-tabpanels') as HTMLElement
    await vi.waitFor(() => expect(panel().textContent).toContain('@cust'))
    expect(document.querySelector('[role=tab][aria-selected=true]')?.textContent).toContain('۱۲')
    expect(calls.some((c) => c.startsWith('GET /api/v1/services?'))).toBe(false)

    ;([...document.querySelectorAll('[role=tab]')].find((tb) => tb.textContent?.includes(text('reseller.statement'))) as HTMLElement).click()
    await vi.waitFor(() => expect(calls).toContain(`GET /api/v1/ledger?user=${rid}&page=1&size=10`))
    await vi.waitFor(() => expect(router.currentRoute.value.query.tab).toBe('ledger'))
    await vi.waitFor(() => expect(panel().textContent).toContain(text('ledger.kind.commission')))
    expect(panel().textContent).toContain('بدهی ۲۳۸٬۰۰۰ تومان') // the balance after, as debt
    expect(panel().querySelector('a[download]')?.getAttribute('href')).toBe(`/api/v1/ledger.csv?user=${rid}`)
    // a commission links to the customer's order
    expect(panel().querySelector('a[href^="/admin/payments/orders/"]')).not.toBeNull()

    ;([...document.querySelectorAll('[role=tab]')].find((tb) => tb.textContent?.includes(text('reseller.customers'))) as HTMLElement).click()
    await vi.waitFor(() => expect(panel().textContent).toContain('@cust'))
    ;([...panel().querySelectorAll('.p-datatable-tbody tr td')][1] as HTMLElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('user'))
    expect(router.currentRoute.value.params.id).toBe(cid)
  })

  const userDetail = (over: Partial<UserItem> = {}) => ({
    user: { ...customer, ...over }, counts: { subscriptions: 2, orders: 3 }, referral: { referred_by: null, invited: 0, rewarded: 0, earned: [] }, can_ban: true,
  })

  it('makes an active customer a reseller from their page', async () => {
    const { calls, router } = await boot(`/admin/users/${cid}`, ownerPerms, (url, init) => {
      if (url === `/api/v1/users/${cid}`) return json(200, userDetail())
      if (url === '/api/v1/resellers' && init?.method === 'POST') return json(201, { item: { ...detail(), user: { id: cid, telegram_id: 777, username: 'cust', status: 'active' } } })
    })
    const make = await found<HTMLButtonElement>('button.make-reseller')
    expect(document.querySelector('a.reseller-page')).toBeNull()
    make.click()
    const d = await found('.p-dialog')
    expect(d.textContent).toContain('⁨@cust⁩') // the name stays whole in the Persian sentence
    expect(d.querySelector('input.search')).toBeNull() // the customer is known
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/reseller-tiers'))
    type(d.querySelector('textarea'), 'trusted partner')
    expect(confirmButton().disabled).toBe(true) // no tier yet (two to choose from)
    await pick('.p-dialog .tier', 'Gold')
    type(d.querySelector('input.limit'), '۵٬۰۰۰٬۰۰۰')
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    // a limit Toman cannot have keeps it off
    type(d.querySelector('input.limit'), '5.5')
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(true))
    type(d.querySelector('input.limit'), '۵٬۰۰۰٬۰۰۰')
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('reseller'))
    expect(router.currentRoute.value.params.id).toBe(cid)
    expect(posts(calls, 'POST', '/resellers')).toEqual([
      { user_id: cid, tier_id: 't1', credit_limits: [{ currency: 'IRT', amount: '5000000' }], commission_percent: null, note: '', reason: 'trusted partner' },
    ])
  })

  it('offers Make reseller only for active customers, to those who may', async () => {
    let user: Partial<UserItem> = { status: 'banned' }
    const answer: Answer = (url) => (url === `/api/v1/users/${cid}` ? json(200, userDetail(user)) : undefined)
    await boot(`/admin/users/${cid}`, ownerPerms, answer)
    await found('.app-facts')
    expect(document.querySelector('button.make-reseller')).toBeNull()

    // staff: their own actions in the audit log
    user = { role: 'support' }
    await boot(`/admin/users/${cid}`, ownerPerms, answer)
    await found('.app-facts')
    expect(document.querySelector('button.make-reseller')).toBeNull()
    expect(document.querySelector(`a[href="/admin/audit?actor=${cid}"]`)).not.toBeNull()

    // support sees the customer, not the action
    user = {}
    await boot(`/admin/users/${cid}`, supportPerms, answer)
    await found('.app-facts')
    expect(document.querySelector('button.make-reseller')).toBeNull()

    // a reseller's page links to their reseller page, and says the role
    user = { role: 'reseller', wallets: [{ amount: -250000, currency: 'IRT' }] }
    await boot(`/admin/users/${cid}`, ownerPerms, answer)
    const link = await found<HTMLAnchorElement>('a.reseller-page')
    expect(link.getAttribute('href')).toBe(`/admin/resellers/${cid}`)
    expect(document.querySelector('button.make-reseller')).toBeNull()
    // resellers act in the bot, never as the audit log's actor
    expect(document.querySelector(`a[href="/admin/audit?entity_id=${cid}"]`)).not.toBeNull()
    expect(document.querySelector(`a[href="/admin/audit?actor=${cid}"]`)).toBeNull()
    expect(document.querySelector('.app-facts')?.textContent).toContain(text('role.reseller'))
    expect(document.body.textContent).toContain('بدهی ۲۵۰٬۰۰۰ تومان')
  })

  it('adds a reseller from the list after finding the customer', async () => {
    const { calls, router } = await boot('/admin/resellers', ownerPerms, (url, init) => {
      if (url.startsWith('/api/v1/users?')) return json(200, page([customer, { ...customer, id: 'x', username: 'staffer', role: 'admin' }]))
      if (url === '/api/v1/reseller-tiers') return json(200, { items: tiers().slice(1) })
      if (url === '/api/v1/resellers' && init?.method === 'POST') return json(201, { item: detail() })
    })
    ;(await found<HTMLButtonElement>('button.add')).click()
    const d = await found('.p-dialog')
    // the only tier is chosen already
    await vi.waitFor(() => expect(d.querySelector('.tier .p-select-label')?.textContent).toContain('Silver'))
    type(d.querySelector('textarea'), 'new partner')
    expect(confirmButton().disabled).toBe(true) // nobody picked yet
    type(d.querySelector('input.search'), '@cust')
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/users?q=%40cust&role=user&status=active&size=10'))
    const option = await vi.waitFor(() => {
      const el = [...d.querySelectorAll('.p-listbox-option')].find((o) => o.textContent?.includes('@cust')) as HTMLElement | undefined
      expect(el).toBeDefined()
      return el!
    })
    expect(d.querySelector('.p-listbox')?.textContent).not.toContain('staffer') // staff cannot be resellers
    option.click()
    await vi.waitFor(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('reseller'))
    expect(posts(calls, 'POST', '/resellers')[0]).toEqual({ user_id: cid, tier_id: 't2', credit_limits: [], commission_percent: null, note: '', reason: 'new partner' })
  })

  it('asks for a tier before anyone can become a reseller', async () => {
    await boot('/admin/resellers', ownerPerms, (url) => (url === '/api/v1/reseller-tiers' ? json(200, { items: [] }) : undefined))
    ;(await found<HTMLButtonElement>('button.add')).click()
    const d = await found('.p-dialog')
    await vi.waitFor(() => expect(d.textContent).toContain(text('reseller.no_tiers')))
    expect(d.querySelector('.p-message a')?.getAttribute('href')).toBe('/admin/resellers?tab=tiers')
  })

  it('shows what resellers owe on the overview, for those who manage them', async () => {
    const overview = (resellers?: { count: number; debt: { amount: number; currency: string }[] }) => ({
      users: { total: 1, new_24h: 0, new_7d: 0 }, services: { active: 0, expiring: 0, ended: 0 }, orders: { paid_24h: 0, provision_failed: 0 },
      revenue: { day: [], month: [] }, pending_payments: 0, panel: { healthy: true, detail: '' }, recent_orders: [], resellers,
    })
    let answer = overview({ count: 3, debt: [{ amount: 250000, currency: 'IRT' }] })
    const core: Answer = (url) => (url === '/api/v1/overview' ? json(200, answer) : undefined)
    const { calls, router } = await boot('/admin/', ownerPerms, core)
    const card = await found<HTMLAnchorElement>('a.resellers-card')
    expect(card.getAttribute('href')).toBe('/admin/resellers?in_debt=1')
    expect(card.textContent).toContain(text('overview.reseller_debt'))
    expect(card.textContent).toContain('۲۵۰٬۰۰۰ تومان')
    expect(card.textContent).toContain('۳')
    card.click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('resellers'))
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/resellers?in_debt=1&page=1&size=25'))
    expect((document.querySelector('.debt-filter input') as HTMLInputElement).checked).toBe(true)

    // nobody owes: the card says so and opens the whole list
    answer = overview({ count: 2, debt: [] })
    await boot('/admin/', ownerPerms, core)
    const quiet = await found<HTMLAnchorElement>('a.resellers-card')
    expect(quiet.getAttribute('href')).toBe('/admin/resellers')
    expect(quiet.textContent).toContain(text('overview.reseller_no_debt'))

    // no resellers yet, or not for this viewer: no card
    answer = overview({ count: 0, debt: [] })
    await boot('/admin/', ownerPerms, core)
    await found('.recent')
    await vi.waitFor(() => expect(document.querySelector('.stat .big')).not.toBeNull())
    expect(document.querySelector('a.resellers-card')).toBeNull()
    answer = overview({ count: 3, debt: [{ amount: 1, currency: 'IRT' }] })
    await boot('/admin/', supportPerms, core)
    await vi.waitFor(() => expect(document.querySelector('.stat .big')).not.toBeNull())
    expect(document.querySelector('a.resellers-card')).toBeNull()
  })

  it('filters the ledger and the audit log by the new kinds and items', async () => {
    const { calls } = await boot('/admin/ledger', ownerPerms)
    await pick('.app-toolbar .p-select', text('ledger.kind.settlement'))
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/ledger?kind=settlement&page=1&size=25'))

    const audit = await boot('/admin/audit', ownerPerms, (url) => {
      if (url.startsWith('/api/v1/audit?')) {
        return json(200, page([{ id: 'a1', actor: boss, actor_role: 'owner', action: 'reseller.payment', entity: 'reseller', entity_id: rid, before: null, after: { amount: '100000' }, reason: 'cash', ip: '', source: 'dashboard', created_at: t0 }]))
      }
      if (url === '/api/v1/audit/actions') return json(200, { items: [{ action: 'reseller.payment', count: 1 }] })
    })
    const link = await found<HTMLAnchorElement>('.p-datatable-tbody a[href^="/admin/resellers/"]')
    expect(link.getAttribute('href')).toBe(`/admin/resellers/${rid}`)
    expect(link.textContent).toContain(text('audit.entity.reseller'))
    expect(document.querySelector('.p-datatable-tbody')?.textContent).toContain(text('audit.action.reseller.payment'))
    await pick('.app-toolbar .p-select:nth-of-type(2)', text('audit.entity.reseller_tier'))
    await vi.waitFor(() => expect(audit.calls).toContain('GET /api/v1/audit?entity=reseller_tier&page=1&size=25'))
  })

  it('points to the customer page when someone is no longer a reseller', async () => {
    await boot(`/admin/resellers/${rid}`, ownerPerms, (url) => (url === `/api/v1/resellers/${rid}` ? json(404, { error: 'not_found', message: 'no such item' }) : undefined))
    const msg = await found('.p-message-error')
    await vi.waitFor(() => expect(msg.textContent).toContain(text('reseller.not_reseller')))
    expect(msg.querySelector('a')?.getAttribute('href')).toBe(`/admin/users/${rid}`)
    expect(document.querySelector('.stats')).toBeNull()
    expect(document.querySelector('button.pay')).toBeNull()
  })
})
