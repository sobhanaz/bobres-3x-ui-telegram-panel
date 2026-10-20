import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReportBucket, ReportCell, ReportMethod, ReportPlan, ReportSummary } from '../reports'
import type { HoursAnswer, PlanSeriesAnswer } from '../reportsvisual'

// The newer parts of the Reports page, booted like the real app against a fake core: the charts that need the plan
// series and the hours of the week, the Professional report (the period before, the sentences, the 3D charts and
// when they give way to the flat ones, the calendar, print), in English and in Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const all = (root: ParentNode, sel: string) => [...root.querySelectorAll<HTMLElement>(sel)]
const q = (sel: string) => document.querySelector<HTMLElement>(sel)

const flow = (count = 0, amount = 0) => ({ count, amount })
const cell = (c: Partial<ReportCell>): ReportCell => ({
  revenue: 0, refunds: flow(), net: 0, orders: 0, new_orders: 0, renewals: 0, traffic_topups: 0, topups: flow(), rejected: flow(), gateway_failed: flow(), cancelled: flow(), ...c,
})
const bucket = (date: string, irt: Partial<ReportCell>, p: Partial<ReportBucket> = {}): ReportBucket => ({
  date, partial: false, new_customers: 0, trials: 0, churned_services: 0, churned_customers: 0, at_risk: 0, currencies: { IRT: cell(irt) }, ...p,
})
const summary = (): ReportSummary => ({
  from: '2026-10-15', to: '2026-10-17', group: 'day', calendar: 'gregorian', tz: 'Asia/Tehran', churn_grace_days: 7, currencies: ['IRT'],
  buckets: [
    bucket('2026-10-15', { revenue: 100_000, net: 100_000, orders: 1, new_orders: 1 }, { new_customers: 1, churned_services: 2 }),
    bucket('2026-10-16', {}, { trials: 1 }),
    bucket('2026-10-17', { revenue: 250_000, refunds: flow(1, 50_000), net: 200_000, orders: 2, new_orders: 1, traffic_topups: 1 }, { new_customers: 1, churned_services: 1 }),
  ],
  totals: {
    new_customers: 2, trials: 1, churned_services: 3, churned_customers: 2, at_risk: 0,
    currencies: { IRT: cell({ revenue: 350_000, refunds: flow(1, 50_000), net: 300_000, orders: 3, new_orders: 2, traffic_topups: 1 }) },
  },
})
// the three days before: 280,000 in 4 orders, 4 new customers, 1 free trial, 1 service lost, no customer lost
const before = (): ReportSummary => ({
  ...summary(), from: '2026-10-12', to: '2026-10-14', group: 'month', buckets: [bucket('2026-10-01', { revenue: 280_000, net: 280_000, orders: 4 })],
  totals: { new_customers: 4, trials: 1, churned_services: 1, churned_customers: 0, at_risk: 0, currencies: { IRT: cell({ revenue: 280_000, net: 280_000, orders: 4 }) } },
})
const plans: ReportPlan[] = [{ plan_id: 'p1', name: { fa: 'پایه', en: 'Basic' }, currency: 'IRT', orders: 3, revenue: 350_000 }]
const methods: ReportMethod[] = [
  { method: 'wallet', provider: '', currency: 'IRT', orders: 2, revenue: 250_000 },
  { method: 'manual', provider: 'manual_card', currency: 'IRT', orders: 1, revenue: 100_000 },
]
const planSeries = (): PlanSeriesAnswer => ({
  from: '2026-10-15', to: '2026-10-17', group: 'day', calendar: 'gregorian', tz: 'Asia/Tehran',
  series: [
    { plan_id: 'p1', name: { fa: 'پایه', en: 'Basic' }, currency: 'IRT', orders: 2, revenue: 250_000, points: [{ date: '2026-10-15', orders: 1, revenue: 100_000 }, { date: '2026-10-17', orders: 1, revenue: 150_000 }] },
    { plan_id: 'p3', name: { fa: 'ویژه', en: 'Pro' }, currency: 'IRT', orders: 1, revenue: 100_000, points: [{ date: '2026-10-17', orders: 1, revenue: 100_000 }] },
  ],
})
const hours = (): HoursAnswer => ({
  from: '2026-10-15', to: '2026-10-17', tz: 'Asia/Tehran',
  hours: [
    { weekday: 6, hour: 21, currency: 'IRT', orders: 3, revenue: 250_000 }, // Saturday
    { weekday: 1, hour: 9, currency: 'IRT', orders: 1, revenue: 100_000 }, // Monday
  ],
})

