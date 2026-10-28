import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { localDate } from '../format'
import { resellerOf } from '../resellers'
import { canonical, changes, formatTiers, MAX_TIERS, normalize, normalizeTiers, parseTiers, problems, validate } from '../settings'
import type { ResellerDetail, SettingItem, TargetsView } from '../types'

// The reseller targets on the Settings page (a Targets card with its tiers as rows) and on a reseller's page (their month against the tiers), against
// a fake core, in English and Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function item(key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem {
  return { key, group: 'targets', kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra }
}
const targetItems = (): SettingItem[] => [
  item('targets.enabled', 'bool', { default: 'false' }),
  item('targets.tiers', 'list'),
  item('targets.calendar', 'enum', { default: 'gregorian', options: ['gregorian', 'jalali'] }),
]

describe('the tiers of the targets', () => {
  it('are read as core reads them', () => {
    expect(parseTiers('')).toEqual({ tiers: [] })
    expect(parseTiers('  ')).toEqual({ tiers: [] })
    expect(parseTiers('null')).toEqual({ tiers: [] })
    expect(parseTiers('[]')).toEqual({ tiers: [] })
    expect(parseTiers('[{"threshold":10,"bonus":100000},{"threshold":3,"bonus":10000}]')).toEqual({
      tiers: [{ threshold: 3, bonus: 10000 }, { threshold: 10, bonus: 100000 }],
    })
    // the order of a row's two names does not matter
    expect(parseTiers('[{"bonus":5,"threshold":2}]')).toEqual({ tiers: [{ threshold: 2, bonus: 5 }] })
    expect(parseTiers('[{"threshold":100000,"bonus":100000000000}]')).toEqual({ tiers: [{ threshold: 100000, bonus: 100_000_000_000 }] })
  })

  it('are refused when they are not whole, in range, unique and few', () => {
    const bad = [
      'nope', '{}', '"x"', '7', '[1]', '[null]', '[[]]', '[{}]', '[{"threshold":3}]', '[{"bonus":3}]', '[{"threshold":3,"bonus":4,"extra":1}]',
      '[{"threshold":"3","bonus":4}]', '[{"threshold":3,"bonus":"4"}]', '[{"threshold":3.5,"bonus":4}]', '[{"threshold":3,"bonus":4.5}]',
      '[{"threshold":0,"bonus":4}]', '[{"threshold":-1,"bonus":4}]', '[{"threshold":3,"bonus":0}]', '[{"threshold":100001,"bonus":4}]',
      '[{"threshold":3,"bonus":100000000001}]', '[{"threshold":"","bonus":""}]',
    ]
    for (const raw of bad) expect(parseTiers(raw), raw).toEqual({ code: 'tiers' })
    expect(parseTiers('[{"threshold":3,"bonus":4},{"threshold":3,"bonus":9}]')).toEqual({ code: 'tiers_dup' })
    const many = JSON.stringify(Array.from({ length: MAX_TIERS + 1 }, (_, i) => ({ threshold: i + 1, bonus: 1 })))
    expect(parseTiers(many)).toEqual({ code: 'tiers_many' })
    const full = JSON.stringify(Array.from({ length: MAX_TIERS }, (_, i) => ({ threshold: i + 1, bonus: 1 })))
    expect('tiers' in parseTiers(full)).toBe(true)
  })

  it('are written the way core stores them: sorted, compact, nothing for none', () => {
    expect(formatTiers([])).toBe('')
    expect(formatTiers([{ threshold: 10, bonus: 100000 }, { threshold: 3, bonus: 10000 }])).toBe('[{"threshold":3,"bonus":10000},{"threshold":10,"bonus":100000}]')
    expect(normalizeTiers(' [ { "bonus": 10000, "threshold": 3 } ] ')).toBe('[{"threshold":3,"bonus":10000}]')
    expect(normalizeTiers('[]')).toBe('')
    expect(normalizeTiers(' not json ')).toBe('not json') // left as typed, for validate to refuse
  })

  it('are checked as the card sends them', () => {
    const tiers = item('targets.tiers', 'list')
    expect(normalize(tiers, '[{"bonus":10000,"threshold":3}]')).toBe('[{"threshold":3,"bonus":10000}]')
    expect(validate(tiers, '[{"threshold":3,"bonus":10000}]')).toBeNull()
    expect(validate(tiers, '')).toBeNull() // empty is "back to the default": no tiers, so nothing to pay
    expect(validate(tiers, '[{"threshold":"","bonus":""}]')).toEqual({ code: 'tiers' })
    expect(validate(tiers, '[{"threshold":3,"bonus":1},{"threshold":3,"bonus":2}]')).toEqual({ code: 'tiers_dup' })
    const items = targetItems()
    expect(changes(items, { 'targets.tiers': '[{"bonus":10000,"threshold":3}]', 'targets.calendar': 'gregorian' })).toEqual({
      'targets.tiers': '[{"threshold":3,"bonus":10000}]',
    })
    expect(problems(items, { 'targets.tiers': '[{"threshold":0,"bonus":1}]' })).toEqual({ 'targets.tiers': { code: 'tiers' } })
    expect(problems(items, { 'targets.tiers': '[{"threshold":1,"bonus":1}]', 'targets.calendar': 'jalali' })).toEqual({})
    expect(canonical(items[1], '[]')).toBe('')
  })
})

