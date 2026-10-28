import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { percentBP as formatPercent } from '../format'
import { canonical, changes, ltr, normalize, percentBP, problems, validate } from '../settings'
import type { OrderItem, SettingItem } from '../types'

// The cashback on the Settings page (its card), on the order page (what the order earned, and what a refund took back) and in the reports and
// the wallet history (their names), against a fake core, in English and Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function item(key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem {
  return { key, group: 'cashback', kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra }
}
const cashbackItems = (): SettingItem[] => [
  item('cashback.enabled', 'bool', { default: 'false' }),
  item('cashback.percent', 'text'),
  item('cashback.max_per_order', 'int', { min: 0, max: 100_000_000_000 }),
  item('cashback.min_order', 'int', { min: 0, max: 100_000_000_000 }),
  item('cashback.renewals', 'bool', { default: 'true' }),
]

const orderItem = (over: Partial<OrderItem> = {}): OrderItem => ({
  id: '00000000-0000-7000-8000-0000000abcde', user: { id: 'u1', telegram_id: 1001, username: 'ali' }, plan: { id: 'p1', name: { fa: 'ماهانه', en: 'Monthly' } },
  type: 'new', status: 'active', amount: { amount: 10000, currency: 'IRT' }, discount: null, subscription_id: null, created_at: 1_790_000_000, updated_at: 1_790_000_100,
  attempts: 0, last_error: '', next_attempt_at: null, staff: '', extend: null, refund: null, ...over,
})

describe('the cashback percent', () => {
  it('is read as core reads it', () => {
    for (const [typed, bp] of [['5', 500], ['2.5', 250], ['2,5', 250], ['۲٫۵', 250], ['٢،٥', 250], ['0.05', 5], ['20', 2000], ['7%', 700], [' 0.5 ', 50], ['.5', 50], ['5.', 500], ['12.34', 1234]] as const) {
      expect(percentBP(typed), typed).toBe(bp)
    }
    for (const bad of ['', '.', '0', '0.00', '0.001', '20.01', '21', '100', '-1', 'abc', '1.2.3', '1e1', '5 5']) expect(percentBP(bad), bad).toBeNull()
  })

  it('is written the short way, and checked as the card sends it', () => {
    const percent = item('cashback.percent', 'text')
    expect(normalize(percent, ' ۲٫۵۰ ')).toBe('2.5')
    expect(normalize(percent, '0.5%')).toBe('0.5')
    expect(normalize(percent, '12')).toBe('12')
    expect(normalize(percent, 'lots')).toBe('lots')
    expect(validate(percent, '2.5')).toBeNull()
    expect(validate(percent, 'lots')).toEqual({ code: 'percent' })
    expect(validate(percent, '20.5')).toEqual({ code: 'percent' })
    expect(validate(percent, '')).toBeNull() // empty is "back to the default": no cashback
    expect(ltr(percent)).toBe(true)
    // only the key that changed is sent, normalized; a bad one is a problem of that key alone
    const items = cashbackItems()
    expect(changes(items, { 'cashback.percent': '2.50', 'cashback.min_order': '' })).toEqual({ 'cashback.percent': '2.5' })
    expect(problems(items, { 'cashback.percent': '0' })).toEqual({ 'cashback.percent': { code: 'percent' } })
    expect(problems(items, { 'cashback.percent': '3', 'cashback.max_per_order': '1000' })).toEqual({})
    expect(canonical(items[1], '')).toBe('')
  })

  it('is written in the language of the page', () => {
    expect(formatPercent(250, 'en')).toBe('2.5%')
    expect(formatPercent(250, 'fa')).toBe('۲٫۵٪')
    expect(formatPercent(2000, 'en')).toBe('20%')
    expect(formatPercent(5, 'en')).toBe('0.05%')
  })
})

describe('the texts of the cashback', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = [
    'settings.group.cashback.title', 'settings.group.cashback.desc', 'settings.group.cashback.note', 'settings.invalid.percent',
    'settings.field.cashback.enabled.label', 'settings.field.cashback.enabled.help', 'settings.field.cashback.percent.label',
    'settings.field.cashback.percent.help', 'settings.field.cashback.max_per_order.label', 'settings.field.cashback.max_per_order.help',
    'settings.field.cashback.min_order.label', 'settings.field.cashback.min_order.help', 'settings.field.cashback.renewals.label',
    'settings.field.cashback.renewals.help', 'reports.kpi.cashback', 'order.cashback', 'order.cashback_line', 'order.cashback_capped',
    'order.cashback_reversed', 'audit.action.order.cashback_reversal', 'ledger.kind.cashback', 'ledger.kind.cashback_reversal',
    'ledger.kind.target_bonus', 'ledger.kind.wheel_prize',
  ]

  it('are in both languages, with the same values to fill in', () => {
    const names = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
    for (const k of keys) {
      const a = get(en, k)
      const b = get(fa, k)
      expect(typeof a, `en ${k}`).toBe('string')
      expect(typeof b, `fa ${k}`).toBe('string')
      expect(names(b as string), k).toEqual(names(a as string))
    }
  })
})