type Answer = (url: string) => Response | undefined
interface Env {
  /** a screen at least 640 px wide */
  wide?: boolean
  /** prefers-reduced-motion */
  reduced?: boolean
  lang?: 'en' | 'fa'
}

async function boot(path: string, answer: Answer, env: Env = {}) {
  unmount()
  const lang = env.lang ?? 'en'
  localStorage.setItem('bobres.lang', lang)
  vi.resetModules()
  window.history.replaceState(null, '', path)
  document.body.innerHTML = '<div id="app"></div>'
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('matchMedia', (media: string) => ({
    matches: media.includes('min-width') ? !!env.wide : media.includes('reduce') ? !!env.reduced : false,
    media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
  }))
  const fetchMock = vi.fn(async (url: string) => {
    const own = answer(url)
    if (own) return own
    if (url === '/api/v1/me') {
      return json(200, {
        user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'admin', language: lang }, permissions: ['reports.read'], csrf: 'c',
        session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
      })
    }
    return json(503, { error: 'unavailable', message: 'not in this test' })
  })
  vi.stubGlobal('fetch', fetchMock)
  await import('../main')
  const { router } = await import('../router')
  await router.isReady()
  return { fetchMock, router }
}

const params = (url: string) => new URL(url, 'https://x.example').searchParams
/** What core answers: the summary of the range, or of the three days before it, plans, methods and the two newer answers. */
const core =
  (over: { summary?: (url: string) => Response | undefined; series?: boolean; hours?: boolean; previous?: () => Response } = {}): Answer =>
  (url) => {
    if (url === '/api/v1/reports/daily-summary') return json(503, { error: 'unavailable', message: 'x' })
    if (url.startsWith('/api/v1/reports/summary')) {
      const mine = over.summary?.(url)
      if (mine) return mine
      const p = params(url)
      if (p.get('group') === 'month' && p.get('from')) return over.previous ? over.previous() : json(200, before())
      return json(200, { ...summary(), calendar: p.get('calendar') === 'jalali' ? 'jalali' : 'gregorian', group: p.get('group') || 'day' })
    }
    if (url.startsWith('/api/v1/reports/plans')) return json(200, { plans })
    if (url.startsWith('/api/v1/reports/methods')) return json(200, { methods })
    if (url.startsWith('/api/v1/reports/plan-series')) return over.series === false ? json(503, { error: 'unavailable', message: 'x' }) : json(200, planSeries())
    if (url.startsWith('/api/v1/reports/hours')) return over.hours === false ? json(503, { error: 'unavailable', message: 'x' }) : json(200, hours())
    return undefined
  }

const found = async <T extends Element>(selector: string) =>
  vi.waitFor(() => {
    const el = document.querySelector<T>(selector)
    expect(el, selector).not.toBeNull()
    return el!
  }, { timeout: 8000 })
const urls = (f: { mock: { calls: unknown[][] } }) => f.mock.calls.map((c) => String(c[0]))
const press = (container: string, label: string) => {
  const b = all(document, `${container} [role="button"], ${container} button`).find((e) => text(e) === label)
  expect(b, label).toBeDefined()
  b!.click()
}
const chartTable = async (selector: string) => {
  const btn = await found<HTMLButtonElement>(`${selector} .table-btn`)
  if (btn.getAttribute('aria-pressed') !== 'true') btn.click()
  await vi.waitFor(() => expect(q(`${selector} .chart-table`)).not.toBeNull())
  return all(document, `${selector} .chart-table tbody tr`).map((r) => [...r.children].map(text))
}

