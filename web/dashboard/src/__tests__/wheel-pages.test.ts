import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { localDate } from '../format'
import { canonical, changes, chancePercent, formatPrizes, MAX_PRIZES, normalize, normalizePrizes, parsePrizes, problems, validate } from '../settings'
import type { SettingItem, WheelReportView } from '../types'

// The daily wheel on the Settings page (a Daily wheel card with its prizes as rows and each row's chance, and under it a small report), against a
// fake core, in English and Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function item(key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem {
  return { key, group: 'wheel', kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra }
}
const wheelItems = (): SettingItem[] => [
  item('wheel.enabled', 'bool', { default: 'false' }),
  item('wheel.prizes', 'list'),
  item('wheel.no_prize_weight', 'int', { min: 0, max: 1_000_000_000 }),
  item('wheel.daily_budget', 'int', { min: 0, max: 100_000_000_000 }),
  item('wheel.min_orders', 'int', { min: 1, max: 1000, default: '1' }),
]

describe('the prizes of the wheel', () => {
  it('are read as core reads them', () => {
    expect(parsePrizes('')).toEqual({ prizes: [] })
    expect(parsePrizes('null')).toEqual({ prizes: [] })
    expect(parsePrizes('[]')).toEqual({ prizes: [] })
    expect(parsePrizes('[{"amount":5000,"weight":1},{"amount":1000,"weight":3}]')).toEqual({
      prizes: [{ amount: 1000, weight: 3 }, { amount: 5000, weight: 1 }],
    })
    expect(parsePrizes('[{"weight":2,"amount":7}]')).toEqual({ prizes: [{ amount: 7, weight: 2 }] })
    expect(parsePrizes('[{"amount":100000000000,"weight":1000000}]')).toEqual({ prizes: [{ amount: 100_000_000_000, weight: 1_000_000 }] })
  })

  it('are refused when they are not whole, in range, unique and few', () => {
    for (const bad of [
      'nope', '{}', '7', '[1]', '[{}]', '[{"amount":1}]', '[{"amount":1,"weight":1,"x":1}]', '[{"amount":"1","weight":1}]', '[{"amount":1.5,"weight":1}]',
      '[{"amount":0,"weight":1}]', '[{"amount":1,"weight":0}]', '[{"amount":-1,"weight":1}]', '[{"amount":100000000001,"weight":1}]', '[{"amount":1,"weight":1000001}]',
      '[{"amount":"","weight":""}]',
    ]) {
      expect(parsePrizes(bad), bad).toEqual({ code: 'prizes' })
    }
    expect(parsePrizes('[{"amount":5,"weight":1},{"amount":5,"weight":2}]')).toEqual({ code: 'prizes_dup' })
    const many = JSON.stringify(Array.from({ length: MAX_PRIZES + 1 }, (_, i) => ({ amount: i + 1, weight: 1 })))
    expect(parsePrizes(many)).toEqual({ code: 'prizes_many' })
    const full = JSON.stringify(Array.from({ length: MAX_PRIZES }, (_, i) => ({ amount: i + 1, weight: 1 })))
    expect('prizes' in parsePrizes(full)).toBe(true)
  })

  it('are written the way core stores them: sorted by amount, compact, nothing for none', () => {
    expect(formatPrizes([])).toBe('')
    expect(formatPrizes([{ amount: 5000, weight: 1 }, { amount: 1000, weight: 3 }])).toBe('[{"amount":1000,"weight":3},{"amount":5000,"weight":1}]')
    expect(normalizePrizes(' [ { "weight": 3, "amount": 1000 } ] ')).toBe('[{"amount":1000,"weight":3}]')
    expect(normalizePrizes('[]')).toBe('')
    expect(normalizePrizes(' not json ')).toBe('not json')
  })

  it('are checked as the card sends them, separately from the tiers of the targets', () => {
    const prizes = item('wheel.prizes', 'list')
    expect(normalize(prizes, '[{"weight":3,"amount":1000}]')).toBe('[{"amount":1000,"weight":3}]')
    expect(validate(prizes, '[{"amount":1000,"weight":3}]')).toBeNull()
    expect(validate(prizes, '')).toBeNull()
    expect(validate(prizes, '[{"amount":"","weight":""}]')).toEqual({ code: 'prizes' })
    expect(validate(prizes, '[{"amount":1,"weight":1},{"amount":1,"weight":2}]')).toEqual({ code: 'prizes_dup' })
    // a tier list is still a tier list
    const tiers = item('targets.tiers', 'list')
    expect(validate(tiers, '[{"threshold":3,"bonus":10000}]')).toBeNull()
    expect(validate(tiers, '[{"amount":1000,"weight":3}]')).toEqual({ code: 'tiers' })
    const items = wheelItems()
    expect(changes(items, { 'wheel.prizes': '[{"weight":3,"amount":1000}]', 'wheel.min_orders': '1' })).toEqual({ 'wheel.prizes': '[{"amount":1000,"weight":3}]' })
    expect(problems(items, { 'wheel.prizes': '[{"amount":0,"weight":1}]' })).toEqual({ 'wheel.prizes': { code: 'prizes' } })
    expect(problems(items, { 'wheel.min_orders': '0' })['wheel.min_orders']).toBeDefined()
    expect(problems(items, { 'wheel.prizes': '[{"amount":10,"weight":1}]', 'wheel.daily_budget': '5000' })).toEqual({})
    expect(canonical(items[1], '[]')).toBe('')
  })

  it('show their chances in the language of the page', () => {
    expect(chancePercent(0.375, 'en')).toBe('37.5%')
    expect(chancePercent(0.375, 'fa')).toBe('۳۷٫۵٪')
    expect(chancePercent(1 / 3, 'en')).toBe('33.33%')
    expect(chancePercent(1, 'en')).toBe('100%')
  })
})