describe('the pages', () => {
  const me = (permissions: string[]) => ({
    user: { id: 'u0', telegram_id: 1, username: 'boss', role: 'owner', language: 'fa' },
    permissions,
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'Shop',
    branding: { color: '#0f766e', logo: null, currency: { fa: '', en: 'T' } },
  })

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

  /** boot starts the app at path against a fake core (answer returns a Response, or nothing for the common calls). */
  async function boot(path: string, lang: 'en' | 'fa', answer: (url: string, init?: RequestInit) => Response | undefined, calls: string[] = []) {
    vi.resetModules()
    localStorage.setItem('bobres.lang', lang)
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push(`${init?.method ?? 'GET'} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
        const own = answer(url, init)
        if (own) return own
        if (url === '/api/v1/me') return json(200, me(['overview.read', 'payments.read', 'payments.review', 'settings.write', 'discounts.write', 'audit.read']))
        if (url === '/api/v1/branding') return json(200, { name: 'Shop', support: '', color: '#0f766e', terms_url: '', privacy_url: '', currency: { fa: '', en: 'T' }, logo: null, defaults: { name: 'BOBRES', color: '#14b8a6', currency: { fa: 'تومان', en: 'Toman' } } })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
  }
  const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()

  const orderPage = (o: OrderItem) => (url: string): Response | undefined =>
    url === `/api/v1/orders/${o.id}` ? json(200, { order: o, payments: [], payments_error: '', can: { retry: false, refund: false } }) : undefined

  it('show what an order earned on its page, and what a refund took back', async () => {
    const earned = orderItem({ cashback: { amount: { amount: 250, currency: 'IRT' }, percent_bp: 250, capped: false, at: 1_790_000_100 } })
    await boot(`/admin/payments/orders/${earned.id}`, 'en', orderPage(earned))
    await vi.waitFor(() => expect(document.querySelector('dd.cashback')).not.toBeNull())
    expect(text(document.querySelector('dd.cashback'))).toBe('250 T · 2.5% of 10,000 T')
    expect(text(document.querySelector('.app-facts'))).toContain('Cashback')
  })

  it('says when it was cut to the maximum and when a refund took it back', async () => {
    const o = orderItem({
      cashback: { amount: { amount: 1000, currency: 'IRT' }, percent_bp: 1000, capped: true, at: 1_790_000_100, reversed: { amount: 1000, currency: 'IRT' }, reversed_at: 1_790_000_900 },
    })
    await boot(`/admin/payments/orders/${o.id}`, 'en', orderPage(o))
    await vi.waitFor(() => expect(document.querySelector('dd.cashback')).not.toBeNull())
    const t = text(document.querySelector('dd.cashback'))
    expect(t).toContain('1,000 T · 10% of 10,000 T')
    expect(t).toContain('cut to the most one order earns')
    expect(t).toContain('1,000 T was taken back with the refund')
  })

  it('show it in Persian, and show nothing for an order that earned none', async () => {
    const o = orderItem({ cashback: { amount: { amount: 250, currency: 'IRT' }, percent_bp: 250, capped: false, at: 1_790_000_100 } })
    await boot(`/admin/payments/orders/${o.id}`, 'fa', orderPage(o))
    await vi.waitFor(() => expect(document.querySelector('dd.cashback')).not.toBeNull())
    expect(text(document.querySelector('dd.cashback'))).toBe('۲۵۰ تومان · ۲٫۵٪ از ۱۰٬۰۰۰ تومان')
    vi.unstubAllGlobals()
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    const none = orderItem()
    await boot(`/admin/payments/orders/${none.id}`, 'en', orderPage(none))
    await vi.waitFor(() => expect(document.querySelector('.app-facts')).not.toBeNull())
    expect(document.querySelector('dd.cashback')).toBeNull()
  })

  it('give the settings page a Cashback card that sends only what changed, in the shape core takes', async () => {
    let items = cashbackItems()
    const calls: string[] = []
    const reply = () => json(200, { items, groups: ['cashback'] })
    await boot(
      '/admin/settings',
      'en',
      (url, init) => {
        if (url === '/api/v1/settings' && (init?.method ?? 'GET') === 'GET') return reply()
        if (url === '/api/v1/settings' && init?.method === 'PUT') {
          const values = JSON.parse(String(init.body)).values as Record<string, string>
          items = items.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i))
          return reply()
        }
        return undefined
      },
      calls,
    )
    await vi.waitFor(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    expect(text(card.querySelector('h2'))).toBe('Cashback')
    expect(text(card)).toContain('Cashback percent')
    expect(text(card)).toContain('Most cashback per order (Toman)')
    expect(text(card)).toContain('Smallest order that earns (Toman)')
    expect(text(card)).toContain('Renewals earn cashback')
    expect(text(card)).toContain('Cashback is credit in the wallet, not cash')

    const percent = card.querySelector('input#setting-cashback-percent') as HTMLInputElement
    percent.value = 'lots'
    percent.dispatchEvent(new Event('input', { bubbles: true }))
    percent.dispatchEvent(new Event('blur'))
    await nextTick()
    expect(text(card)).toContain('A percent from 0.01 to 20, for example 2.5.')

    percent.value = '۲٫۵۰'
    percent.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    const toggle = card.querySelector('input#setting-cashback-enabled') as HTMLInputElement
    toggle.click()
    await nextTick()
    const save = [...card.querySelectorAll('button')].find((b) => b.getAttribute('type') === 'submit') as HTMLButtonElement
    save.click()
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/settings'))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/settings')) as string
    expect(JSON.parse(put.slice(put.indexOf('{'))).values).toEqual({ 'cashback.enabled': 'true', 'cashback.percent': '2.5' })
  })
})