afterEach(() => {
  unmount()
  try {
    localStorage.removeItem('bobres.lang')
    localStorage.removeItem('bobres.reports.calendar')
  } catch {
    /* nothing was stored */
  }
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('the charts that need the newer answers', () => {
  it('draws revenue by plan over the periods and the busiest hours, one currency at a time', async () => {
    const { fetchMock } = await boot('/admin/reports', core())
    await found('.plans-chart')
    // the plan series are asked for the very days, zone, group of the summary (and the calendar, when it is Jalali)
    const asked = urls(fetchMock).find((u) => u.startsWith('/api/v1/reports/plan-series'))!
    expect(Object.fromEntries(params(asked))).toEqual({ from: '2026-10-15', to: '2026-10-17', tz: 'Asia/Tehran', group: 'day' })
    const hoursAsked = urls(fetchMock).find((u) => u.startsWith('/api/v1/reports/hours'))!
    expect(Object.fromEntries(params(hoursAsked))).toEqual({ from: '2026-10-15', to: '2026-10-17', tz: 'Asia/Tehran' })
    // a stacked column per period, a part per plan, adding up to the day's revenue
    expect(all(document, '.plans-chart .legend .item').map(text)).toEqual(['Basic', 'Pro'])
    expect(await chartTable('.plans-chart')).toEqual([
      ['15 Oct 2026', '100,000 Toman', '0 Toman'],
      ['16 Oct 2026', '0 Toman', '0 Toman'],
      ['17 Oct 2026', '150,000 Toman', '100,000 Toman'],
    ])
    // the hours of the week: the week reads from Monday in the Gregorian calendar, the hour with most orders is named
    await found('.heat-chart')
    expect(all(document, '.heat-chart .row-label').map(text)).toEqual(['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'])
    expect(q('.heat-chart .plot')?.getAttribute('aria-label')).toBe('Busiest time: Saturday around 21:00, with 3 paid orders')
    expect(text(q('.heat-note'))).toContain('Asia/Tehran')
    expect(all(document, '.heat-chart .cells rect').filter((r) => r.getAttribute('data-level') === '6')).toHaveLength(1)
  })

  it('reads the week from Saturday once the calendar is Jalali, and asks for Jalali periods', async () => {
    const { fetchMock } = await boot('/admin/reports', core())
    await found('.heat-chart')
    press('.calendar', 'Jalali')
    await vi.waitFor(() => expect(all(document, '.heat-chart .row-label').map(text)[0]).toBe('Saturday'))
    await vi.waitFor(() => expect(urls(fetchMock).some((u) => u.startsWith('/api/v1/reports/plan-series') && u.includes('calendar=jalali'))).toBe(true))
    expect(all(document, '.heat-chart .row-label').map(text)).toEqual(['Saturday', 'Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday'])
  })

  it('leaves those charts out, and keeps the rest, when core cannot answer them (an older core)', async () => {
    await boot('/admin/reports', core({ series: false, hours: false }))
    await found('.revenue-chart')
    expect(q('.plans-chart')).toBeNull()
    expect(q('.heat-chart')).toBeNull()
    expect(q('.methods-chart')).not.toBeNull()
    expect(q('.orders-chart')).not.toBeNull()
    expect(q('.report-error')).toBeNull() // no error for what is only a nicety
  })

  it('keeps the table of periods in a box of its own with its header, first column and totals held in place', async () => {
    await boot('/admin/reports', core())
    await found('.period-table')
    const box = q('.period-scroll')!
    expect(box.getAttribute('tabindex')).toBe('0') // it scrolls by keyboard
    expect(box.getAttribute('role')).toBe('region')
    expect(box.getAttribute('aria-label')).toBe('By period')
    expect(all(document, '.period-table thead th').every((th) => th.getAttribute('scope') === 'col')).toBe(true)
    expect(all(document, '.period-table tbody th').every((th) => th.getAttribute('scope') === 'row')).toBe(true)
    // the totals row is the foot of the table, one number per column, the sum of the periods
    const foot = all(document, '.period-table tfoot td').map(text)
    expect(text(q('.period-table tfoot th'))).toBe('Total')
    expect(foot[0]).toBe('350,000 Toman')
    expect(foot[1]).toBe('3')
    expect(foot.slice(-4)).toEqual(['2', '1', '3', '0'])
    expect(all(document, '.period-table tbody tr')).toHaveLength(3)
  })
})

describe('the Professional report', () => {
  const kpi = (key: string) => q(`.cur-block[data-currency="IRT"] [data-kpi="${key}"]`)!
  const story = (key: string) => text(kpi(key).querySelector('.kpi-story'))
  const open = async (env: Env = {}, over?: Parameters<typeof core>[0], path = '/admin/reports?view=report') => {
    const b = await boot(path, core(over), env)
    await found('.pro-report')
    return b
  }

  it('is a view of the page, in the address, and asks for the period before only when it is shown', async () => {
    const { fetchMock, router } = await boot('/admin/reports', core())
    await found('.currency-card')
    expect(urls(fetchMock).some((u) => u.includes('group=month'))).toBe(false) // the charts view does not need it
    expect(q('.pro-report')).toBeNull()
    press('.view-switch', 'Professional report')
    await found('.pro-report')
    expect(router.currentRoute.value.query.view).toBe('report')
    const prev = await vi.waitFor(() => {
      const u = urls(fetchMock).find((x) => x.startsWith('/api/v1/reports/summary') && x.includes('group=month'))
      expect(u).toBeDefined()
      return u!
    })
    // the same number of days right before the range, in the same zone, in the cheapest grouping (only its totals are used)
    expect(Object.fromEntries(params(prev))).toEqual({ from: '2026-10-12', to: '2026-10-14', tz: 'Asia/Tehran', group: 'month' })
    expect(q('.currency-card')).toBeNull() // the report replaces the cards and tables
    press('.view-switch', 'Charts and tables')
    await found('.currency-card')
    expect(router.currentRoute.value.query.view).toBeUndefined()
  })

  it('opens straight from the address, with a letterhead: the store, the title, the days, the zone and what it is compared with', async () => {
    await open()
    const head = text(q('.pro-head'))
    expect(head).toContain('Shop')
    expect(head).toContain('Sales report')
    expect(head).toContain('15 Oct 2026 to 17 Oct 2026')
    expect(head).toContain('Asia/Tehran')
    expect(head).toMatch(/prepared on \d+ \w+ \d{4}/)
    await vi.waitFor(() => expect(text(q('.pro-head .compared'))).toBe('Every change is against the 3-day period right before this one, 12 Oct 2026 to 14 Oct 2026.'))
  })

  it('writes a headline for each currency in plain words', async () => {
    await open()
    await vi.waitFor(() => expect(q('.cur-block .lead')).not.toBeNull())
    const lead = all(document, '.cur-block[data-currency="IRT"] .lead p').map(text)
    expect(lead).toEqual([
      'Customers paid for 3 orders in this period, worth 350,000 Toman.',
      'The best day was 17 Oct 2026, with 250,000 Toman.',
      'Refunds: 1 refund, worth 50,000 Toman.',
      'Basic sold best: 100% of the revenue.',
      'Most of the money came in through Wallet balance: 71% of the revenue.',
      'Customers paid most often on Saturday around 21:00.',
    ])
  })

  it('shows each headline number with its change on the period before and a sentence about it', async () => {
    await open()
    await vi.waitFor(() => expect(q('.kpi-delta')).not.toBeNull())
    expect(text(kpi('revenue').querySelector('.kpi-value'))).toBe('350,000 Toman')
    expect(text(kpi('revenue').querySelector('.kpi-delta'))).toBe('▲ 25%')
    expect(story('revenue')).toBe('Revenue was 350,000 Toman, 25% higher than the previous 3 days.')
    expect(story('net')).toBe('After refunds you kept 300,000 Toman, 7% higher than the previous 3 days.')
    expect(story('orders')).toBe('Customers paid for 3 orders, 25% lower than the previous 3 days.')
    expect(text(kpi('orders').querySelector('.kpi-delta'))).toBe('▼ 25%')
    expect(story('aov')).toBe('The average order was worth 116,667 Toman, 67% higher than the previous 3 days.')
    // a rise in revenue is good (green), a fall in orders is not
    expect(kpi('revenue').querySelector('.kpi-delta')?.classList.contains('tone-good')).toBe(true)
    expect(kpi('orders').querySelector('.kpi-delta')?.classList.contains('tone-bad')).toBe(true)
    // each card draws a small trend and says it in words
    expect(kpi('revenue').querySelector('svg[role="img"]')?.getAttribute('aria-label')).toBe('Revenue: rising over the period')
  })

  it('counts a rise in lost services as bad news, and customers where there were none as new', async () => {
    await open()
    await vi.waitFor(() => expect(q('.people-block .kpi-delta')).not.toBeNull())
    const p = (key: string) => q(`.people-block [data-kpi="${key}"]`)!
    expect(text(p('new_customers').querySelector('.kpi-story'))).toBe('2 customers made a first purchase, 50% lower than the previous 3 days.')
    expect(p('new_customers').querySelector('.kpi-delta')?.classList.contains('tone-bad')).toBe(true) // fewer new customers
    expect(text(p('trials').querySelector('.kpi-story'))).toBe('1 person started a free trial, exactly the same as the previous 3 days.')
    expect(p('trials').querySelector('.kpi-delta')?.classList.contains('tone-flat')).toBe(true)
    expect(text(p('churned_services').querySelector('.kpi-story'))).toBe('3 services ended without being renewed, 200% higher than the previous 3 days.')
    expect(p('churned_services').querySelector('.kpi-delta')?.classList.contains('tone-bad')).toBe(true) // more lost: bad, though it went up
    expect(text(p('churned_customers').querySelector('.kpi-story'))).toBe('2 customers lost their last service, while the previous 3 days had none.')
    expect(text(p('churned_customers').querySelector('.kpi-delta'))).toBe('▲ new')
  })

  it('says each number alone when there is nothing to compare with: a failed request, or no earlier period', async () => {
    await open({}, { previous: () => json(503, { error: 'unavailable', message: 'x' }) })
    await vi.waitFor(() => expect(text(q('.pro-head .compared'))).toBe('The period before could not be loaded, so changes are not shown.'))
    expect(all(document, '.kpi-delta')).toHaveLength(0)
    expect(story('revenue')).toBe('Revenue was 350,000 Toman.')
    // a range that starts in the first days core reports on has no period before it, and none is asked for
    const early = summary()
    early.from = '2000-01-02'
    early.to = '2000-01-04'
    early.buckets = early.buckets.map((b, i) => ({ ...b, date: `2000-01-0${2 + i}` }))
    const { fetchMock } = await open({}, { summary: (u) => (params(u).get('group') === 'month' ? undefined : json(200, early)) })
    await vi.waitFor(() => expect(text(q('.pro-head .compared'))).toBe('There is no earlier period to compare with.'))
    expect(urls(fetchMock).some((u) => u.includes('group=month'))).toBe(false)
    expect(story('revenue')).toBe('Revenue was 350,000 Toman.')
  })

  it('writes the same report in Persian, right to left, with Persian digits and the Jalali calendar', async () => {
    await open({ lang: 'fa' })
    expect(document.documentElement.dir).toBe('rtl')
    await vi.waitFor(() => expect(q('.kpi-delta')).not.toBeNull())
    const head = text(q('.pro-head'))
    expect(head).toContain('گزارش فروش')
    expect(head).toContain('۲۳ مهر ۱۴۰۵ تا ۲۵ مهر ۱۴۰۵')
    expect(text(q('.pro-head .compared'))).toBe('همهٔ تغییرها نسبت به بازهٔ ۳ روزهٔ درست پیش از این بازه است، ۲۰ مهر ۱۴۰۵ تا ۲۲ مهر ۱۴۰۵.')
    expect(story('revenue')).toBe('درآمد ۳۵۰٬۰۰۰ تومان بود، ۲۵٪ بیشتر از ۳ روز قبل.')
    expect(story('orders')).toBe('مشتری‌ها ۳ سفارش را پرداخت کردند، ۲۵٪ کمتر از ۳ روز قبل.')
    expect(text(q('.cur-block[data-currency="IRT"] .lead-main'))).toBe('در این بازه مشتری‌ها ۳ سفارش را پرداخت کردند، به ارزش ۳۵۰٬۰۰۰ تومان.')
    expect(text(kpi('revenue').querySelector('.kpi-delta'))).toBe('▲ ۲۵٪')
    // every text is in the catalog: nothing shows as a key
    expect(text(q('.pro-report'))).not.toMatch(/reports\.pro|charts\./)
    // the busiest hour is named by the Persian weekday and hour
    expect(text(q('.cur-block[data-currency="IRT"] .lead'))).toContain('شنبه حوالی ساعت ۲۱:۰۰')
  })

  it('follows the Calendar switch: the days of the report and of the comparison are written in the calendar chosen', async () => {
    await open()
    await vi.waitFor(() => expect(text(q('.pro-head .compared'))).toContain('12 Oct 2026'))
    press('.calendar', 'Jalali')
    await vi.waitFor(() => expect(text(q('.pro-head .pro-period'))).toBe('23 Mehr 1405 to 25 Mehr 1405'))
    await vi.waitFor(() => expect(text(q('.pro-head .compared'))).toBe('Every change is against the 3-day period right before this one, 20 Mehr 1405 to 22 Mehr 1405.'))
    expect(text(q('.cur-block[data-currency="IRT"] .lead'))).toContain('The best day was 25 Mehr 1405')
    // the hours of the week read from Saturday now
    expect(all(document, '.cur-block[data-currency="IRT"] .busy-hours .row-label').map(text)[0]).toBe('Saturday')
    press('.calendar', 'Gregorian')
    await vi.waitFor(() => expect(text(q('.pro-head .pro-period'))).toBe('15 Oct 2026 to 17 Oct 2026'))
  })

  it('prints with the browser: one button, and the screen only parts marked to stay off the paper', async () => {
    const print = vi.fn()
    vi.stubGlobal('print', print)
    await open({ wide: true })
    const btn = await found<HTMLButtonElement>('.print-btn')
    expect(text(btn)).toBe('Print or save as PDF')
    btn.click()
    expect(print).toHaveBeenCalledTimes(1)
    expect(btn.closest('.no-print')).not.toBeNull() // the button, like the menus and the toolbar, stays off the page
    expect(all(document, '.app-no-print').length).toBeGreaterThan(2) // the hint, the toolbar, the exports
  })

  it('also has every chart as a table, for screen readers and for exact numbers', async () => {
    await open()
    await vi.waitFor(() => expect(q('.revenue-lines')).not.toBeNull())
    expect(await chartTable('.revenue-lines')).toHaveLength(3)
    expect(await chartTable('.top-plans')).toEqual([['Basic', '350,000 Toman']])
  })
})

describe('3D, and when it gives way to the flat charts', () => {
  const open = async (env: Env, over?: Parameters<typeof core>[0]) => {
    const b = await boot('/admin/reports?view=report', core(over), env)
    await found('.pro-report')
    return b
  }

  it('draws the plan columns and the payment donut in 3D on a wide screen, loading that part only then', async () => {
    await open({ wide: true })
    await found('.plans-3d')
    expect(q('.plans-flat')).toBeNull()
    expect(all(document, '.plans-3d .column polygon').length).toBeGreaterThan(5)
    expect(q('.plans-3d .scene')?.getAttribute('aria-roledescription')).toBe('chart')
    await found('.methods-3d')
    expect(all(document, '.methods-3d path.top')).toHaveLength(2)
    expect(q('.methods-flat')).toBeNull()
    // the same plans, the same numbers, in the table of the 3D chart
    expect(await chartTable('.plans-3d')).toEqual([
      ['15 Oct 2026', '100,000 Toman', '0 Toman'],
      ['16 Oct 2026', '0 Toman', '0 Toman'],
      ['17 Oct 2026', '150,000 Toman', '100,000 Toman'],
    ])
  })

  it('lets the person switch to the flat charts, and back', async () => {
    await open({ wide: true })
    await found('.plans-3d')
    press('.mode', 'Flat')
    await found('.plans-flat')
    expect(q('.plans-3d')).toBeNull()
    expect(q('.methods-3d')).toBeNull()
    expect(q('.methods-flat')).not.toBeNull()
    press('.mode', '3D')
    await found('.plans-3d')
    expect(q('.plans-flat')).toBeNull()
  })

  it('shows the flat charts first when the person prefers reduced motion, and 3D only if they ask', async () => {
    await open({ wide: true, reduced: true })
    await found('.plans-flat')
    expect(q('.plans-3d')).toBeNull()
    press('.mode', '3D')
    await found('.plans-3d')
  })

  it('shows only the flat charts on a phone, with no switch to offer', async () => {
    await open({ wide: false })
    await found('.plans-flat')
    expect(q('.plans-3d')).toBeNull()
    expect(q('.methods-3d')).toBeNull()
    expect(q('.methods-flat')).not.toBeNull()
    expect(q('.mode')).toBeNull()
    expect(q('.print-btn')).not.toBeNull() // printing works from a phone too
  })

  it('shows the flat charts for a range with too many periods for a 3D scene to read', async () => {
    const long = summary()
    long.buckets = Array.from({ length: 70 }, (_, i) => ({ ...summary().buckets[i % 3], date: new Date(Date.UTC(2026, 6, 1 + i)).toISOString().slice(0, 10) }))
    long.from = long.buckets[0].date
    long.to = long.buckets[69].date
    await open({ wide: true }, { summary: (u) => (params(u).get('group') === 'month' ? undefined : json(200, long)) })
    await found('.plans-flat')
    expect(q('.plans-3d')).toBeNull()
    expect(q('.mode')).toBeNull() // 3D is not on offer for this range
  })

  it('does without 3D data too: no plan answer, no plan chart, the rest of the report stands', async () => {
    await open({ wide: true }, { series: false })
    await found('.revenue-lines')
    expect(q('.plans-3d')).toBeNull()
    expect(q('.plans-flat')).toBeNull()
    expect(q('.methods-3d')).not.toBeNull()
  })
})
