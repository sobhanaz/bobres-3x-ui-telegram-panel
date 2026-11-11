import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import ar from '../locales/ar.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import { amounts, counts, moveEmpty, showCard, type InternalMove, type InternalReport } from '../internalOrders'
import type { SettingItem } from '../types'

// Internal orders: owner accounts' purchases left out of revenue and the reports. On the Settings page the switch sits in the Reports card with
// two buttons under it (count the earlier purchases as internal, with a dry run and a confirmation; put them back); on the Reports page a card
// shows what was left out. Against a fake core, in English and Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
const names = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort()

const SWITCH = 'reports.exclude_owner_orders'
function item(key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem {
  return { key, group: 'reports', kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra }
}
const reportItems = (): SettingItem[] => [
  item('reports.daily_summary', 'bool', { default: 'false' }),
  item('reports.daily_summary_time', 'time', { default: '09:00' }),
  item(SWITCH, 'bool', { default: 'false' }),
]

const move = (over: Partial<InternalMove> = {}): InternalMove => ({
  dry_run: true,
  orders: 3,
  ledger_entries: 7,
  currencies: [
    { currency: 'IRT', orders: 3, amount: 60_000, refunds: 1, refunds_amount: 10_000 },
    { currency: 'USDT', orders: 1, amount: 5_000_000, refunds: 0, refunds_amount: 0 },
  ],
  ...over,
})

const card = (over: Partial<InternalReport> = {}): InternalReport => ({
  from: '2026-10-15',
  to: '2026-10-17',
  tz: 'Asia/Tehran',
  enabled: true,
  orders: 3,
  currencies: [{ currency: 'IRT', orders: 3, amount: 60_000, refunds: 1, refunds_amount: 10_000 }],
  accounts: [
    { user: { id: 'u9', telegram_id: 900001, username: 'boss' }, orders: 2, currencies: [{ currency: 'IRT', orders: 2, amount: 40_000, refunds: 0, refunds_amount: 0 }] },
    { user: { id: 'u8', telegram_id: 900002, username: '' }, orders: 1, currencies: [{ currency: 'IRT', orders: 1, amount: 20_000, refunds: 1, refunds_amount: 10_000 }] },
  ],
  ...over,
})

describe('the helpers', () => {
  it('say when the card has something to show', () => {
    expect(showCard(null)).toBe(false)
    expect(showCard(card({ enabled: false, orders: 0, currencies: [], accounts: [] }))).toBe(false)
    expect(showCard(card({ enabled: true, orders: 0, currencies: [], accounts: [] }))).toBe(true) // the switch is on: say there is none yet
    expect(showCard(card({ enabled: false }))).toBe(true) // the switch went off but earlier rows were marked
    expect(showCard(card({ enabled: false, orders: 0, currencies: [{ currency: 'IRT', orders: 0, amount: 0, refunds: 1, refunds_amount: 5 }] }))).toBe(true)
  })

  it('write each currency on its own and fill the confirmation in', () => {
    expect(amounts(move().currencies, 'en')).toBe('60,000 Toman, 5 USDT')
    expect(amounts(move().currencies, 'fa')).toContain('،')
    expect(amounts([{ currency: 'IRT', amount: 0 }], 'en')).toBe('')
    expect(counts(move(), 'en')).toEqual({ orders: '3', entries: '7', amounts: '60,000 Toman, 5 USDT' })
    expect(counts(move({ currencies: [] }), 'en').amounts).toBe('—')
    expect(moveEmpty(move())).toBe(false)
    expect(moveEmpty(move({ orders: 0, ledger_entries: 0, currencies: [] }))).toBe(true)
  })
})

describe('the texts', () => {
  const keys = [
    `settings.field.${SWITCH}.label`,
    `settings.field.${SWITCH}.help`,
    'settings.group.reports.title',
    'settings.group.reports.desc',
    ...['title', 'help', 'count', 'undo', 'confirm_title', 'confirm', 'none', 'counted', 'undo_title', 'undo_confirm', 'undone', 'undone_none'].map((k) => `settings.internal.${k}`),
    ...['title', 'desc', 'off', 'none', 'orders', 'line', 'refunded', 'accounts'].map((k) => `reports.internal.${k}`),
    'audit.action.reports.internal_backfill',
    'audit.action.reports.internal_undo',
  ]

  it('exist in all five languages with the same values to fill in', () => {
    for (const k of keys) {
      const base = get(en, k)
      expect(typeof base, `en ${k}`).toBe('string')
      for (const [lang, cat] of [['fa', fa], ['ar', ar], ['ru', ru], ['tr', tr]] as const) {
        const v = get(cat, k)
        expect(typeof v === 'string' && v.trim() !== '', `${lang} ${k}`).toBe(true)
        expect(names(v as string), `${lang} ${k}`).toEqual(names(base as string))
      }
    }
  })

  it('name the button as the owner asked and say that nothing is deleted', () => {
    expect(en.settings.internal.count).toBe("Count the owner accounts' earlier purchases as internal")
    expect(en.settings.field.reports.exclude_owner_orders.label).toBe('Purchases by owner accounts are not counted in revenue and reports')
    expect(en.settings.internal.confirm).toContain('Nothing is deleted')
    expect(fa.settings.internal.confirm).toContain('چیزی پاک نمی‌شود')
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
  const all = ['overview.read', 'settings.write', 'audit.read', 'reports.read']

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

  /** boot starts the app at path against a fake core. */
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

  const settingsAnswer = (state: { items: SettingItem[] }, plan: () => InternalMove, done: () => InternalMove = () => move({ dry_run: false })) =>
    (url: string, init?: RequestInit): Response | undefined => {
      const method = init?.method ?? 'GET'
      if (url === '/api/v1/settings' && method === 'GET') return json(200, { items: state.items, groups: ['reports'] })
      if (url === '/api/v1/reports/internal/backfill' && method === 'POST') {
        const body = JSON.parse(String(init?.body)) as { dry_run?: boolean }
        return json(200, body.dry_run ? plan() : done())
      }
      if (url === '/api/v1/reports/internal/undo' && method === 'POST') return json(200, move({ dry_run: false }))
      return undefined
    }
  const button = (label: string) => [...document.querySelectorAll('button')].find((b) => text(b) === label) as HTMLButtonElement | undefined
  const accept = async () => {
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog-accept-button')).not.toBeNull())
    ;(document.querySelector('.p-confirmdialog-accept-button') as HTMLButtonElement).click()
  }

  it('give the Reports card of the settings the switch and the two buttons under it', async () => {
    const state = { items: reportItems() }
    await boot('/admin/settings', 'en', settingsAnswer(state, move))
    await vi.waitFor(() => expect(document.querySelector('.internal-actions')).not.toBeNull())
    const settings = document.querySelector('.settings-card') as HTMLElement
    expect(text(settings.querySelector('h2'))).toBe('Reports')
    expect(text(settings)).toContain('Purchases by owner accounts are not counted in revenue and reports')
    expect(settings.querySelector('input#setting-reports-exclude-owner-orders')).not.toBeNull()
    expect(settings.contains(document.querySelector('.internal-actions'))).toBe(true) // the same card
    expect(button("Count the owner accounts' earlier purchases as internal")).toBeDefined()
    expect(button("Put the owner accounts' purchases back in the reports")).toBeDefined()
  })

  it('show what would move first, move nothing until confirmed, and then say what moved', async () => {
    const state = { items: reportItems() }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state, move), calls)
    await vi.waitFor(() => expect(button("Count the owner accounts' earlier purchases as internal")).toBeDefined())
    button("Count the owner accounts' earlier purchases as internal")!.click()

    // The dry run is asked, and its numbers are in the confirmation.
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).not.toBeNull())
    const dialog = text(document.querySelector('.p-confirmdialog'))
    expect(dialog).toContain('3 orders and 7 ledger entries of the owner accounts will be marked internal')
    expect(dialog).toContain('60,000 T, 5 USDT') // the store's own short name for the Toman
    expect(dialog).toContain('Nothing is deleted')
    expect(calls.filter((c) => c.startsWith('POST /api/v1/reports/internal/backfill'))).toEqual(['POST /api/v1/reports/internal/backfill {"dry_run":true}'])

    // Cancelled: nothing else is sent.
    ;(document.querySelector('.p-confirmdialog-reject-button') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    expect(calls.filter((c) => c.startsWith('POST /api/v1/reports/internal/backfill'))).toHaveLength(1)

    // Confirmed: the real call follows, and the toast says what was moved.
    button("Count the owner accounts' earlier purchases as internal")!.click()
    await accept()
    await vi.waitFor(() => expect(calls.filter((c) => c.startsWith('POST /api/v1/reports/internal/backfill'))).toHaveLength(3))
    expect(calls.filter((c) => c.startsWith('POST /api/v1/reports/internal/backfill')).slice(1)).toEqual([
      'POST /api/v1/reports/internal/backfill {"dry_run":true}',
      'POST /api/v1/reports/internal/backfill {"dry_run":false}',
    ])
    await vi.waitFor(() => expect(text(document.querySelector('.p-toast'))).toContain('3 orders and 7 ledger entries are internal now'))
  })

  it('say there is nothing to count when nothing would move, and ask for no confirmation', async () => {
    const state = { items: reportItems() }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state, () => move({ orders: 0, ledger_entries: 0, currencies: [] })), calls)
    await vi.waitFor(() => expect(button("Count the owner accounts' earlier purchases as internal")).toBeDefined())
    button("Count the owner accounts' earlier purchases as internal")!.click()
    await vi.waitFor(() => expect(text(document.querySelector('.p-toast'))).toContain('Nothing to count'))
    expect(document.querySelector('.p-confirmdialog')).toBeNull()
    expect(calls.filter((c) => c.startsWith('POST /api/v1/reports/internal/backfill'))).toHaveLength(1)
  })

  it('put the purchases back only after a confirmation', async () => {
    const state = { items: reportItems() }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state, move), calls)
    await vi.waitFor(() => expect(button("Put the owner accounts' purchases back in the reports")).toBeDefined())
    button("Put the owner accounts' purchases back in the reports")!.click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).not.toBeNull())
    expect(calls.some((c) => c.includes('/internal/undo'))).toBe(false)
    expect(text(document.querySelector('.p-confirmdialog'))).toContain('goes back into revenue and the reports')
    await accept()
    await vi.waitFor(() => expect(calls).toContain('POST /api/v1/reports/internal/undo {}'))
    await vi.waitFor(() => expect(text(document.querySelector('.p-toast'))).toContain('count in the reports again'))
  })

  it('keep the buttons and the dialog in Persian', async () => {
    const state = { items: reportItems() }
    await boot('/admin/settings', 'fa', settingsAnswer(state, move))
    await vi.waitFor(() => expect(button('خریدهای قبلی حساب‌های مالک هم داخلی حساب شود')).toBeDefined())
    expect(text(document.querySelector('.settings-card'))).toContain('خرید حساب‌های مالک در درآمد و گزارش‌ها شمرده نشود')
    button('خریدهای قبلی حساب‌های مالک هم داخلی حساب شود')!.click()
    await vi.waitFor(() => expect(text(document.querySelector('.p-confirmdialog'))).toContain('۳ سفارش و ۷ ردیف دفتر حساب'))
  })

  // ---- the Reports page
  const flow = (count = 0, amount = 0) => ({ count, amount })
  const summary = () => ({
    from: '2026-10-15', to: '2026-10-17', group: 'day', calendar: 'gregorian', tz: 'Asia/Tehran', churn_grace_days: 7, currencies: ['IRT'],
    buckets: [
      {
        date: '2026-10-15', partial: false, new_customers: 1, trials: 0, churned_services: 0, churned_customers: 0, at_risk: 0,
        currencies: { IRT: { revenue: 100_000, refunds: flow(), net: 100_000, orders: 1, new_orders: 1, renewals: 0, traffic_topups: 0, topups: flow(), rejected: flow(), gateway_failed: flow(), cancelled: flow() } },
      },
    ],
    totals: {
      new_customers: 1, trials: 0, churned_services: 0, churned_customers: 0, at_risk: 0,
      currencies: { IRT: { revenue: 100_000, refunds: flow(), net: 100_000, orders: 1, new_orders: 1, renewals: 0, traffic_topups: 0, topups: flow(), rejected: flow(), gateway_failed: flow(), cancelled: flow() } },
    },
  })
  const reportsAnswer = (internal: () => Response) => (url: string): Response | undefined => {
    if (url.startsWith('/api/v1/reports/summary')) return json(200, summary())
    if (url.startsWith('/api/v1/reports/plans')) return json(200, { plans: [] })
    if (url.startsWith('/api/v1/reports/methods')) return json(200, { methods: [] })
    if (url.startsWith('/api/v1/reports/internal')) return internal()
    return undefined
  }

  it('show the Internal orders card of the Reports page: the count, the money of each currency, and each account', async () => {
    const calls: string[] = []
    await boot('/admin/reports', 'en', reportsAnswer(() => json(200, card())), calls)
    await vi.waitFor(() => expect(document.querySelector('.internal-card')).not.toBeNull())
    const el = document.querySelector('.internal-card') as HTMLElement
    expect(text(el)).toContain('Internal orders')
    expect(text(el.querySelector('.internal-count'))).toBe('3')
    const irt = el.querySelector('.internal-currency[data-currency="IRT"]')!
    expect(text(irt.querySelector('.line'))).toBe('Orders: 3 · 60,000 T')
    expect(text(irt.querySelector('.refunded'))).toBe('Refunded: 1 · 10,000 T')
    const rows = [...el.querySelectorAll('.internal-account')].map((r) => [...r.children].map((c) => text(c)))
    expect(rows).toEqual([
      ['@boss900001', '2', '40,000 T'],
      ['900002', '1', '20,000 T'],
    ])
    // asked with the days and the zone the summary named
    expect(calls.some((c) => c.startsWith('GET /api/v1/reports/internal?from=2026-10-15&to=2026-10-17&tz=Asia%2FTehran'))).toBe(true)
    // the figures of the page are the summary's, without the internal orders
    expect(text(document.querySelector('.currency-card'))).toContain('100,000 T')
    expect(text(document.querySelector('.currency-card'))).toContain('1 paid order')
    // the switch is on, so no remark that it is off
    expect(el.querySelector('.internal-off')).toBeNull()
  })

  it('say so when the switch is on and there is none, and when it is off but earlier ones were marked', async () => {
    await boot('/admin/reports', 'en', reportsAnswer(() => json(200, card({ orders: 0, currencies: [], accounts: [] }))))
    await vi.waitFor(() => expect(document.querySelector('.internal-card')).not.toBeNull())
    expect(text(document.querySelector('.internal-none'))).toBe('No purchase by an owner account was marked internal in this range.')
    expect(document.querySelector('.internal-count')).toBeNull()

    await boot('/admin/reports', 'en', reportsAnswer(() => json(200, card({ enabled: false }))))
    await vi.waitFor(() => expect(document.querySelector('.internal-off')).not.toBeNull())
    expect(text(document.querySelector('.internal-off'))).toContain('The switch in Settings > Reports is off')
    expect(text(document.querySelector('.internal-count'))).toBe('3')
  })

  it('leave the card out when the switch is off and nothing was marked, or when core cannot answer', async () => {
    await boot('/admin/reports', 'en', reportsAnswer(() => json(200, card({ enabled: false, orders: 0, currencies: [], accounts: [] }))))
    await vi.waitFor(() => expect(document.querySelector('.currency-card')).not.toBeNull())
    expect(document.querySelector('.internal-card')).toBeNull()

    await boot('/admin/reports', 'en', reportsAnswer(() => json(500, { error: 'internal', message: 'x' })))
    await vi.waitFor(() => expect(document.querySelector('.currency-card')).not.toBeNull())
    expect(document.querySelector('.internal-card')).toBeNull()
  })

  it('show the card even when nothing else was sold in the range', async () => {
    const empty = { ...summary(), currencies: [], buckets: [], totals: { new_customers: 0, trials: 0, churned_services: 0, churned_customers: 0, at_risk: 0, currencies: {} } }
    await boot('/admin/reports', 'en', (url) => (url.startsWith('/api/v1/reports/summary') ? json(200, empty) : reportsAnswer(() => json(200, card()))(url)))
    await vi.waitFor(() => expect(document.querySelector('.internal-card')).not.toBeNull())
    expect(text(document.querySelector('.empty'))).toBe('No sales in this period.')
    expect(document.querySelector('.currency-card')).toBeNull()
    expect(text(document.querySelector('.internal-count'))).toBe('3')
  })

  it('write the card in Persian', async () => {
    await boot('/admin/reports', 'fa', reportsAnswer(() => json(200, card())))
    await vi.waitFor(() => expect(document.querySelector('.internal-card')).not.toBeNull())
    const el = document.querySelector('.internal-card') as HTMLElement
    expect(text(el)).toContain('سفارش‌های داخلی')
    expect(text(el.querySelector('.internal-count'))).toBe('۳')
    expect(text(el.querySelector('.internal-currency'))).toContain('۶۰٬۰۰۰')
  })
})