describe('the texts of the wheel', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = [
    'settings.group.wheel.title', 'settings.group.wheel.desc', 'settings.group.wheel.note', 'settings.invalid.prizes', 'settings.invalid.prizes_dup',
    'settings.invalid.prizes_many', 'texts.group.wheel', 'settings.prizes.empty', 'settings.prizes.row', 'settings.prizes.amount', 'settings.prizes.weight',
    'settings.prizes.chance', 'settings.prizes.remove', 'settings.prizes.add', 'settings.prizes.max', 'settings.prizes.sorted',
    ...['enabled', 'prizes', 'no_prize_weight', 'daily_budget', 'min_orders'].flatMap((k) => [`settings.field.wheel.${k}.label`, `settings.field.wheel.${k}.help`]),
    ...['title', 'desc', 'off', 'today', 'spins', 'winners', 'paid', 'budget', 'remaining', 'day', 'total', 'empty'].map((k) => `wheel.report.${k}`),
  ]

  it('are in both languages, with the same values to fill in', () => {
    const names = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort() // English plural forms repeat a name
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
  const all = ['overview.read', 'payments.read', 'payments.review', 'settings.write', 'discounts.write', 'audit.read', 'reports.read']

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
  async function boot(path: string, lang: 'en' | 'fa', answer: (url: string, init?: RequestInit) => Response | undefined, calls: string[] = [], perms = all) {
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
        if (url === '/api/v1/me') return json(200, me(perms))
        if (url === '/api/v1/branding') return json(200, { name: 'Shop', support: '', color: '#0f766e', terms_url: '', privacy_url: '', currency: { fa: '', en: 'T' }, logo: null, defaults: { name: 'BOBRES', color: '#14b8a6', currency: { fa: 'تومان', en: 'Toman' } } })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
  }
  const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
  const type = (input: HTMLInputElement, v: string) => {
    input.value = v
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }

  const report = (over: Partial<WheelReportView> = {}): WheelReportView => ({
    on: true, day: '2026-10-28', budget: { amount: 20000, currency: 'IRT' }, remaining: { amount: 14000, currency: 'IRT' },
    today: { day: '2026-10-28', spins: 3, winners: 2, paid: { amount: 6000, currency: 'IRT' } },
    days: [
      { day: '2026-10-28', spins: 3, winners: 2, paid: { amount: 6000, currency: 'IRT' } },
      { day: '2026-10-26', spins: 5, winners: 4, paid: { amount: 9000, currency: 'IRT' } },
    ],
    total: { day: '', spins: 8, winners: 6, paid: { amount: 15000, currency: 'IRT' } }, window_days: 30, ...over,
  })

  const settingsAnswer = (state: { items: SettingItem[] }, rep: WheelReportView | null = report()) => (url: string, init?: RequestInit): Response | undefined => {
    const reply = () => json(200, { items: state.items, groups: ['wheel'] })
    if (url === '/api/v1/settings' && (init?.method ?? 'GET') === 'GET') return reply()
    if (url === '/api/v1/settings' && init?.method === 'PUT') {
      const values = JSON.parse(String(init.body)).values as Record<string, string>
      state.items = state.items.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i))
      return reply()
    }
    if (url === '/api/v1/wheel/report') return rep ? json(200, rep) : json(500, { error: 'internal', message: 'x' })
    return undefined
  }

  it('give the settings page a Daily wheel card with prizes as rows and their chances, sent sorted in the shape core takes', async () => {
    const state = { items: wheelItems() }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state), calls)
    await vi.waitFor(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    expect(text(card.querySelector('h2'))).toBe('Daily wheel')
    for (const label of ['Prizes', 'Weight of no prize', 'Daily budget (Toman)', 'Paid orders needed']) expect(text(card)).toContain(label)
    expect(text(card)).toContain('No prize yet.')
    expect(text(card)).toContain('A prize is credit in the wallet, not cash')

    const add = [...card.querySelectorAll('button')].find((b) => text(b) === 'Add a prize') as HTMLButtonElement
    add.click()
    await nextTick()
    add.click()
    await nextTick()
    const inputs = () => [...card.querySelectorAll('.prize input')] as HTMLInputElement[]
    expect(inputs()).toHaveLength(4)

    // typed out of order; a Persian digit and a thousands separator are read
    type(inputs()[0], '5,000')
    type(inputs()[1], '1')
    type(inputs()[2], '۱۰۰۰')
    type(inputs()[3], '3')
    type(card.querySelector('input#setting-wheel-no-prize-weight') as HTMLInputElement, '4')
    await nextTick()
    // 1 + 3 + 4 = 8 weights: the chances are 12.5% and 37.5%
    expect(text(card)).toContain('12.5% of spins')
    expect(text(card)).toContain('37.5% of spins')
    expect(text(card)).toContain('5,000 T')

    // a duplicate amount is refused, then fixed
    type(inputs()[2], '5000')
    inputs()[2].dispatchEvent(new Event('blur'))
    await nextTick()
    expect(text(card)).toContain('Two prizes have the same amount.')
    type(inputs()[2], '1000')
    await nextTick()
    expect(text(card)).not.toContain('Two prizes have the same amount.')

    ;(card.querySelector('input#setting-wheel-enabled') as HTMLInputElement).click()
    type(card.querySelector('input#setting-wheel-daily-budget') as HTMLInputElement, '20000')
    await nextTick()
    const save = [...card.querySelectorAll('button')].find((b) => b.getAttribute('type') === 'submit') as HTMLButtonElement
    save.click()
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/settings'))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/settings')) as string
    expect(JSON.parse(put.slice(put.indexOf('{'))).values).toEqual({
      'wheel.enabled': 'true',
      'wheel.prizes': '[{"amount":1000,"weight":3},{"amount":5000,"weight":1}]',
      'wheel.no_prize_weight': '4',
      'wheel.daily_budget': '20000',
    })
  })

  it('show the prizes that are saved, and take a row away', async () => {
    const state = {
      items: wheelItems().map((i) =>
        i.key === 'wheel.prizes' ? { ...i, value: '[{"amount":1000,"weight":3},{"amount":5000,"weight":1}]' } : i.key === 'wheel.no_prize_weight' ? { ...i, value: '4' } : i,
      ),
    }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state), calls)
    await vi.waitFor(() => expect(document.querySelector('.prize')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    const inputs = () => [...card.querySelectorAll('.prize input')] as HTMLInputElement[]
    expect(inputs().map((i) => i.value)).toEqual(['1000', '3', '5000', '1'])
    expect(text(card)).toContain('37.5% of spins')
    ;(card.querySelector('button[aria-label="Remove prize 1"]') as HTMLButtonElement).click()
    await nextTick()
    expect(inputs().map((i) => i.value)).toEqual(['5000', '1'])
    expect(text(card)).toContain('20% of spins') // 1 out of 1 + 4
    const save = [...card.querySelectorAll('button')].find((b) => b.getAttribute('type') === 'submit') as HTMLButtonElement
    save.click()
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/settings'))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/settings')) as string
    expect(JSON.parse(put.slice(put.indexOf('{'))).values).toEqual({ 'wheel.prizes': '[{"amount":5000,"weight":1}]' })
  })

  it('show the report under the card: today against the budget, and the days that had a spin', async () => {
    const state = { items: wheelItems() }
    await boot('/admin/settings', 'en', settingsAnswer(state))
    await vi.waitFor(() => expect(document.querySelector('[data-wheel-report]')).not.toBeNull())
    await vi.waitFor(() => expect(text(document.querySelector('[data-wheel-report]'))).toContain('Paid out'))
    const rep = document.querySelector('[data-wheel-report]') as HTMLElement
    const t = text(rep)
    expect(t).toContain('Daily wheel report')
    expect(t).toContain(`${localDate('2026-10-28', 'en')} · Spins: 3 · Winners: 2 · Paid out: 6,000 T`)
    expect(t).toContain('Daily budget20,000 T')
    expect(t).toContain('Left today14,000 T')
    const cells = (row: Element) => [...row.querySelectorAll('th, td')].map((c) => text(c))
    expect([...rep.querySelectorAll('tbody tr')].map(cells)).toEqual([
      [localDate('2026-10-28', 'en'), '3', '2', '6,000 T'],
      [localDate('2026-10-26', 'en'), '5', '4', '9,000 T'],
    ])
    expect(cells(rep.querySelector('tfoot tr') as Element)).toEqual(['All these days', '8', '6', '15,000 T'])
    expect(t).not.toContain('off or closed')
  })

  it('say when the wheel is off or closed, and when nobody has spun', async () => {
    const state = { items: wheelItems() }
    await boot('/admin/settings', 'en', settingsAnswer(state, report({ on: false, days: [], today: { day: '2026-10-28', spins: 0, winners: 0, paid: { amount: 0, currency: 'IRT' } } })))
    await vi.waitFor(() => expect(text(document.querySelector('[data-wheel-report]'))).toContain('Nobody has spun yet.'))
    expect(text(document.querySelector('[data-wheel-report]'))).toContain('The wheel is off or closed now')
  })

  it('leave the report to those who read reports', async () => {
    const state = { items: wheelItems() }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state), calls, all.filter((p) => p !== 'reports.read'))
    await vi.waitFor(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    expect(document.querySelector('[data-wheel-report]')).toBeNull()
    expect(calls.some((c) => c.includes('/api/v1/wheel/report'))).toBe(false)
  })

  it('keep the card and the report in Persian', async () => {
    const state = { items: wheelItems().map((i) => (i.key === 'wheel.prizes' ? { ...i, value: '[{"amount":1000,"weight":3}]' } : i.key === 'wheel.no_prize_weight' ? { ...i, value: '5' } : i)) }
    await boot('/admin/settings', 'fa', settingsAnswer(state))
    await vi.waitFor(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    expect(text(card.querySelector('h2'))).toBe('گردونه شانس')
    expect(text(card)).toContain('۳۷٫۵٪ از چرخش‌ها')
    await vi.waitFor(() => expect(text(document.querySelector('[data-wheel-report]'))).toContain('پرداخت‌شده'))
    expect(text(document.querySelector('[data-wheel-report]'))).toContain('گزارش گردونه شانس')
  })
})