describe('the texts of the targets', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = [
    'settings.group.targets.title', 'settings.group.targets.desc', 'settings.group.targets.note', 'settings.invalid.tiers', 'settings.invalid.tiers_dup',
    'settings.invalid.tiers_many', 'settings.field.targets.enabled.label', 'settings.field.targets.enabled.help', 'settings.field.targets.tiers.label',
    'settings.field.targets.tiers.help', 'settings.field.targets.calendar.label', 'settings.field.targets.calendar.help',
    'settings.option.targets.calendar.gregorian', 'settings.option.targets.calendar.jalali', 'settings.tiers.empty', 'settings.tiers.row',
    'settings.tiers.threshold', 'settings.tiers.bonus', 'settings.tiers.remove', 'settings.tiers.add', 'settings.tiers.max', 'settings.tiers.sorted',
    'reseller.targets.title', 'reseller.targets.range', 'reseller.targets.sold', 'reseller.targets.tier', 'reseller.targets.paid',
    'reseller.targets.reached_unpaid', 'reseller.targets.not_reached', 'reseller.targets.next', 'reseller.targets.done', 'reseller.targets.paid_total',
    'reseller.targets.no_clawback',
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
  const permissions = ['overview.read', 'payments.read', 'payments.review', 'settings.write', 'discounts.write', 'audit.read', 'resellers.write', 'resellers.read']

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
        if (url === '/api/v1/me') return json(200, me(permissions))
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

  const settingsAnswer = (state: { items: SettingItem[] }) => (url: string, init?: RequestInit): Response | undefined => {
    const reply = () => json(200, { items: state.items, groups: ['targets'] })
    if (url === '/api/v1/settings' && (init?.method ?? 'GET') === 'GET') return reply()
    if (url === '/api/v1/settings' && init?.method === 'PUT') {
      const values = JSON.parse(String(init.body)).values as Record<string, string>
      state.items = state.items.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i))
      return reply()
    }
    return undefined
  }

  it('give the settings page a Targets card whose tiers are rows, sent sorted in the shape core takes', async () => {
    const state = { items: targetItems() }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state), calls)
    await vi.waitFor(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    expect(text(card.querySelector('h2'))).toBe('Reseller targets')
    expect(text(card)).toContain('Monthly targets')
    expect(text(card)).toContain('Calendar of the month')
    expect(text(card)).toContain('Gregorian')
    expect(text(card)).toContain('No tier yet.')
    expect(text(card)).toContain('A refunded sale leaves the count of its month, but a bonus already paid is not taken back.')

    const add = [...card.querySelectorAll('button')].find((b) => text(b) === 'Add a tier') as HTMLButtonElement
    add.click()
    await nextTick()
    add.click()
    await nextTick()
    const inputs = () => [...card.querySelectorAll('.tier input')] as HTMLInputElement[]
    expect(inputs()).toHaveLength(4)
    expect(text(card)).not.toContain('No tier yet.')

    // typed out of order, one with Persian digits; both twins of a threshold are refused, then fixed
    type(inputs()[0], '۱۰')
    type(inputs()[1], '100000')
    type(inputs()[2], '10')
    type(inputs()[3], '10,000')
    inputs()[2].dispatchEvent(new Event('blur'))
    await nextTick()
    expect(text(card)).toContain('Two tiers ask for the same number of services.')
    expect(text(card)).toContain('10,000 T') // the bonus in words under its row
    type(inputs()[2], '3')
    await nextTick()
    expect(text(card)).not.toContain('Two tiers ask for the same number of services.')

    ;(card.querySelector('input#setting-targets-enabled') as HTMLInputElement).click()
    await nextTick()
    const save = [...card.querySelectorAll('button')].find((b) => b.getAttribute('type') === 'submit') as HTMLButtonElement
    save.click()
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/settings'))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/settings')) as string
    expect(JSON.parse(put.slice(put.indexOf('{'))).values).toEqual({
      'targets.enabled': 'true',
      'targets.tiers': '[{"threshold":3,"bonus":10000},{"threshold":10,"bonus":100000}]',
    })
  })

  it('show the tiers that are saved, and take a row away', async () => {
    const state = { items: targetItems().map((i) => (i.key === 'targets.tiers' ? { ...i, value: '[{"threshold":3,"bonus":10000},{"threshold":10,"bonus":100000}]' } : i)) }
    const calls: string[] = []
    await boot('/admin/settings', 'en', settingsAnswer(state), calls)
    await vi.waitFor(() => expect(document.querySelector('.tier')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    const inputs = () => [...card.querySelectorAll('.tier input')] as HTMLInputElement[]
    expect(inputs().map((i) => i.value)).toEqual(['3', '10000', '10', '100000'])
    const drop = card.querySelector('button[aria-label="Remove tier 1"]') as HTMLButtonElement
    drop.click()
    await nextTick()
    expect(inputs().map((i) => i.value)).toEqual(['10', '100000'])
    const save = [...card.querySelectorAll('button')].find((b) => b.getAttribute('type') === 'submit') as HTMLButtonElement
    save.click()
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/settings'))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/settings')) as string
    expect(JSON.parse(put.slice(put.indexOf('{'))).values).toEqual({ 'targets.tiers': '[{"threshold":10,"bonus":100000}]' })
  })

  it('keep the card in Persian', async () => {
    const state = { items: targetItems() }
    await boot('/admin/settings', 'fa', settingsAnswer(state))
    await vi.waitFor(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    expect(text(card.querySelector('h2'))).toBe('هدف نمایندگان')
    expect(text(card)).toContain('هنوز پله‌ای نیست.')
    expect(text(card)).toContain('میلادی')
  })

  const month = (over: Partial<TargetsView> = {}): TargetsView => ({
    period: '2026-10', calendar: 'gregorian', from: '2026-10-01', to: '2026-10-31', ends_at: 1_793_000_000, sold: 3, remaining: 2, bonus_paid: 10000, currency: 'IRT', scale: 0,
    tiers: [
      { threshold: 2, bonus: 10000, reached: true, paid: true },
      { threshold: 5, bonus: 50000, reached: false, paid: false },
    ],
    ...over,
  })
  const detail = (targets?: TargetsView): ResellerDetail =>
    ({
      user: { id: 'u9', telegram_id: 9, username: 'shop9', status: 'active' },
      tier: { id: 't1', name: 'Gold', discount_percent: 10, commission_percent: 5 },
      commission_percent: 5, commission_override: null, balances: [], customers: 0, commission_earned: [], created_at: 1_700_000_000,
      note: '', version: 1, can_remove: true, config_prefix: '', panel_group: '', panel_group_full: '', groups_synced: false,
      ...(targets ? { targets } : {}),
    }) as unknown as ResellerDetail
  const resellerAnswer = (d: ResellerDetail) => (url: string): Response | undefined => (url === '/api/v1/resellers/u9' ? json(200, { item: d }) : undefined)

  it('show a reseller their month against the tiers', async () => {
    await boot('/admin/resellers/u9', 'en', resellerAnswer(detail(month())))
    await vi.waitFor(() => expect(document.querySelector('[data-targets]')).not.toBeNull())
    const card = document.querySelector('[data-targets]') as HTMLElement
    const t = text(card)
    expect(t).toContain('Monthly target')
    expect(t).toContain(`${localDate('2026-10-01', 'en', 'medium', 'gregorian')} to ${localDate('2026-10-31', 'en', 'medium', 'gregorian')}`)
    expect(text(card.querySelector('.sold'))).toBe('3')
    expect(t).toContain('new services sold this month')
    expect(t).toContain('2 services: 10,000 T bonus · bonus paid')
    expect(t).toContain('5 services: 50,000 T bonus · not reached yet')
    expect(t).toContain('2 more services for the next bonus of 50,000 T.')
    expect(t).toContain('Bonuses paid this month: 10,000 T')
    expect(t).toContain('a bonus already paid is not taken back')
  })

  it('say when every tier is reached, and when a reached tier has no bonus paid', async () => {
    const all = month({
      sold: 6, remaining: 0, bonus_paid: 60000,
      tiers: [
        { threshold: 2, bonus: 10000, reached: true, paid: true },
        { threshold: 5, bonus: 50000, reached: true, paid: false },
      ],
    })
    await boot('/admin/resellers/u9', 'en', resellerAnswer(detail(all)))
    await vi.waitFor(() => expect(document.querySelector('[data-targets]')).not.toBeNull())
    const t = text(document.querySelector('[data-targets]'))
    expect(t).toContain('Every tier is reached this month.')
    expect(t).toContain('5 services: 50,000 T bonus · reached, bonus not paid')
    expect(t).not.toContain('for the next bonus')
  })

  it('show it in Persian', async () => {
    await boot('/admin/resellers/u9', 'fa', resellerAnswer(detail(month())))
    await vi.waitFor(() => expect(document.querySelector('[data-targets]')).not.toBeNull())
    const t = text(document.querySelector('[data-targets]'))
    expect(t).toContain('هدف ماهانه')
    expect(t).toContain('۲ سرویس: ۱۰٬۰۰۰ تومان پاداش · پاداش پرداخت شد')
    expect(t).toContain('۵ سرویس: ۵۰٬۰۰۰ تومان پاداش · هنوز نرسیده')
    expect(t).toContain('۲ سرویس دیگر تا پاداش بعدی، ۵۰٬۰۰۰ تومان.')
  })

  it('show no card while the targets are off', async () => {
    await boot('/admin/resellers/u9', 'en', resellerAnswer(detail()))
    await vi.waitFor(() => expect(document.querySelector('.app-facts')).not.toBeNull())
    expect(document.querySelector('[data-targets]')).toBeNull()
  })

  it('read the month from an answer, and none from an older one', () => {
    expect(resellerOf(detail(month())).targets?.sold).toBe(3)
    expect(resellerOf(detail()).targets).toBeNull()
  })
})
